package main

import (
	"context"
	_ "embed"
	"log"

	"github.com/DiegoMunevar2007/Proyecto-1-Cloud.git/admin"
	"github.com/DiegoMunevar2007/Proyecto-1-Cloud.git/auth"
	"github.com/DiegoMunevar2007/Proyecto-1-Cloud.git/courses"
	"github.com/DiegoMunevar2007/Proyecto-1-Cloud.git/queue"
	"github.com/DiegoMunevar2007/Proyecto-1-Cloud.git/storage"
	"github.com/DiegoMunevar2007/Proyecto-1-Cloud.git/utils"
	"github.com/gin-gonic/gin"
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

// healthHandler expone el endpoint de salud con sus dependencias.
type healthHandler struct {
	db  *gorm.DB
	rdb *redis.Client
}

// Check verifica la conectividad con PostgreSQL y Redis.
//
//	@Summary		Salud del servicio
//	@Description	Verifica la conectividad con PostgreSQL y Redis. Usado por Docker healthchecks y monitoreo.
//	@Tags			Operación
//	@Produce		json
//	@Success		200	{object}	HealthResponse	"Servicio y dependencias Saludables"
//	@Failure		500	{object}	HealthResponse	"Alguna dependencia falló"
//	@Router			/health [get]
func (h healthHandler) Check(c *gin.Context) {
	// Ping base de datos PostgreSQL
	dbErr := h.db.Exec("SELECT 1").Error
	// Ping Redis
	redisErr := h.rdb.Ping(ctx).Err()

	if dbErr != nil {
		c.JSON(500, gin.H{"status": "error", "message": "Error al conectar con la base de datos PostgreSQL"})
		return
	}
	if redisErr != nil {
		c.JSON(500, gin.H{"status": "error", "message": "Error al conectar con Redis"})
		return
	}
	c.JSON(200, gin.H{"status": "ok"})
}

func SetupRouter(db *gorm.DB, rdb *redis.Client) *gin.Engine {
	router := gin.Default()

	router.GET("/health", healthHandler{db: db, rdb: rdb}.Check)

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
	rdb := redis.NewClient(utils.GetRedisOptions())
	if err := rdb.Ping(ctx).Err(); err != nil {
		panic(err)
	}
	return rdb
}

// @title			Plataforma MOOC - API
// @version		1.0
// @description	API REST del monolito modular: identidad y sesiones, administración y auditoría, autoría de cursos versionados y carga multimedia con procesamiento asíncrono a HLS.
// @description	Autenticación con JWT revocable: iniciar sesión en /auth/login y enviar `Authorization: Bearer <token>`.
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
	); err != nil {
		panic("No se pudo migrar el esquema: " + err.Error())
	}

	// Cola asynq (publicador) y almacenamiento S3/MinIO.
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
	auth.SetupAuthRoutes(router, db, rdb)
	admin.SetupAdminRoutes(router, db, rdb)
	courses.SetupCourseRoutes(router, db, rdb)

	// Iniciar el servidor
	if err := router.Run(":8080"); err != nil {
		panic(err)
	}
}
