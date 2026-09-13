package badges

import (
	"net/http"
	"strconv"

	"github.com/DiegoMunevar2007/Proyecto-1-Cloud.git/auth"
	"github.com/DiegoMunevar2007/Proyecto-1-Cloud.git/utils"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

// _ conserva el import para las anotaciones swag.
var _ = utils.ErrorResponse{}

// Handler agrupa los endpoints de insignias con sus dependencias.
type Handler struct {
	DB  *gorm.DB
	RDB *redis.Client
}

// SetupBadgesRoutes registra verificación pública y revocación admin.
func SetupBadgesRoutes(router *gin.RouterGroup, db *gorm.DB, rdb *redis.Client) {
	h := &Handler{DB: db, RDB: rdb}
	router.GET("/badges", auth.RequireAuth(rdb), h.Mine)
	router.GET("/badges/:code", h.Verify)
	router.DELETE("/badges/:id", auth.RequireRole(rdb, auth.RoleAdmin), h.Revoke)
}

func toResponse(b *Badge) BadgeResponse {
	return BadgeResponse{
		Code:     b.Code,
		CourseID: b.CourseID,
		ImageURL: b.ImageURL,
		IssuedAt: b.IssuedAt.Format("2006-01-02T15:04:05Z07:00"),
		Valid:    b.RevokedAt == nil,
	}
}

// Mine lista las insignias del estudiante autenticado.
//
//	@Summary	Mis insignias
//	@Description	Retorna las insignias emitidas al estudiante (incluye IDs para gestión).
//	@Tags			Insignias
//	@Produce		json
//	@Param			Authorization	header		string	true	"Bearer <token>"
//	@Security		BearerAuth
//	@Success		200	{object}	MyBadgesResponse	"Insignias"
//	@Router			/api/v1/badges [get]
func (h *Handler) Mine(c *gin.Context) {
	uid, _ := auth.LookupUser(h.DB, c.GetString("username"))
	items, err := Mine(h.DB, uid)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"badges": items, "total": len(items)})
}

// Verify expone la verificación pública: sin auth y sin correo del estudiante.
//
//	@Summary	Verificar insignia
//	@Description	URL pública de verificación; no expone datos del estudiante.
//	@Tags			Insignias
//	@Produce		json
//	@Param			code	path		string	true	"Código de la insignia"
//	@Success		200	{object}	BadgeResponse	"Insignia válida"
//	@Failure		404	{object}	utils.ErrorResponse	"No encontrada o revocada"
//	@Router			/api/v1/badges/{code} [get]
func (h *Handler) Verify(c *gin.Context) {
	b, err := Verify(h.DB, c.Param("code"))
	if err != nil {
		c.JSON(404, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, toResponse(b))
}

// Revoke invalida una insignia (admin).
//
//	@Summary	Revocar insignia
//	@Description	Marca RevokedAt; la URL pública deja de validar.
//	@Tags			Insignias
//	@Produce		json
//	@Param			Authorization	header		string	true	"Bearer <token de admin>"
//	@Param			id				path		int		true	"ID de la insignia"
//	@Security		BearerAuth
//	@Success		200	{object}	utils.MessageResponse	"Insignia revocada"
//	@Failure		404	{object}	utils.ErrorResponse	"No encontrada"
//	@Router			/api/v1/badges/{id} [delete]
func (h *Handler) Revoke(c *gin.Context) {
	id, _ := strconv.ParseUint(c.Param("id"), 10, 32)
	if err := Revoke(h.DB, uint(id)); err != nil {
		c.JSON(404, gin.H{"error": "no encontrada"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "insignia revocada"})
}
