package admin

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/DiegoMunevar2007/Proyecto-1-Cloud.git/auth"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

func actorFromContext(c *gin.Context) Actor {
	return Actor{
		Username:  c.GetString("username"),
		IP:        c.ClientIP(),
		UserAgent: c.Request.UserAgent(),
	}
}

func SetupAdminRoutes(router *gin.Engine, db *gorm.DB, rdb *redis.Client) {
	admin := router.Group("/admin", auth.RequireRole(rdb, auth.RoleAdmin))
	{
		// Gestión de usuarios
		admin.GET("/users", func(c *gin.Context) {
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

			users, total, err := ListUsers(db, f)
			if err != nil {
				c.JSON(500, gin.H{"error": "No se pudo listar usuarios: " + err.Error()})
				return
			}
			c.JSON(200, gin.H{"users": users, "total": total, "page": f.Page, "limit": f.Limit})
		})

		admin.GET("/users/:id", func(c *gin.Context) {
			id, err := strconv.ParseUint(c.Param("id"), 10, 32)
			if err != nil {
				c.JSON(400, gin.H{"error": "ID inválido"})
				return
			}
			user, err := GetUserByID(db, uint(id))
			if err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					c.JSON(404, gin.H{"error": "Usuario no encontrado"})
					return
				}
				c.JSON(500, gin.H{"error": err.Error()})
				return
			}
			c.JSON(200, gin.H{"user": user})
		})

		admin.PUT("/users/:id/role", func(c *gin.Context) {
			id, err := strconv.ParseUint(c.Param("id"), 10, 32)
			if err != nil {
				c.JSON(400, gin.H{"error": "ID inválido"})
				return
			}
			var req struct {
				Role string `json:"role" form:"role" binding:"required"`
			}
			if err := c.ShouldBind(&req); err != nil {
				c.JSON(400, gin.H{"error": "Se requiere campo 'role'"})
				return
			}
			user, err := UpdateUserRole(db, actorFromContext(c), uint(id), req.Role)
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
			c.JSON(200, gin.H{"message": "Rol actualizado correctamente", "user": user})
		})

		admin.PATCH("/users/:id/status", func(c *gin.Context) {
			id, err := strconv.ParseUint(c.Param("id"), 10, 32)
			if err != nil {
				c.JSON(400, gin.H{"error": "ID inválido"})
				return
			}
			var req struct {
				Status string `json:"status" form:"status" binding:"required"`
			}
			if err := c.ShouldBind(&req); err != nil {
				c.JSON(400, gin.H{"error": "Se requiere campo 'status'"})
				return
			}
			user, err := UpdateUserStatus(db, actorFromContext(c), rdb, uint(id), req.Status)
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
			c.JSON(200, gin.H{"message": "Estado actualizado correctamente", "user": user})
		})

		admin.DELETE("/users/:id", func(c *gin.Context) {
			id, err := strconv.ParseUint(c.Param("id"), 10, 32)
			if err != nil {
				c.JSON(400, gin.H{"error": "ID inválido"})
				return
			}
			err = DeleteUser(db, actorFromContext(c), rdb, uint(id))
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
		})

		admin.POST("/users/:id/restore", func(c *gin.Context) {
			id, err := strconv.ParseUint(c.Param("id"), 10, 32)
			if err != nil {
				c.JSON(400, gin.H{"error": "ID inválido"})
				return
			}
			user, err := RestoreUser(db, actorFromContext(c), uint(id))
			if err != nil {
				c.JSON(500, gin.H{"error": err.Error()})
				return
			}
			c.JSON(200, gin.H{"message": "Usuario restaurado", "user": user})
		})

		// Sesiones multi-sesión (ZSet)
		admin.GET("/users/:id/sessions", func(c *gin.Context) {
			id, err := strconv.ParseUint(c.Param("id"), 10, 32)
			if err != nil {
				c.JSON(400, gin.H{"error": "ID inválido"})
				return
			}
			user, err := GetUserByID(db, uint(id))
			if err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					c.JSON(404, gin.H{"error": "Usuario no encontrado"})
					return
				}
				c.JSON(500, gin.H{"error": err.Error()})
				return
			}
			sessions, err := auth.ListSessionsForUser(user.Username, rdb)
			if err != nil {
				c.JSON(500, gin.H{"error": err.Error()})
				return
			}
			c.JSON(200, gin.H{"username": user.Username, "sessions": sessions, "count": len(sessions)})
		})

		admin.DELETE("/users/:id/sessions", func(c *gin.Context) {
			id, err := strconv.ParseUint(c.Param("id"), 10, 32)
			if err != nil {
				c.JSON(400, gin.H{"error": "ID inválido"})
				return
			}
			count, err := RevokeUserSessions(db, actorFromContext(c), rdb, uint(id))
			if err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					c.JSON(404, gin.H{"error": "Usuario no encontrado"})
					return
				}
				c.JSON(500, gin.H{"error": err.Error()})
				return
			}
			c.JSON(200, gin.H{"message": "Sesiones revocadas", "count": count})
		})

		admin.DELETE("/sessions", func(c *gin.Context) {
			var req struct {
				Token string `json:"token" form:"token"`
			}
			_ = c.ShouldBind(&req)
			token := strings.TrimSpace(req.Token)
			if token == "" {
				token = c.Query("token")
			}
			if token == "" {
				c.JSON(400, gin.H{"error": "Se requiere el token a revocar"})
				return
			}
			if err := RevokeOneSession(db, actorFromContext(c), rdb, token); err != nil {
				c.JSON(500, gin.H{"error": err.Error()})
				return
			}
			c.JSON(200, gin.H{"message": "Sesión revocada correctamente"})
		})

		// Auditoría
		admin.GET("/audit", func(c *gin.Context) {
			var f AuditFilter
			f.Actor = c.Query("actor")
			f.Target = c.Query("target")
			f.Action = c.Query("action")
			f.From = c.Query("from")
			f.To = c.Query("to")
			f.Page, _ = strconv.Atoi(c.DefaultQuery("page", "1"))
			f.Limit, _ = strconv.Atoi(c.DefaultQuery("limit", "20"))
			logs, total, err := ListAuditLogs(db, f)
			if err != nil {
				c.JSON(500, gin.H{"error": err.Error()})
				return
			}
			c.JSON(200, gin.H{"logs": logs, "total": total, "page": f.Page, "limit": f.Limit})
		})

		admin.GET("/stats", func(c *gin.Context) {
			var total, active, inactive, blocked, admins int64
			db.Model(&auth.UserModel{}).Count(&total)
			db.Model(&auth.UserModel{}).Where("status = ?", auth.StatusActive).Count(&active)
			db.Model(&auth.UserModel{}).Where("status = ?", auth.StatusInactive).Count(&inactive)
			db.Model(&auth.UserModel{}).Where("status = ?", auth.StatusBlocked).Count(&blocked)
			db.Model(&auth.UserModel{}).Where("role = ? AND status = ?", auth.RoleAdmin, auth.StatusActive).Count(&admins)
			c.JSON(http.StatusOK, gin.H{
				"total": total, "active": active, "inactive": inactive, "blocked": blocked,
				"admins_active": admins,
			})
		})
	}
}
