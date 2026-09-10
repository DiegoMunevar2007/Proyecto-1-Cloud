package admin

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/DiegoMunevar2007/Proyecto-1-Cloud.git/auth"
	"github.com/DiegoMunevar2007/Proyecto-1-Cloud.git/utils"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

// _ garantiza que el import de utils se conserve: swag lo usa para resolver
// los tipos utils.ErrorResponse y utils.MessageResponse de las anotaciones.
var _ = utils.ErrorResponse{}

// Handler agrupa los endpoints administrativos con sus dependencias.
type Handler struct {
	DB  *gorm.DB
	RDB *redis.Client
}

// NewHandler crea un Handler administrativo.
func NewHandler(db *gorm.DB, rdb *redis.Client) *Handler {
	return &Handler{DB: db, RDB: rdb}
}

func actorFromContext(c *gin.Context) Actor {
	return Actor{
		Username:  c.GetString("username"),
		IP:        c.ClientIP(),
		UserAgent: c.Request.UserAgent(),
	}
}

// SetupAdminRoutes registra las rutas administrativas (solo rol admin).
func SetupAdminRoutes(router *gin.Engine, db *gorm.DB, rdb *redis.Client) {
	h := NewHandler(db, rdb)
	admin := router.Group("/admin", auth.RequireRole(rdb, auth.RoleAdmin))
	{
		// Gestión de usuarios
		admin.GET("/users", h.ListUsers)
		admin.GET("/users/:id", h.GetUser)
		admin.PUT("/users/:id/role", h.UpdateRole)
		admin.PATCH("/users/:id/status", h.UpdateStatus)
		admin.DELETE("/users/:id", h.DeleteUser)
		admin.POST("/users/:id/restore", h.RestoreUser)

		// Sesiones multi-sesión (ZSet)
		admin.GET("/users/:id/sessions", h.ListSessions)
		admin.DELETE("/users/:id/sessions", h.RevokeSessions)
		admin.DELETE("/sessions", h.RevokeOne)

		// Auditoría
		admin.GET("/audit", h.ListAudit)
		admin.GET("/stats", h.Stats)
	}
}

// ListUsers lista usuarios con filtros, paginación y orden.
//
//	@Summary		Listar usuarios
//	@Description	Lista paginada de usuarios con búsqueda por texto, filtros por rol, estado y verificación.
//	@Tags			Administración
//	@Produce		json
//	@Param			Authorization	header		string	true	"Bearer <token de admin>"
//	@Param			search			query		string	false	"Búsqueda en username y email"
//	@Param			role			query		string	false	"Filtro por rol (student, professor, admin)"
//	@Param			status			query		string	false	"Filtro por estado (active, inactive, blocked)"
//	@Param			verified		query		boolean	false	"Filtro por verificación"
//	@Param			page			query		int		false	"Página (base 1)"	default(1)
//	@Param			limit			query		int		false	"Resultados por página (máx 100)"	default(20)
//	@Param			sort			query		string	false	"Orden: columna y dirección (ej. username asc)"
//	@Security		BearerAuth
//	@Success		200	{object}	UserListResponse	"Listado de usuarios"
//	@Failure		401	{object}	utils.ErrorResponse	"No autenticado"
//	@Failure		403	{object}	utils.ErrorResponse	"Se requiere rol admin"
//	@Failure		500	{object}	utils.ErrorResponse	"Error interno"
//	@Router			/admin/users [get]
func (h *Handler) ListUsers(c *gin.Context) {
	var f UserFilter
	f.Search = c.Query("search")
	f.Role = c.Query("role")
	f.Status = c.Query("status")
	if v := c.Query("verified"); v != "" {
		b := v == "true" || v == "1"
		f.Verified = &b
	}
	f.Page, _ = strconv.Atoi(c.DefaultQuery("page", "1"))
	f.Limit, _ = strconv.Atoi(c.DefaultQuery("limit", "20"))
	f.Sort = c.Query("sort")

	users, total, err := ListUsers(h.DB, f)
	if err != nil {
		c.JSON(500, gin.H{"error": "No se pudo listar usuarios: " + err.Error()})
		return
	}
	c.JSON(200, gin.H{"users": ToUserResponses(users), "total": total, "page": f.Page, "limit": f.Limit})
}

// GetUser obtiene un usuario por su ID.
//
//	@Summary		Obtener usuario
//	@Description	Retorna la representación pública de un usuario (sin contraseña).
//	@Tags			Administración
//	@Produce		json
//	@Param			Authorization	header		string	true	"Bearer <token de admin>"
//	@Param			id				path		int		true	"ID del usuario"
//	@Security		BearerAuth
//	@Success		200	{object}	UserDetailResponse	"Usuario encontrado"
//	@Failure		400	{object}	utils.ErrorResponse	"ID inválido"
//	@Failure		401	{object}	utils.ErrorResponse	"No autenticado"
//	@Failure		403	{object}	utils.ErrorResponse	"Se requiere rol admin"
//	@Failure		404	{object}	utils.ErrorResponse	"Usuario no encontrado"
//	@Router			/admin/users/{id} [get]
func (h *Handler) GetUser(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(400, gin.H{"error": "ID inválido"})
		return
	}
	user, err := GetUserByID(h.DB, uint(id))
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(404, gin.H{"error": "Usuario no encontrado"})
			return
		}
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"user": auth.ToUserResponse(*user)})
}

// UpdateRole cambia el rol de un usuario, protegiendo al último admin activo.
//
//	@Summary		Cambiar rol
//	@Description	Actualiza el rol de un usuario y registra la acción en auditoría. No permite degradar al último administrador activo.
//	@Tags			Administración
//	@Produce		json
//	@Param			Authorization	header		string				true	"Bearer <token de admin>"
//	@Param			id				path		int					true	"ID del usuario"
//	@Param			request			body		RoleUpdateRequest	true	"Nuevo rol"
//	@Security		BearerAuth
//	@Success		200	{object}	UserMessageResponse	"Rol actualizado"
//	@Failure		400	{object}	utils.ErrorResponse	"ID inválido, rol inválido o falta el campo"
//	@Failure		404	{object}	utils.ErrorResponse	"Usuario no encontrado"
//	@Failure		409	{object}	utils.ErrorResponse	"No se puede dejar el sistema sin administrador"
//	@Router			/admin/users/{id}/role [put]
func (h *Handler) UpdateRole(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(400, gin.H{"error": "ID inválido"})
		return
	}
	var req RoleUpdateRequest
	if err := c.ShouldBind(&req); err != nil {
		c.JSON(400, gin.H{"error": "Se requiere campo 'role'"})
		return
	}
	// Compatibilidad: aceptar también campo vacío como error de validación.
	if strings.TrimSpace(req.Role) == "" {
		c.JSON(400, gin.H{"error": "Se requiere campo 'role'"})
		return
	}
	user, err := UpdateUserRole(h.DB, actorFromContext(c), uint(id), req.Role)
	if err != nil {
		if errors.Is(err, ErrLastAdmin) {
			c.JSON(409, gin.H{"error": err.Error()})
			return
		}
		if strings.Contains(err.Error(), "rol inválido") {
			c.JSON(400, gin.H{"error": err.Error()})
			return
		}
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(404, gin.H{"error": "Usuario no encontrado"})
			return
		}
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"message": "Rol actualizado correctamente", "user": auth.ToUserResponse(*user)})
}

// UpdateStatus cambia el estado de un usuario y revoca sus sesiones si se desactiva.
//
//	@Summary		Cambiar estado
//	@Description	Actualiza el estado (active, inactive, blocked) con auditoría. Desactivar o bloquear revoca todas las sesiones. Protege al último admin activo.
//	@Tags			Administración
//	@Produce		json
//	@Param			Authorization	header		string					true	"Bearer <token de admin>"
//	@Param			id				path		int						true	"ID del usuario"
//	@Param			request			body		StatusUpdateRequest		true	"Nuevo estado"
//	@Security		BearerAuth
//	@Success		200	{object}	UserMessageResponse	"Estado actualizado"
//	@Failure		400	{object}	utils.ErrorResponse	"ID inválido, estado inválido o falta el campo"
//	@Failure		404	{object}	utils.ErrorResponse	"Usuario no encontrado"
//	@Failure		409	{object}	utils.ErrorResponse	"No se puede dejar el sistema sin administrador"
//	@Router			/admin/users/{id}/status [patch]
func (h *Handler) UpdateStatus(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(400, gin.H{"error": "ID inválido"})
		return
	}
	var req StatusUpdateRequest
	if err := c.ShouldBind(&req); err != nil {
		c.JSON(400, gin.H{"error": "Se requiere campo 'status'"})
		return
	}
	if strings.TrimSpace(req.Status) == "" {
		c.JSON(400, gin.H{"error": "Se requiere campo 'status'"})
		return
	}
	user, err := UpdateUserStatus(h.DB, actorFromContext(c), h.RDB, uint(id), req.Status)
	if err != nil {
		if errors.Is(err, ErrLastAdmin) {
			c.JSON(409, gin.H{"error": err.Error()})
			return
		}
		if strings.Contains(err.Error(), "estado inválido") {
			c.JSON(400, gin.H{"error": err.Error()})
			return
		}
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(404, gin.H{"error": "Usuario no encontrado"})
			return
		}
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"message": "Estado actualizado correctamente", "user": auth.ToUserResponse(*user)})
}

// DeleteUser elimina un usuario (soft delete) y revoca sus sesiones.
//
//	@Summary		Eliminar usuario
//	@Description	Soft delete con auditoría y revocación de sesiones. Protege al último administrador activo.
//	@Tags			Administración
//	@Produce		json
//	@Param			Authorization	header		string	true	"Bearer <token de admin>"
//	@Param			id				path		int		true	"ID del usuario"
//	@Security		BearerAuth
//	@Success		200	{object}	utils.MessageResponse	"Usuario eliminado (soft delete)"
//	@Failure		400	{object}	utils.ErrorResponse	"ID inválido"
//	@Failure		404	{object}	utils.ErrorResponse	"Usuario no encontrado"
//	@Failure		409	{object}	utils.ErrorResponse	"No se puede dejar el sistema sin administrador"
//	@Router			/admin/users/{id} [delete]
func (h *Handler) DeleteUser(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(400, gin.H{"error": "ID inválido"})
		return
	}
	err = DeleteUser(h.DB, actorFromContext(c), h.RDB, uint(id))
	if err != nil {
		if errors.Is(err, ErrLastAdmin) {
			c.JSON(409, gin.H{"error": err.Error()})
			return
		}
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(404, gin.H{"error": "Usuario no encontrado"})
			return
		}
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"message": "Usuario eliminado correctamente (soft delete)"})
}

// RestoreUser restaura un usuario eliminado (soft delete).
//
//	@Summary		Restaurar usuario
//	@Description	Revierte el soft delete de un usuario, con registro en auditoría.
//	@Tags			Administración
//	@Produce		json
//	@Param			Authorization	header		string	true	"Bearer <token de admin>"
//	@Param			id				path		int		true	"ID del usuario"
//	@Security		BearerAuth
//	@Success		200	{object}	UserMessageResponse	"Usuario restaurado"
//	@Failure		400	{object}	utils.ErrorResponse	"ID inválido"
//	@Failure		500	{object}	utils.ErrorResponse	"Error interno"
//	@Router			/admin/users/{id}/restore [post]
func (h *Handler) RestoreUser(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(400, gin.H{"error": "ID inválido"})
		return
	}
	user, err := RestoreUser(h.DB, actorFromContext(c), uint(id))
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"message": "Usuario restaurado", "user": auth.ToUserResponse(*user)})
}

// ListSessions lista los tokens activos de un usuario.
//
//	@Summary		Listar sesiones
//	@Description	Retorna los JWT activos de un usuario, purgando expirados perezosamente.
//	@Tags			Administración
//	@Produce		json
//	@Param			Authorization	header		string	true	"Bearer <token de admin>"
//	@Param			id				path		int		true	"ID del usuario"
//	@Security		BearerAuth
//	@Success		200	{object}	SessionsResponse	"Sesiones activas"
//	@Failure		400	{object}	utils.ErrorResponse	"ID inválido"
//	@Failure		404	{object}	utils.ErrorResponse	"Usuario no encontrado"
//	@Router			/admin/users/{id}/sessions [get]
func (h *Handler) ListSessions(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(400, gin.H{"error": "ID inválido"})
		return
	}
	user, err := GetUserByID(h.DB, uint(id))
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(404, gin.H{"error": "Usuario no encontrado"})
			return
		}
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	sessions, err := auth.ListSessionsForUser(user.Username, h.RDB)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"username": user.Username, "sessions": sessions, "count": len(sessions)})
}

// RevokeSessions revoca todas las sesiones de un usuario.
//
//	@Summary		Revocar sesiones
//	@Description	Elimina todos los tokens activos de un usuario, con registro en auditoría.
//	@Tags			Administración
//	@Produce		json
//	@Param			Authorization	header		string	true	"Bearer <token de admin>"
//	@Param			id				path		int		true	"ID del usuario"
//	@Security		BearerAuth
//	@Success		200	{object}	RevokeSessionsResponse	"Sesiones revocadas"
//	@Failure		400	{object}	utils.ErrorResponse	"ID inválido"
//	@Failure		404	{object}	utils.ErrorResponse	"Usuario no encontrado"
//	@Router			/admin/users/{id}/sessions [delete]
func (h *Handler) RevokeSessions(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(400, gin.H{"error": "ID inválido"})
		return
	}
	count, err := RevokeUserSessions(h.DB, actorFromContext(c), h.RDB, uint(id))
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(404, gin.H{"error": "Usuario no encontrado"})
			return
		}
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"message": "Sesiones revocadas", "count": count})
}

// RevokeOne revoca un token de sesión individual.
//
//	@Summary		Revocar una sesión
//	@Description	Revoca el token indicado (cuerpo o query) con registro en auditoría.
//	@Tags			Administración
//	@Produce		json
//	@Param			Authorization	header		string					true	"Bearer <token de admin>"
//	@Param			request			body		SessionTokenRequest		false	"Token a revocar (o query param token)"
//	@Param			token			query		string					false	"Token a revocar (alternativo)"
//	@Security		BearerAuth
//	@Success		200	{object}	utils.MessageResponse	"Sesión revocada"
//	@Failure		400	{object}	utils.ErrorResponse	"Falta el token a revocar"
//	@Failure		500	{object}	utils.ErrorResponse	"Error interno"
//	@Router			/admin/sessions [delete]
func (h *Handler) RevokeOne(c *gin.Context) {
	var req SessionTokenRequest
	_ = c.ShouldBind(&req)
	token := strings.TrimSpace(req.Token)
	if token == "" {
		token = c.Query("token")
	}
	if token == "" {
		c.JSON(400, gin.H{"error": "Se requiere el token a revocar"})
		return
	}
	if err := RevokeOneSession(h.DB, actorFromContext(c), h.RDB, token); err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"message": "Sesión revocada correctamente"})
}

// ListAudit lista los registros de auditoría con filtros y paginación.
//
//	@Summary		Listar auditoría
//	@Description	Registros inmutables de acciones administrativas, filtrables por actor, objetivo, acción y rango de fechas.
//	@Tags			Administración
//	@Produce		json
//	@Param			Authorization	header		string	true	"Bearer <token de admin>"
//	@Param			actor			query		string	false	"Filtro por actor (username)"
//	@Param			target			query		string	false	"Filtro por usuario objetivo"
//	@Param			action			query		string	false	"Filtro por acción"
//	@Param			from			query		string	false	"Desde (fecha)"
//	@Param			to				query		string	false	"Hasta (fecha)"
//	@Param			page			query		int		false	"Página (base 1)"	default(1)
//	@Param			limit			query		int		false	"Resultados por página (máx 100)"	default(20)
//	@Security		BearerAuth
//	@Success		200	{object}	AuditListResponse	"Registros de auditoría"
//	@Failure		500	{object}	utils.ErrorResponse	"Error interno"
//	@Router			/admin/audit [get]
func (h *Handler) ListAudit(c *gin.Context) {
	var f AuditFilter
	f.Actor = c.Query("actor")
	f.Target = c.Query("target")
	f.Action = c.Query("action")
	f.From = c.Query("from")
	f.To = c.Query("to")
	f.Page, _ = strconv.Atoi(c.DefaultQuery("page", "1"))
	f.Limit, _ = strconv.Atoi(c.DefaultQuery("limit", "20"))
	logs, total, err := ListAuditLogs(h.DB, f)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"logs": logs, "total": total, "page": f.Page, "limit": f.Limit})
}

// Stats retorna conteos agregados de usuarios.
//
//	@Summary		Estadísticas
//	@Description	Totales de usuarios por estado y administradores activos.
//	@Tags			Administración
//	@Produce		json
//	@Param			Authorization	header		string	true	"Bearer <token de admin>"
//	@Security		BearerAuth
//	@Success		200	{object}	StatsResponse	"Estadísticas"
//	@Router			/admin/stats [get]
func (h *Handler) Stats(c *gin.Context) {
	var total, active, inactive, blocked, admins int64
	h.DB.Model(&auth.UserModel{}).Count(&total)
	h.DB.Model(&auth.UserModel{}).Where("status = ?", auth.StatusActive).Count(&active)
	h.DB.Model(&auth.UserModel{}).Where("status = ?", auth.StatusInactive).Count(&inactive)
	h.DB.Model(&auth.UserModel{}).Where("status = ?", auth.StatusBlocked).Count(&blocked)
	h.DB.Model(&auth.UserModel{}).Where("role = ? AND status = ?", auth.RoleAdmin, auth.StatusActive).Count(&admins)
	c.JSON(http.StatusOK, gin.H{
		"total": total, "active": active, "inactive": inactive, "blocked": blocked,
		"admins_active": admins,
	})
}
