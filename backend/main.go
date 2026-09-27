package main

import (
	"context"
	_ "embed"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/DiegoMunevar2007/Proyecto-1-Cloud.git/admin"
	"github.com/DiegoMunevar2007/Proyecto-1-Cloud.git/auth"
	"github.com/DiegoMunevar2007/Proyecto-1-Cloud.git/badges"
	"github.com/DiegoMunevar2007/Proyecto-1-Cloud.git/courses"
	"github.com/DiegoMunevar2007/Proyecto-1-Cloud.git/enroll"
	"github.com/DiegoMunevar2007/Proyecto-1-Cloud.git/progress"
	"github.com/DiegoMunevar2007/Proyecto-1-Cloud.git/queue"
	"github.com/DiegoMunevar2007/Proyecto-1-Cloud.git/quiz"
	"github.com/DiegoMunevar2007/Proyecto-1-Cloud.git/storage"
	"github.com/DiegoMunevar2007/Proyecto-1-Cloud.git/telemetry"
	"github.com/DiegoMunevar2007/Proyecto-1-Cloud.git/uploads"
	"github.com/DiegoMunevar2007/Proyecto-1-Cloud.git/utils"
	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/redis/go-redis/v9"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// openAPIJSON es la especificación OpenAPI 3.1 generada con swag.
// Se regenera con: swag init -g main.go -o docs --ot json,yaml --v3.1 --packageName docs
//
//go:embed docs/openapi.json
var openAPIJSON []byte

var ctx = context.Background()

// HealthResponse indica el estado de las dependencias de la API.
type HealthResponse struct {
	Status  string `json:"status" example:"ok"`
	Message string `json:"message,omitempty" example:"Error al conectar con Redis"`
}

// healthCheck expone el endpoint de salud con sus dependencias.
//
//	@Summary		Salud del servicio
//	@Description	Verifica la conectividad con PostgreSQL y Redis. Usado por Docker healthchecks y monitoreo.
//	@Tags			Operación
//	@Produce		json
//	@Success		200	{object}	HealthResponse	"Servicio y dependencias Saludables"
//	@Failure		500	{object}	HealthResponse	"Alguna dependencia falló"
//	@Router			/health [get]
func healthCheck(db *gorm.DB, rdb *redis.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Ping base de datos PostgreSQL
		if err := db.Exec("SELECT 1").Error; err != nil {
			c.JSON(500, gin.H{"status": "error", "message": "Error al conectar con la base de datos PostgreSQL"})
			return
		}
		// Ping Redis
		if err := rdb.Ping(ctx).Err(); err != nil {
			c.JSON(500, gin.H{"status": "error", "message": "Error al conectar con Redis"})
			return
		}
		c.JSON(200, gin.H{"status": "ok"})
	}
}

// metricsRegistry contiene las métricas de la API. Es un registro propio y no
// el global porque el s3store de tusd ya registra sus colectores tusd_s3_* en el
// global, y ahí es donde se produciría el conflicto de registro.
//
// Lo scrapea el Ops Agent de Google Cloud, que corre en la misma VM y reenvía las
// series a Cloud Monitoring. Por eso la API publica además el estado de la cola:
// es la única forma de que profundidad, antigüedad y tasa de procesamiento queden
// en la misma fuente que las métricas del sistema, sin consultar Redis a mano.
var metricsRegistry, metricsRegisterer = telemetry.NewRegistry(telemetry.ComponentAPI)

var (
	// requestDuration Histograma de latencia por ruta, método y estado.
	requestDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "mooc_http_request_duration_seconds",
		Help:    "Latencia de las respuestas de la API por plantilla de ruta.",
		Buckets: []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30},
	}, []string{"route", "method", "status"})

	// requestsTotal Conteo de respuestas por ruta, método y estado.
	requestsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "mooc_http_requests_total",
		Help: "Respuestas servidas por la API por plantilla de ruta.",
	}, []string{"route", "method", "status"})

	// requestsInflight Peticiones serviéndose en este instante.
	requestsInflight = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "mooc_http_requests_inflight",
		Help: "Peticiones que la API está atender simultáneamente.",
	})
)

func init() {
	metricsRegisterer.MustRegister(requestDuration, requestsTotal, requestsInflight)
	// El estado de la cola se lee de Redis en cada scrape, no al arrancar.
	queueCollector := queue.NewCollector(queue.InspectMediaQueue)
	if err := queueCollector.Register(metricsRegisterer); err != nil {
		panic("no se pudo registrar el collector de la cola: " + err.Error())
	}
}

// instrumentRequest mide cada petición para el scrape de /metrics. Se etiqueta
// con FullPath, la plantilla de la ruta, y no con la URL pedida: usar el URL
// haría que cada :id generara una serie nueva y el scrape crecería sin límite.
// El propio scrape queda fuera para no medirse a sí mismo.
func instrumentRequest() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.URL.Path == "/metrics" {
			c.Next()
			return
		}
		start := time.Now()
		requestsInflight.Inc()
		defer requestsInflight.Dec()

		c.Next()

		route := c.FullPath()
		if route == "" {
			// gin no_full_path en 404 y en redirecciones: sin plantilla no hay
			// série, pero el código de estado sí queda registrado.
			route = "no_full_path"
		}
		status := strconv.Itoa(c.Writer.Status())
		requestDuration.WithLabelValues(route, c.Request.Method, status).
			Observe(time.Since(start).Seconds())
		requestsTotal.WithLabelValues(route, c.Request.Method, status).Inc()
	}
}

// metricsHandler sirve las métricas de la API en formato Prometheus, incluidas
// las de la cola asíncrona. El consumo es el Ops Agent de Google Cloud, que
// corre en la misma VM, así que el endpoint se puede dejar en el puerto interno
// del contenedor sin exponerse.
func metricsHandler() http.Handler {
	return promhttp.HandlerFor(metricsRegistry, promhttp.HandlerOpts{
		ErrorHandling: promhttp.ContinueOnError,
	})
}

func SetupRouter(db *gorm.DB, rdb *redis.Client) *gin.Engine {
	router := gin.Default()
	router.Use(instrumentRequest())

	router.GET("/health", healthCheck(db, rdb))

	// Métricas en formato Prometheus para el Ops Agent.
	router.GET("/metrics", gin.WrapH(metricsHandler()))

	// Especificación OpenAPI 3.1 (importable en Bruno/Postman).
	router.GET("/openapi.json", func(c *gin.Context) {
		c.Data(200, "application/json", openAPIJSON)
	})

	return router
}
func initPostgresDB() *gorm.DB {
	// Inicializar conexión a la base de datos PostgreSQL
	dsn := utils.GetPostgresDSN()
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		panic("No se pudo conectar a la base de datos")
	}
	return db
}

func initRedisClient() *redis.Client {
	// Inicializar cliente Redis
	rdb := redis.NewClient(&redis.Options{Addr: utils.RedisAddr(), Password: utils.RedisPassword()})
	if err := rdb.Ping(ctx).Err(); err != nil {
		panic(err)
	}
	return rdb
}

// @title			Plataforma MOOC - API
// @version		1.0
// @description	API REST del monolito modular: identidad y sesiones, administración y auditoría, autoría de cursos versionados y carga multimedia con procesamiento asíncrono a HLS.
// @description	Autenticación con JWT revocable: iniciar sesión en /api/v1/auth/login y enviar `Authorization: Bearer <token>`.
//
// @host		localhost:8080
// @schemes	http https
//
// @securityDefinitions.apikey	BearerAuth
// @in							header
// @name						Authorization
// @description				Token JWT de sesión. Formato: `Bearer <token>`.
func main() {
	// Inicializar conexión a la base de datos
	db := initPostgresDB()
	rdb := initRedisClient()
	if err := db.AutoMigrate(
		&auth.UserModel{},
		&admin.AuditLog{},
		&courses.Course{},
		&courses.CourseVersion{},
		&courses.Module{},
		&courses.Unit{},
		&courses.Resource{},
		&courses.Matricula{},
		&quiz.Quiz{},
		&quiz.Question{},
		&quiz.Attempt{},
		&progress.Progress{},
		&badges.Badge{},
	); err != nil {
		panic("No se pudo migrar el esquema: " + err.Error())
	}

	// Cola asynq (publicador) y almacenamiento S3-compatible.
	// Sin estado local: solo clientes externos, la API escala horizontalmente.
	qclient := queue.NewClient()
	defer qclient.Close()
	store, err := storage.NewClient()
	if err != nil {
		log.Printf("advertencia: almacenamiento no disponible: %v (upload-url retornará 501)", err)
		store = nil
	}
	courses.SetClients(qclient, store)

	// Inicializar el enrutador Gin
	router := SetupRouter(db, rdb)
	v1 := router.Group("/api/v1")
	auth.SetupAuthRoutes(v1, db, rdb)
	admin.SetupAdminRoutes(v1, db, rdb)
	courses.SetupCourseRoutes(v1, db, rdb)
	enroll.SetupEnrollRoutes(v1, db, rdb)
	quiz.SetupQuizRoutes(v1, db, rdb)
	progress.SetupProgressRoutes(v1, db, rdb)
	badges.SetupBadgesRoutes(v1, db, rdb)
	if err := uploads.SetupRoutes(v1, db, rdb, qclient); err != nil {
		panic("No se pudo montar TUS: " + err.Error())
	}

	// Iniciar el servidor
	if err := router.Run(":8080"); err != nil {
		panic(err)
	}
}
