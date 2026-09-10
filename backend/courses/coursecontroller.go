package courses

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/DiegoMunevar2007/Proyecto-1-Cloud.git/auth"
	"github.com/DiegoMunevar2007/Proyecto-1-Cloud.git/queue"
	"github.com/DiegoMunevar2007/Proyecto-1-Cloud.git/storage"
	"github.com/DiegoMunevar2007/Proyecto-1-Cloud.git/utils"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

// _ garantiza que el import de utils se conserve: swag lo usa para resolver
// los tipos utils.ErrorResponse y utils.MessageResponse de las anotaciones.
var _ = utils.ErrorResponse{}

var (
	queueClient   *queue.Client
	storageClient *storage.Client
)

// SetClients inyecta clientes de cola y almacenamiento (llamado desde main).
func SetClients(q *queue.Client, s *storage.Client) {
	queueClient = q
	storageClient = s
}

// Handler agrupa los endpoints de cursos con sus dependencias.
type Handler struct {
	DB  *gorm.DB
	RDB *redis.Client
}

// NewHandler crea un Handler de cursos.
func NewHandler(db *gorm.DB, rdb *redis.Client) *Handler {
	return &Handler{DB: db, RDB: rdb}
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

// SetupCourseRoutes registra las rutas de autoría y catálogo (sin versionamiento de API).
func SetupCourseRoutes(router *gin.Engine, db *gorm.DB, rdb *redis.Client) {
	h := NewHandler(db, rdb)
	requireAuth := auth.RequireAuth(rdb)
	requireAuthor := auth.RequireRole(rdb, auth.RoleProfessor, auth.RoleAdmin)

	g := router.Group("/courses")
	{
		g.GET("", h.ListCatalog)
		g.GET("/:id", h.GetDetail)
		g.POST("", requireAuthor, h.Create)
		g.PUT("/:id", requireAuthor, h.Update)
		g.GET("/:id/validate", requireAuthor, h.Validate)
		g.POST("/:id/publish", requireAuthor, h.Publish)
		g.POST("/:id/unpublish", requireAuthor, h.Unpublish)
		g.POST("/:id/versions", requireAuthor, h.NewVersion)
		g.POST("/:id/modules", requireAuthor, h.AddModule)
		g.POST("/:id/modules/reorder", requireAuthor, h.ReorderModules)
	}

	router.POST("/modules/:id/units", requireAuthor, h.AddUnit)
	router.POST("/modules/:id/units/reorder", requireAuthor, h.ReorderUnits)
	router.POST("/units/:id/resources", requireAuthor, h.AddResource)
	router.POST("/units/:id/resources/reorder", requireAuthor, h.ReorderResources)
	router.PUT("/resources/:id", requireAuthor, h.UpdateResource)
	router.POST("/resources/:id/upload-url", requireAuthor, h.UploadURL)
	router.GET("/resources/:id/download-url", requireAuth, h.DownloadURL)
}

// ListCatalog lista el catálogo público de cursos publicados.
//
//	@Summary		Catálogo de cursos
//	@Description	Catálogo público con búsqueda y filtros. Solo incluye cursos publicados.
//	@Tags			Cursos
//	@Produce		json
//	@Param			search	query		string	false	"Búsqueda en título y descripción"
//	@Param			page	query		int		false	"Página (base 1)"	default(1)
//	@Param			limit	query		int		false	"Resultados por página (máx 100)"	default(20)
//	@Success		200		{object}	CoursesResponse	"Catálogo paginado"
//	@Failure		500		{object}	utils.ErrorResponse	"Error interno"
//	@Router			/courses [get]
func (h *Handler) ListCatalog(c *gin.Context) {
	search := c.Query("search")
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	if page <= 0 {
		page = 1
	}
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	q := h.DB.Model(&Course{}).Where("status = ?", CourseStatusPublished)
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
}

// GetDetail retorna un curso con su árbol de contenido ordenado.
//
//	@Summary		Detalle de curso
//	@Description	Si el curso está publicado es público; si es borrador requiere ser el autor o admin.
//	@Tags			Cursos
//	@Produce		json
//	@Param			id				path		int		true	"ID del curso"
//	@Param			Authorization	header		string	false	"Bearer <token> (obligatorio para borradores)"
//	@Success		200	{object}	CourseDetailResponse	"Curso con su versión y árbol"
//	@Failure		400	{object}	utils.ErrorResponse	"ID inválido"
//	@Failure		401	{object}	utils.ErrorResponse	"Token inválido"
//	@Failure		403	{object}	utils.ErrorResponse	"Curso no publicado"
//	@Failure		404	{object}	utils.ErrorResponse	"No encontrado"
//	@Router			/courses/{id} [get]
func (h *Handler) GetDetail(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(400, gin.H{"error": "ID inválido"})
		return
	}
	course, tree, err := GetFullTree(h.DB, uint(id))
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
	username, role, err := auth.ResolveSessionTokenWithRole(token, h.RDB)
	if err != nil {
		c.JSON(401, gin.H{"error": "token inválido"})
		return
	}
	var u auth.UserModel
	h.DB.Where("username = ?", username).First(&u)
	if !IsOwnerOrAdmin(course, u.ID, role) {
		c.JSON(403, gin.H{"error": "curso no publicado"})
		return
	}
	c.JSON(200, gin.H{"course": course, "version": tree})
}

// Create crea un curso en borrador con su versión 1.
//
//	@Summary		Crear curso
//	@Description	Crea el curso (borrador) con su versión 1 draft. Requiere rol professor o admin.
//	@Tags			Cursos
//	@Produce		json
//	@Param			Authorization	header		string					true	"Bearer <token>"
//	@Param			request			body		CreateCourseRequest		true	"Metadatos del curso"
//	@Security		BearerAuth
//	@Success		201	{object}	CourseCreatedResponse	"Curso creado"
//	@Failure		400	{object}	utils.ErrorResponse	"Título requerido"
//	@Failure		401	{object}	utils.ErrorResponse	"No autenticado"
//	@Failure		403	{object}	utils.ErrorResponse	"Se requiere professor o admin"
//	@Router			/courses [post]
func (h *Handler) Create(c *gin.Context) {
	var req CreateCourseRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "título requerido"})
		return
	}
	uid, _ := currentUser(h.DB, c)
	course, version, err := CreateCourse(h.DB, uid, req.Title, req.Slug, req.Description, req.ThumbnailKey, req.MinRequiredPct)
	if err != nil {
		writeErr(c, err)
		return
	}
	c.JSON(201, gin.H{"course": course, "version": version})
}

// Update edita los metadatos del curso (solo si no está publicado).
//
//	@Summary		Editar curso
//	@Description	Edita título, descripción o miniatura. Un curso publicado debe despublicarse primero (MVP).
//	@Tags			Cursos
//	@Produce		json
//	@Param			Authorization	header		string					true	"Bearer <token>"
//	@Param			id				path		int						true	"ID del curso"
//	@Param			request			body		UpdateCourseRequest		true	"Campos a actualizar"
//	@Security		BearerAuth
//	@Success		200	{object}	CourseCreatedResponse	"Curso actualizado (envuelve course)"
//	@Failure		400	{object}	utils.ErrorResponse	"ID inválido"
//	@Failure		403	{object}	utils.ErrorResponse	"Sin permiso"
//	@Failure		404	{object}	utils.ErrorResponse	"No encontrado"
//	@Failure		409	{object}	utils.ErrorResponse	"Curso publicado (despublicar primero)"
//	@Router			/courses/{id} [put]
func (h *Handler) Update(c *gin.Context) {
	id, _ := strconv.ParseUint(c.Param("id"), 10, 32)
	var req UpdateCourseRequest
	_ = c.ShouldBindJSON(&req)
	uid, role := currentUser(h.DB, c)
	course, err := UpdateCourseMeta(h.DB, uint(id), uid, role, req.Title, req.Description, req.ThumbnailKey)
	if err != nil {
		writeErr(c, err)
		return
	}
	c.JSON(200, gin.H{"course": course})
}

// Validate retorna la lista exhaustiva de errores que impiden publicar.
//
//	@Summary		Validar publicación
//	@Description	Verifica metadatos, estructura mínima, criterios de aprobación y disponibilidad de recursos visibles.
//	@Tags			Cursos
//	@Produce		json
//	@Param			Authorization	header		string	true	"Bearer <token>"
//	@Param			id				path		int		true	"ID del curso"
//	@Security		BearerAuth
//	@Success		200	{object}	ValidateResponse	"Resultado de validación"
//	@Failure		401	{object}	utils.ErrorResponse	"No autenticado"
//	@Failure		403	{object}	utils.ErrorResponse	"Se requiere professor o admin"
//	@Router			/courses/{id}/validate [get]
func (h *Handler) Validate(c *gin.Context) {
	id, _ := strconv.ParseUint(c.Param("id"), 10, 32)
	errs := ValidatePublishable(h.DB, uint(id))
	c.JSON(200, gin.H{"valid": len(errs) == 0, "errors": errs})
}

// Publish publica el borrador como versión inmutable.
//
//	@Summary		Publicar curso
//	@Description	Valida y publica el borrador: la versión se vuelve inmutable y pasa a ser la vigente.
//	@Tags			Cursos
//	@Produce		json
//	@Param			Authorization	header		string	true	"Bearer <token>"
//	@Param			id				path		int		true	"ID del curso"
//	@Security		BearerAuth
//	@Success		200	{object}	PublishResponse	"Versión publicada"
//	@Failure		400	{object}	utils.ErrorResponse	"Validación fallida (ver errores)"
//	@Failure		403	{object}	utils.ErrorResponse	"Sin permiso"
//	@Failure		404	{object}	utils.ErrorResponse	"No encontrado"
//	@Router			/courses/{id}/publish [post]
func (h *Handler) Publish(c *gin.Context) {
	id, _ := strconv.ParseUint(c.Param("id"), 10, 32)
	uid, role := currentUser(h.DB, c)
	v, err := PublishVersion(h.DB, uint(id), uid, role)
	if err != nil {
		writeErr(c, err)
		return
	}
	c.JSON(200, gin.H{"message": "versión publicada", "version": v})
}

// Unpublish despublica temporalmente el curso para permitir edición (MVP).
//
//	@Summary		Despublicar curso
//	@Description	La edición de un curso publicado exige despublicarlo temporalmente durante el MVP.
//	@Tags			Cursos
//	@Produce		json
//	@Param			Authorization	header		string	true	"Bearer <token>"
//	@Param			id				path		int		true	"ID del curso"
//	@Security		BearerAuth
//	@Success		200	{object}	UnpublishResponse	"Curso despublicado"
//	@Failure		403	{object}	utils.ErrorResponse	"Sin permiso"
//	@Failure		404	{object}	utils.ErrorResponse	"No encontrado"
//	@Router			/courses/{id}/unpublish [post]
func (h *Handler) Unpublish(c *gin.Context) {
	id, _ := strconv.ParseUint(c.Param("id"), 10, 32)
	uid, role := currentUser(h.DB, c)
	course, err := UnpublishCourse(h.DB, uint(id), uid, role)
	if err != nil {
		writeErr(c, err)
		return
	}
	c.JSON(200, gin.H{"message": "curso despublicado temporalmente", "course": course})
}

// NewVersion crea un borrador de actualización clonando la versión vigente.
//
//	@Summary		Crear borrador de actualización
//	@Description	Clona módulos, unidades y recursos preservando los stable_id para conservar el progreso.
//	@Tags			Cursos
//	@Produce		json
//	@Param			Authorization	header		string	true	"Bearer <token>"
//	@Param			id				path		int		true	"ID del curso"
//	@Security		BearerAuth
//	@Success		201	{object}	VersionEnvelope	"Borrador creado"
//	@Failure		400	{object}	utils.ErrorResponse	"Ya existe un borrador"
//	@Failure		403	{object}	utils.ErrorResponse	"Sin permiso"
//	@Failure		404	{object}	utils.ErrorResponse	"No encontrado"
//	@Router			/courses/{id}/versions [post]
func (h *Handler) NewVersion(c *gin.Context) {
	id, _ := strconv.ParseUint(c.Param("id"), 10, 32)
	uid, role := currentUser(h.DB, c)
	v, err := NewDraftVersion(h.DB, uint(id), uid, role)
	if err != nil {
		writeErr(c, err)
		return
	}
	c.JSON(201, gin.H{"version": v})
}

// AddModule agrega un módulo al borrador editable.
//
//	@Summary		Agregar módulo
//	@Description	Agrega un módulo al final del borrador editable del curso.
//	@Tags			Cursos
//	@Produce		json
//	@Param			Authorization	header		string					true	"Bearer <token>"
//	@Param			id				path		int						true	"ID del curso"
//	@Param			request			body		CreateModuleRequest		true	"Datos del módulo"
//	@Security		BearerAuth
//	@Success		201	{object}	ModuleEnvelope	"Módulo creado"
//	@Failure		400	{object}	utils.ErrorResponse	"Título requerido"
//	@Failure		403	{object}	utils.ErrorResponse	"Sin permiso"
//	@Failure		404	{object}	utils.ErrorResponse	"No encontrado"
//	@Failure		409	{object}	utils.ErrorResponse	"Curso publicado o versión inmutable"
//	@Router			/courses/{id}/modules [post]
func (h *Handler) AddModule(c *gin.Context) {
	id, _ := strconv.ParseUint(c.Param("id"), 10, 32)
	var req CreateModuleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "título requerido"})
		return
	}
	uid, role := currentUser(h.DB, c)
	m, err := AddModule(h.DB, uint(id), uid, role, req.Title, req.Description)
	if err != nil {
		writeErr(c, err)
		return
	}
	c.JSON(201, gin.H{"module": m})
}

// ReorderModules reordena los módulos de la versión borrador.
//
//	@Summary		Reordenar módulos
//	@Description	Asigna posiciones 1-based según el orden de IDs recibido, en transacción.
//	@Tags			Cursos
//	@Produce		json
//	@Param			Authorization	header		string				true	"Bearer <token>"
//	@Param			id				path		int					true	"ID del curso"
//	@Param			request			body		OrderedIDsRequest	true	"IDs en el orden deseado"
//	@Security		BearerAuth
//	@Success		200	{object}	utils.MessageResponse	"Orden actualizado"
//	@Failure		400	{object}	utils.ErrorResponse	"ordered_ids requerido o módulo inexistente"
//	@Failure		403	{object}	utils.ErrorResponse	"Sin permiso"
//	@Failure		404	{object}	utils.ErrorResponse	"No encontrado"
//	@Router			/courses/{id}/modules/reorder [post]
func (h *Handler) ReorderModules(c *gin.Context) {
	id, _ := strconv.ParseUint(c.Param("id"), 10, 32)
	var req OrderedIDsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "ordered_ids requerido"})
		return
	}
	uid, role := currentUser(h.DB, c)
	if err := ReorderModules(h.DB, uint(id), uid, role, req.OrderedIDs); err != nil {
		writeErr(c, err)
		return
	}
	c.JSON(200, gin.H{"message": "orden actualizado"})
}

// AddUnit agrega una unidad a un módulo del borrador.
//
//	@Summary		Agregar unidad
//	@Description	Agrega una unidad al final del módulo indicado.
//	@Tags			Cursos
//	@Produce		json
//	@Param			Authorization	header		string				true	"Bearer <token>"
//	@Param			id				path		int					true	"ID del módulo"
//	@Param			request			body		CreateUnitRequest	true	"Datos de la unidad"
//	@Security		BearerAuth
//	@Success		201	{object}	UnitEnvelope	"Unidad creada"
//	@Failure		400	{object}	utils.ErrorResponse	"Título requerido"
//	@Failure		403	{object}	utils.ErrorResponse	"Sin permiso"
//	@Failure		404	{object}	utils.ErrorResponse	"No encontrado"
//	@Failure		409	{object}	utils.ErrorResponse	"Versión inmutable"
//	@Router			/modules/{id}/units [post]
func (h *Handler) AddUnit(c *gin.Context) {
	id, _ := strconv.ParseUint(c.Param("id"), 10, 32)
	var req CreateUnitRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "título requerido"})
		return
	}
	uid, role := currentUser(h.DB, c)
	u, err := AddUnit(h.DB, uint(id), uid, role, req.Title, req.Description)
	if err != nil {
		writeErr(c, err)
		return
	}
	c.JSON(201, gin.H{"unit": u})
}

// ReorderUnits reordena las unidades dentro de un módulo.
//
//	@Summary		Reordenar unidades
//	@Description	Asigna posiciones 1-based según el orden de IDs recibido, en transacción.
//	@Tags			Cursos
//	@Produce		json
//	@Param			Authorization	header		string				true	"Bearer <token>"
//	@Param			id				path		int					true	"ID del módulo"
//	@Param			request			body		OrderedIDsRequest	true	"IDs en el orden deseado"
//	@Security		BearerAuth
//	@Success		200	{object}	utils.MessageResponse	"Orden actualizado"
//	@Failure		400	{object}	utils.ErrorResponse	"ordered_ids requerido o unidad inexistente"
//	@Failure		403	{object}	utils.ErrorResponse	"Sin permiso"
//	@Failure		404	{object}	utils.ErrorResponse	"No encontrado"
//	@Router			/modules/{id}/units/reorder [post]
func (h *Handler) ReorderUnits(c *gin.Context) {
	id, _ := strconv.ParseUint(c.Param("id"), 10, 32)
	var req OrderedIDsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "ordered_ids requerido"})
		return
	}
	uid, role := currentUser(h.DB, c)
	if err := ReorderUnits(h.DB, uint(id), uid, role, req.OrderedIDs); err != nil {
		writeErr(c, err)
		return
	}
	c.JSON(200, gin.H{"message": "orden actualizado"})
}

// AddResource agrega un recurso a una unidad del borrador.
//
//	@Summary		Agregar recurso
//	@Description	Tipos: text, image, video, audio, pdf, slides, file, iframe, link, quiz. Video/audio quedan en processing pending hasta el transcode.
//	@Tags			Cursos
//	@Produce		json
//	@Param			Authorization	header		string			true	"Bearer <token>"
//	@Param			id				path		int				true	"ID de la unidad"
//	@Param			request			body		ResourceInput	true	"Datos del recurso"
//	@Security		BearerAuth
//	@Success		201	{object}	ResourceEnvelope	"Recurso creado"
//	@Failure		400	{object}	utils.ErrorResponse	"Tipo o datos inválidos"
//	@Failure		403	{object}	utils.ErrorResponse	"Sin permiso"
//	@Failure		404	{object}	utils.ErrorResponse	"No encontrado"
//	@Failure		409	{object}	utils.ErrorResponse	"Versión inmutable"
//	@Router			/units/{id}/resources [post]
func (h *Handler) AddResource(c *gin.Context) {
	id, _ := strconv.ParseUint(c.Param("id"), 10, 32)
	var req ResourceInput
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "datos inválidos"})
		return
	}
	uid, role := currentUser(h.DB, c)
	r, err := AddResource(h.DB, uint(id), uid, role, req)
	if err != nil {
		writeErr(c, err)
		return
	}
	c.JSON(201, gin.H{"resource": r})
}

// ReorderResources reordena los recursos dentro de una unidad.
//
//	@Summary		Reordenar recursos
//	@Description	Asigna posiciones 1-based según el orden de IDs recibido, en transacción.
//	@Tags			Cursos
//	@Produce		json
//	@Param			Authorization	header		string				true	"Bearer <token>"
//	@Param			id				path		int					true	"ID de la unidad"
//	@Param			request			body		OrderedIDsRequest	true	"IDs en el orden deseado"
//	@Security		BearerAuth
//	@Success		200	{object}	utils.MessageResponse	"Orden actualizado"
//	@Failure		400	{object}	utils.ErrorResponse	"ordered_ids requerido o recurso inexistente"
//	@Failure		403	{object}	utils.ErrorResponse	"Sin permiso"
//	@Failure		404	{object}	utils.ErrorResponse	"No encontrado"
//	@Router			/units/{id}/resources/reorder [post]
func (h *Handler) ReorderResources(c *gin.Context) {
	id, _ := strconv.ParseUint(c.Param("id"), 10, 32)
	var req OrderedIDsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "ordered_ids requerido"})
		return
	}
	uid, role := currentUser(h.DB, c)
	if err := ReorderResources(h.DB, uint(id), uid, role, req.OrderedIDs); err != nil {
		writeErr(c, err)
		return
	}
	c.JSON(200, gin.H{"message": "orden actualizado"})
}

// UpdateResource edita título, visibilidad y contenido de un recurso del borrador.
//
//	@Summary		Editar recurso
//	@Description	Actualiza los campos indicados; los no enviados conservan su valor. Solo en versión borrador.
//	@Tags			Cursos
//	@Produce		json
//	@Param			Authorization	header		string			true	"Bearer <token>"
//	@Param			id				path		int				true	"ID del recurso"
//	@Param			request			body		ResourceInput	true	"Campos a actualizar"
//	@Security		BearerAuth
//	@Success		200	{object}	ResourceEnvelope	"Recurso actualizado"
//	@Failure		400	{object}	utils.ErrorResponse	"Datos inválidos"
//	@Failure		403	{object}	utils.ErrorResponse	"Sin permiso"
//	@Failure		404	{object}	utils.ErrorResponse	"No encontrado"
//	@Failure		409	{object}	utils.ErrorResponse	"Versión inmutable"
//	@Router			/resources/{id} [put]
func (h *Handler) UpdateResource(c *gin.Context) {
	id, _ := strconv.ParseUint(c.Param("id"), 10, 32)
	var req ResourceInput
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "datos inválidos"})
		return
	}
	uid, role := currentUser(h.DB, c)
	r, err := UpdateResource(h.DB, uint(id), uid, role, req)
	if err != nil {
		writeErr(c, err)
		return
	}
	c.JSON(200, gin.H{"resource": r})
}

// UploadURL emite una URL prefirmada de subida directa y encola el transcode si aplica.
//
//	@Summary		URL de subida
//	@Description	Genera una URL prefirmada PUT válida 24h para carga directa a objetos. En video/audio encola transcodificación HLS idempotente (header Idempotency-Key recomendado).
//	@Tags			Cursos
//	@Produce		json
//	@Param			Authorization		header		string				true	"Bearer <token>"
//	@Param			Idempotency-Key		header		string				false	"Clave de idempotencia (se genera una por defecto)"
//	@Param			id					path		int					true	"ID del recurso"
//	@Param			request				body		UploadURLRequest	true	"Objeto destino y metadatos"
//	@Security		BearerAuth
//	@Success		200	{object}	UploadURLResponse	"URL prefirmada y tarea creada"
//	@Failure		400	{object}	utils.ErrorResponse	"object_key requerido"
//	@Failure		404	{object}	utils.ErrorResponse	"Recurso no encontrado"
//	@Failure		501	{object}	utils.ErrorResponse	"Almacenamiento/cola no configurados"
//	@Router			/resources/{id}/upload-url [post]
func (h *Handler) UploadURL(c *gin.Context) {
	id, _ := strconv.ParseUint(c.Param("id"), 10, 32)
	var req UploadURLRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "object_key requerido"})
		return
	}
	var r Resource
	if err := h.DB.First(&r, uint(id)).Error; err != nil {
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
	h.DB.Save(&r)
	c.JSON(200, gin.H{"upload_url": url, "task_id": taskID, "resource": r})
}

// DownloadURL entrega la URL de consumo de un recurso tras verificar acceso.
//
//	@Summary		URL de descarga
//	@Description	Verifica propiedad o inscripción y curso publicado con recurso visible. Si hay HLS listo retorna su URL pública; si no, una URL firmada de 15 min.
//	@Tags			Cursos
//	@Produce		json
//	@Param			Authorization	header		string	true	"Bearer <token>"
//	@Param			id				path		int		true	"ID del recurso"
//	@Security		BearerAuth
//	@Success		200	{object}	DownloadURLResponse	"URL de consumo"
//	@Failure		403	{object}	utils.ErrorResponse	"Sin derecho de acceso"
//	@Failure		404	{object}	utils.ErrorResponse	"Recurso no encontrado"
//	@Router			/resources/{id}/download-url [get]
func (h *Handler) DownloadURL(c *gin.Context) {
	id, _ := strconv.ParseUint(c.Param("id"), 10, 32)
	var r Resource
	if err := h.DB.First(&r, uint(id)).Error; err != nil {
		writeErr(c, ErrNotFound)
		return
	}
	var u Unit
	h.DB.First(&u, r.UnitID)
	var m Module
	h.DB.First(&m, u.ModuleID)
	var v CourseVersion
	h.DB.First(&v, m.CourseVersionID)
	var course Course
	h.DB.First(&course, v.CourseID)
	uid, role := currentUser(h.DB, c)
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
}
