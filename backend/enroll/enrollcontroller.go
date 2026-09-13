package enroll

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/DiegoMunevar2007/Proyecto-1-Cloud.git/auth"
	"github.com/DiegoMunevar2007/Proyecto-1-Cloud.git/courses"
	"github.com/DiegoMunevar2007/Proyecto-1-Cloud.git/utils"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

// _ conserva el import para las anotaciones swag.
var _ = utils.ErrorResponse{}

// Handler agrupa los endpoints de inscripción con sus dependencias.
type Handler struct {
	DB  *gorm.DB
	RDB *redis.Client
}

// SetupEnrollRoutes registra las rutas de inscripción (estudiante autenticado).
func SetupEnrollRoutes(router *gin.RouterGroup, db *gorm.DB, rdb *redis.Client) {
	h := &Handler{DB: db, RDB: rdb}
	requireAuth := auth.RequireAuth(rdb)
	g := router.Group("/enrollments")
	{
		g.GET("", requireAuth, h.Mine)
		g.POST("", requireAuth, h.Enroll)
		g.POST("/:course_id/reenroll", requireAuth, h.Reenroll)
		g.DELETE("/:course_id", requireAuth, h.Withdraw)
	}
	// Inscritos del curso (autor o admin). Vive aquí para no importar
	// este paquete desde courses (ciclo).
	router.GET("/courses/:id/enrollments", auth.RequireRole(rdb, auth.RoleProfessor, auth.RoleAdmin), h.CourseEnrollments)
}

func studentOf(h *Handler, c *gin.Context) uint {
	uid, _ := auth.LookupUser(h.DB, c.GetString("username"))
	return uid
}

func writeErr(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrNotPublished):
		c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
	case errors.Is(err, courses.ErrNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
	}
}

// Mine lista las inscripciones del estudiante.
//
//	@Summary	Mis inscripciones
//	@Description	Lista cursos con inscripción del estudiante, incluyendo retirados (progreso conservado).
//	@Tags			Inscripción
//	@Produce		json
//	@Param			Authorization	header		string	true	"Bearer <token>"
//	@Security		BearerAuth
//	@Success		200	{object}	EnrollmentsResponse	"Inscripciones"
//	@Router			/api/v1/enrollments [get]
func (h *Handler) Mine(c *gin.Context) {
	items, err := Mine(h.DB, studentOf(h, c))
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"enrollments": items, "total": len(items)})
}

// CourseEnrollments lista los inscritos activos de un curso (autor o admin).
//
//	@Summary	Inscritos del curso
//	@Description	Solo el autor propietario o admin. Los retirados no aparecen.
//	@Tags			Inscripción
//	@Produce		json
//	@Param			Authorization	header		string	true	"Bearer <token>"
//	@Param			id				path		int		true	"ID del curso"
//	@Security		BearerAuth
//	@Success		200	{object}	EnrollmentsResponse	"Inscritos"
//	@Failure		403	{object}	utils.ErrorResponse	"Sin permiso"
//	@Failure		404	{object}	utils.ErrorResponse	"No encontrado"
//	@Router			/api/v1/courses/{id}/enrollments [get]
func (h *Handler) CourseEnrollments(c *gin.Context) {
	courseID, _ := strconv.ParseUint(c.Param("id"), 10, 32)
	var course courses.Course
	if err := h.DB.First(&course, uint(courseID)).Error; err != nil {
		c.JSON(404, gin.H{"error": "no encontrado"})
		return
	}
	uid, role := auth.LookupUser(h.DB, c.GetString("username"))
	if !courses.IsOwnerOrAdmin(&course, uid, role) {
		c.JSON(403, gin.H{"error": "sin permiso"})
		return
	}
	items, err := MineByCourse(h.DB, uint(courseID))
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"enrollments": items, "total": len(items)})
}

// Enroll inscribe al estudiante en un curso publicado. Idempotente.
//
//	@Summary	Inscribirse
//	@Description	Crea o reactiva la inscripción; re-POST no duplica ni borra progreso.
//	@Tags			Inscripción
//	@Produce		json
//	@Param			Authorization	header		string			true	"Bearer <token>"
//	@Param			request			body		EnrollRequest	true	"Curso"
//	@Security		BearerAuth
//	@Success		201	{object}	MatriculaEnvelope	"Inscrito"
//	@Success		200	{object}	MatriculaEnvelope	"Ya inscrito"
//	@Failure		403	{object}	utils.ErrorResponse	"Curso no publicado"
//	@Failure		404	{object}	utils.ErrorResponse	"No encontrado"
//	@Router			/api/v1/enrollments [post]
func (h *Handler) Enroll(c *gin.Context) {
	var req EnrollRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.CourseID == 0 {
		c.JSON(400, gin.H{"error": "course_id requerido"})
		return
	}
	m, created, err := Enroll(h.DB, studentOf(h, c), req.CourseID)
	if err != nil {
		writeErr(c, err)
		return
	}
	if created {
		c.JSON(201, gin.H{"enrollment": m})
		return
	}
	c.JSON(200, gin.H{"enrollment": m})
}

// Reenroll reinscribe tras un retiro, conservando progreso y resultados.
//
//	@Summary	Reinscribirse
//	@Description	Reactiva la inscripción sin tocar progreso ni notas.
//	@Tags			Inscripción
//	@Produce		json
//	@Param			Authorization	header		string	true	"Bearer <token>"
//	@Param			course_id		path		int		true	"ID del curso"
//	@Security		BearerAuth
//	@Success		200	{object}	MatriculaEnvelope	"Reinscrito"
//	@Failure		403	{object}	utils.ErrorResponse	"Curso no publicado"
//	@Failure		404	{object}	utils.ErrorResponse	"No encontrado"
//	@Router			/api/v1/enrollments/{course_id}/reenroll [post]
func (h *Handler) Reenroll(c *gin.Context) {
	courseID, _ := strconv.ParseUint(c.Param("course_id"), 10, 32)
	m, _, err := Enroll(h.DB, studentOf(h, c), uint(courseID))
	if err != nil {
		writeErr(c, err)
		return
	}
	c.JSON(200, gin.H{"enrollment": m})
}

// Withdraw retira la inscripción sin borrar progreso.
//
//	@Summary	Retirarse
//	@Description	Marca Inscrito=false; el progreso y las notas se conservan para reinscripción.
//	@Tags			Inscripción
//	@Produce		json
//	@Param			Authorization	header		string	true	"Bearer <token>"
//	@Param			course_id		path		int		true	"ID del curso"
//	@Security		BearerAuth
//	@Success		200	{object}	utils.MessageResponse	"Retiro registrado"
//	@Failure		404	{object}	utils.ErrorResponse	"No encontrado"
//	@Router			/api/v1/enrollments/{course_id} [delete]
func (h *Handler) Withdraw(c *gin.Context) {
	courseID, _ := strconv.ParseUint(c.Param("course_id"), 10, 32)
	if err := Withdraw(h.DB, studentOf(h, c), uint(courseID)); err != nil {
		writeErr(c, err)
		return
	}
	c.JSON(200, gin.H{"message": "retiro registrado (progreso conservado)"})
}
