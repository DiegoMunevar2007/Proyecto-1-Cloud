package progress

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

// Handler agrupa los endpoints de progreso con sus dependencias.
type Handler struct {
	DB  *gorm.DB
	RDB *redis.Client
}

// SetupProgressRoutes registra las rutas de progreso (estudiante autenticado).
func SetupProgressRoutes(router *gin.RouterGroup, db *gorm.DB, rdb *redis.Client) {
	h := &Handler{DB: db, RDB: rdb}
	requireAuth := auth.RequireAuth(rdb)
	g := router.Group("/progress")
	{
		g.POST("/heartbeat", requireAuth, auth.RateLimit(10, 20), h.Heartbeat)
		g.GET("/:course_id", requireAuth, h.Summary)
	}
}

// Heartbeat registra reproducción/apertura. Rechaza avance declarado por el cliente.
//
//	@Summary	Heartbeat de progreso
//	@Description	Reporta posición y duración; el servidor decide la finalización. Enviar percent/completed es 400 y se audita.
//	@Tags			Progreso
//	@Produce		json
//	@Param			Authorization	header		string			true	"Bearer <token>"
//	@Param			request			body		HeartbeatInput	true	"Señal de reproducción"
//	@Security		BearerAuth
//	@Success		200	{object}	Progress	"Progreso actualizado"
//	@Failure		400	{object}	utils.ErrorResponse	"Señal inválida o manipulada"
//	@Failure		403	{object}	utils.ErrorResponse	"Sin inscripción"
//	@Failure		404	{object}	utils.ErrorResponse	"Recurso no encontrado"
//	@Router			/api/v1/progress/heartbeat [post]
func (h *Handler) Heartbeat(c *gin.Context) {
	var req HeartbeatInput
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "señal inválida"})
		return
	}
	uid, _ := auth.LookupUser(h.DB, c.GetString("username"))
	p, err := Heartbeat(h.DB, uid, req)
	if err != nil {
		switch {
		case errors.Is(err, ErrClientProgress), errors.Is(err, courses.ErrInvalidPayload):
			c.JSON(400, gin.H{"error": err.Error()})
		case errors.Is(err, courses.ErrForbidden):
			c.JSON(403, gin.H{"error": "sin derecho de acceso"})
		case errors.Is(err, courses.ErrNotFound):
			c.JSON(404, gin.H{"error": "recurso no encontrado"})
		default:
			c.JSON(500, gin.H{"error": err.Error()})
		}
		return
	}
	c.JSON(200, p)
}

// Summary retorna avance, estado e insignia del curso para el estudiante.
//
//	@Summary	Avance del curso
//	@Description	Calcula % sobre recursos obligatorios; al aprobar emite la insignia (idempotente).
//	@Tags			Progreso
//	@Produce		json
//	@Param			Authorization	header		string	true	"Bearer <token>"
//	@Param			course_id		path		int		true	"ID del curso"
//	@Security		BearerAuth
//	@Success		200	{object}	CourseProgressResponse	"Avance"
//	@Failure		403	{object}	utils.ErrorResponse	"Sin inscripción"
//	@Failure		404	{object}	utils.ErrorResponse	"No encontrado"
//	@Router			/api/v1/progress/{course_id} [get]
func (h *Handler) Summary(c *gin.Context) {
	courseID, _ := strconv.ParseUint(c.Param("course_id"), 10, 32)
	uid, _ := auth.LookupUser(h.DB, c.GetString("username"))
	s, err := CourseProgress(h.DB, uid, uint(courseID))
	if err != nil {
		if errors.Is(err, courses.ErrForbidden) {
			c.JSON(403, gin.H{"error": "sin derecho de acceso"})
			return
		}
		if errors.Is(err, courses.ErrNotFound) {
			c.JSON(404, gin.H{"error": "no encontrado"})
			return
		}
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, s)
}
