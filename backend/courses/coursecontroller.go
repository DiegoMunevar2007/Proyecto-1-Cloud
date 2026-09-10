package courses

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/DiegoMunevar2007/Proyecto-1-Cloud.git/auth"
	"github.com/DiegoMunevar2007/Proyecto-1-Cloud.git/queue"
	"github.com/DiegoMunevar2007/Proyecto-1-Cloud.git/storage"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

var (
	queueClient   *queue.Client
	storageClient *storage.Client
)

// SetClients inyecta clientes de cola y almacenamiento (llamado desde main).
func SetClients(q *queue.Client, s *storage.Client) {
	queueClient = q
	storageClient = s
}

func currentUser(db *gorm.DB, c *gin.Context) (uint, string) {
	username := c.GetString("username")
	role := auth.NormalizeRole(c.GetString("role"))
	var u auth.UserModel
	if err := db.Where("username = ?", username).First(&u).Error; err != nil {
		return 0, role
	}
	return u.ID, u.Role
}

func writeErr(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
	case errors.Is(err, ErrForbidden):
		c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
	case errors.Is(err, ErrImmutable), errors.Is(err, ErrPublishedEdit):
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	case errors.Is(err, ErrInvalidPayload):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
	}
}

// SetupCourseRoutes registra las rutas de autoría y catálogo.
func SetupCourseRoutes(router *gin.Engine, db *gorm.DB, rdb *redis.Client) {
	requireAuth := auth.RequireAuth(rdb)
	requireAuthor := auth.RequireRole(rdb, auth.RoleProfessor, auth.RoleAdmin)

	g := router.Group("/courses")
	{
		// Catálogo público: solo publicados, con búsqueda y filtros.
		g.GET("", func(c *gin.Context) {
			search := c.Query("search")
			page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
			limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
			if page <= 0 {
				page = 1
			}
			if limit <= 0 || limit > 100 {
				limit = 20
			}
			q := db.Model(&Course{}).Where("status = ?", CourseStatusPublished)
			if search != "" {
				like := "%" + search + "%"
				q = q.Where("title ILIKE ? OR description ILIKE ?", like, like)
			}
			var total int64
			if err := q.Count(&total).Error; err != nil {
				c.JSON(500, gin.H{"error": err.Error()})
				return
			}
			var items []Course
			if err := q.Order("created_at desc").Offset((page - 1) * limit).Limit(limit).Find(&items).Error; err != nil {
				c.JSON(500, gin.H{"error": err.Error()})
				return
			}
			c.JSON(200, gin.H{"courses": items, "total": total, "page": page, "limit": limit})
		})

		// Detalle + árbol (publicado para estudiantes; borrador para autor/admin).
		g.GET("/:id", func(c *gin.Context) {
			id, err := strconv.ParseUint(c.Param("id"), 10, 32)
			if err != nil {
				c.JSON(400, gin.H{"error": "ID inválido"})
				return
			}
			course, tree, err := GetFullTree(db, uint(id))
			if err != nil {
				writeErr(c, err)
				return
			}
			if course.Status == CourseStatusPublished {
				c.JSON(200, gin.H{"course": course, "version": tree})
				return
			}
			// Borrador: requiere autor propietario o admin.
			token := auth.BearerToken(c)
			if token == "" {
				c.JSON(403, gin.H{"error": "curso no publicado"})
				return
			}
			username, role, err := auth.ResolveSessionTokenWithRole(token, rdb)
			if err != nil {
				c.JSON(401, gin.H{"error": "token inválido"})
				return
			}
			var u auth.UserModel
			db.Where("username = ?", username).First(&u)
			if !IsOwnerOrAdmin(course, u.ID, role) {
				c.JSON(403, gin.H{"error": "curso no publicado"})
				return
			}
			c.JSON(200, gin.H{"course": course, "version": tree})
		})

		// Creación (profesor/admin).
		g.POST("", requireAuthor, func(c *gin.Context) {
			var req struct {
				Title          string `json:"title" binding:"required"`
				Slug           string `json:"slug"`
				Description    string `json:"description"`
				ThumbnailKey   string `json:"thumbnail_key"`
				MinRequiredPct int    `json:"min_required_pct"`
			}
			if err := c.ShouldBindJSON(&req); err != nil {
				c.JSON(400, gin.H{"error": "título requerido"})
				return
			}
			uid, _ := currentUser(db, c)
			course, version, err := CreateCourse(db, uid, req.Title, req.Slug, req.Description, req.ThumbnailKey, req.MinRequiredPct)
			if err != nil {
				writeErr(c, err)
				return
			}
			c.JSON(201, gin.H{"course": course, "version": version})
		})

		g.PUT("/:id", requireAuthor, func(c *gin.Context) {
			id, _ := strconv.ParseUint(c.Param("id"), 10, 32)
			var req struct {
				Title        string `json:"title"`
				Description  string `json:"description"`
				ThumbnailKey string `json:"thumbnail_key"`
			}
			_ = c.ShouldBindJSON(&req)
			uid, role := currentUser(db, c)
			course, err := UpdateCourseMeta(db, uint(id), uid, role, req.Title, req.Description, req.ThumbnailKey)
			if err != nil {
				writeErr(c, err)
				return
			}
			c.JSON(200, gin.H{"course": course})
		})

		// Validación exhaustiva de publicación.
		g.GET("/:id/validate", requireAuthor, func(c *gin.Context) {
			id, _ := strconv.ParseUint(c.Param("id"), 10, 32)
			errs := ValidatePublishable(db, uint(id))
			c.JSON(200, gin.H{"valid": len(errs) == 0, "errors": errs})
		})

		g.POST("/:id/publish", requireAuthor, func(c *gin.Context) {
			id, _ := strconv.ParseUint(c.Param("id"), 10, 32)
			uid, role := currentUser(db, c)
			v, err := PublishVersion(db, uint(id), uid, role)
			if err != nil {
				writeErr(c, err)
				return
			}
			c.JSON(200, gin.H{"message": "versión publicada", "version": v})
		})

		g.POST("/:id/unpublish", requireAuthor, func(c *gin.Context) {
			id, _ := strconv.ParseUint(c.Param("id"), 10, 32)
			uid, role := currentUser(db, c)
			course, err := UnpublishCourse(db, uint(id), uid, role)
			if err != nil {
				writeErr(c, err)
				return
			}
			c.JSON(200, gin.H{"message": "curso despublicado temporalmente", "course": course})
		})

		g.POST("/:id/versions", requireAuthor, func(c *gin.Context) {
			id, _ := strconv.ParseUint(c.Param("id"), 10, 32)
			uid, role := currentUser(db, c)
			v, err := NewDraftVersion(db, uint(id), uid, role)
			if err != nil {
				writeErr(c, err)
				return
			}
			c.JSON(201, gin.H{"version": v})
		})

		// Módulos / unidades / recursos.
		g.POST("/:id/modules", requireAuthor, func(c *gin.Context) {
			id, _ := strconv.ParseUint(c.Param("id"), 10, 32)
			var req struct {
				Title       string `json:"title" binding:"required"`
				Description string `json:"description"`
			}
			if err := c.ShouldBindJSON(&req); err != nil {
				c.JSON(400, gin.H{"error": "título requerido"})
				return
			}
			uid, role := currentUser(db, c)
			m, err := AddModule(db, uint(id), uid, role, req.Title, req.Description)
			if err != nil {
				writeErr(c, err)
				return
			}
			c.JSON(201, gin.H{"module": m})
		})

		g.POST("/:id/modules/reorder", requireAuthor, func(c *gin.Context) {
			id, _ := strconv.ParseUint(c.Param("id"), 10, 32)
			var req struct {
				OrderedIDs []uint `json:"ordered_ids" binding:"required"`
			}
			if err := c.ShouldBindJSON(&req); err != nil {
				c.JSON(400, gin.H{"error": "ordered_ids requerido"})
				return
			}
			uid, role := currentUser(db, c)
			if err := ReorderModules(db, uint(id), uid, role, req.OrderedIDs); err != nil {
				writeErr(c, err)
				return
			}
			c.JSON(200, gin.H{"message": "orden actualizado"})
		})
	}

	router.POST("/modules/:id/units", requireAuthor, func(c *gin.Context) {
		id, _ := strconv.ParseUint(c.Param("id"), 10, 32)
		var req struct {
			Title       string `json:"title" binding:"required"`
			Description string `json:"description"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(400, gin.H{"error": "título requerido"})
			return
		}
		uid, role := currentUser(db, c)
		u, err := AddUnit(db, uint(id), uid, role, req.Title, req.Description)
		if err != nil {
			writeErr(c, err)
			return
		}
		c.JSON(201, gin.H{"unit": u})
	})

	router.POST("/modules/:id/units/reorder", requireAuthor, func(c *gin.Context) {
		id, _ := strconv.ParseUint(c.Param("id"), 10, 32)
		var req struct {
			OrderedIDs []uint `json:"ordered_ids" binding:"required"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(400, gin.H{"error": "ordered_ids requerido"})
			return
		}
		uid, role := currentUser(db, c)
		if err := ReorderUnits(db, uint(id), uid, role, req.OrderedIDs); err != nil {
			writeErr(c, err)
			return
		}
		c.JSON(200, gin.H{"message": "orden actualizado"})
	})

	router.POST("/units/:id/resources", requireAuthor, func(c *gin.Context) {
		id, _ := strconv.ParseUint(c.Param("id"), 10, 32)
		var req ResourceInput
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(400, gin.H{"error": "datos inválidos"})
			return
		}
		// Defaults: visible y obligatorio salvo que se indique lo contrario.
		uid, role := currentUser(db, c)
		r, err := AddResource(db, uint(id), uid, role, req)
		if err != nil {
			writeErr(c, err)
			return
		}
		c.JSON(201, gin.H{"resource": r})
	})

	router.POST("/units/:id/resources/reorder", requireAuthor, func(c *gin.Context) {
		id, _ := strconv.ParseUint(c.Param("id"), 10, 32)
		var req struct {
			OrderedIDs []uint `json:"ordered_ids" binding:"required"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(400, gin.H{"error": "ordered_ids requerido"})
			return
		}
		uid, role := currentUser(db, c)
		if err := ReorderResources(db, uint(id), uid, role, req.OrderedIDs); err != nil {
			writeErr(c, err)
			return
		}
		c.JSON(200, gin.H{"message": "orden actualizado"})
	})

	router.PUT("/resources/:id", requireAuthor, func(c *gin.Context) {
		id, _ := strconv.ParseUint(c.Param("id"), 10, 32)
		var req ResourceInput
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(400, gin.H{"error": "datos inválidos"})
			return
		}
		uid, role := currentUser(db, c)
		r, err := UpdateResource(db, uint(id), uid, role, req)
		if err != nil {
			writeErr(c, err)
			return
		}
		c.JSON(200, gin.H{"resource": r})
	})

	// Carga directa: emite URL prefirmada y encola transcode si es multimedia.
	router.POST("/resources/:id/upload-url", requireAuthor, func(c *gin.Context) {
		id, _ := strconv.ParseUint(c.Param("id"), 10, 32)
		var req struct {
			ObjectKey string `json:"object_key" binding:"required"`
			MimeType  string `json:"mime_type"`
			SizeBytes int64  `json:"size_bytes"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(400, gin.H{"error": "object_key requerido"})
			return
		}
		var r Resource
		if err := db.First(&r, uint(id)).Error; err != nil {
			writeErr(c, ErrNotFound)
			return
		}
		if storageClient == nil || queueClient == nil {
			c.JSON(501, gin.H{"error": "almacenamiento/cola no configurados"})
			return
		}
		url, err := storageClient.PresignedPut(storage.BucketOriginals, req.ObjectKey)
		if err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}
		prevKey := r.ObjectKey
		r.ObjectKey = req.ObjectKey
		if req.MimeType != "" {
			r.MimeType = req.MimeType
		}
		if req.SizeBytes > 0 {
			r.SizeBytes = req.SizeBytes
		}
		taskID := ""
		if IsMediaType(r.Type) {
			// Idempotencia: entrega duplicada del mismo objeto no resetea el
			// estado (el worker confirma sin regenerar salidas); objeto nuevo
			// sí reinicia el procesamiento.
			if prevKey != req.ObjectKey {
				r.HLSKey = ""
				r.ProcessingStatus = ProcessingPending
			} else if r.ProcessingStatus != ProcessingReady || r.HLSKey == "" {
				r.ProcessingStatus = ProcessingPending
			}
			idemKey := c.GetHeader("Idempotency-Key")
			if idemKey == "" {
				idemKey = "transcode-" + r.StableID
			}
			taskID, _ = queueClient.EnqueueTranscode(queue.TranscodePayload{
				ResourceID:     r.ID,
				ObjectKey:      r.ObjectKey,
				MimeType:       r.MimeType,
				IdempotencyKey: idemKey,
			}, idemKey)
		}
		db.Save(&r)
		c.JSON(200, gin.H{"upload_url": url, "task_id": taskID, "resource": r})
	})

	// Descarga/consumo: URL firmada tras verificación (propietario/admin o recurso visible de curso publicado).
	router.GET("/resources/:id/download-url", requireAuth, func(c *gin.Context) {
		id, _ := strconv.ParseUint(c.Param("id"), 10, 32)
		var r Resource
		if err := db.First(&r, uint(id)).Error; err != nil {
			writeErr(c, ErrNotFound)
			return
		}
		var u Unit
		db.First(&u, r.UnitID)
		var m Module
		db.First(&m, u.ModuleID)
		var v CourseVersion
		db.First(&v, m.CourseVersionID)
		var course Course
		db.First(&course, v.CourseID)
		uid, role := currentUser(db, c)
		allowed := IsOwnerOrAdmin(&course, uid, role)
		if !allowed {
			// Estudiante: solo si el curso está publicado y el recurso es visible.
			if course.Status != CourseStatusPublished || !r.IsVisible {
				c.JSON(403, gin.H{"error": "sin derecho de acceso"})
				return
			}
			allowed = true
		}
		if !allowed || storageClient == nil {
			c.JSON(403, gin.H{"error": "sin derecho de acceso"})
			return
		}
		bucket := storage.BucketOriginals
		key := r.ObjectKey
		if r.HLSKey != "" && (r.Type == ResourceTypeVideo || r.Type == ResourceTypeAudio) {
			bucket = storage.BucketHLS
			key = r.HLSKey
			c.JSON(200, gin.H{"url": storageClient.PublicURL(bucket, key), "hls": true})
			return
		}
		url, err := storageClient.PresignedGet(bucket, key, 0)
		if err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}
		c.JSON(200, gin.H{"url": url, "hls": false})
	})
}
