package uploads

import (
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/DiegoMunevar2007/Proyecto-1-Cloud.git/auth"
	"github.com/DiegoMunevar2007/Proyecto-1-Cloud.git/courses"
	"github.com/DiegoMunevar2007/Proyecto-1-Cloud.git/queue"
	"github.com/DiegoMunevar2007/Proyecto-1-Cloud.git/storage"
	"github.com/DiegoMunevar2007/Proyecto-1-Cloud.git/utils"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/tus/tusd/v2/pkg/handler"
	"github.com/tus/tusd/v2/pkg/s3store"
	"gorm.io/gorm"
)

const (
	// MaxUploadSize acota un objeto a 2GB.
	MaxUploadSize = 2 << 30
	// basePath debe coincidir con el grupo de rutas (Location header).
	basePath = "/api/v1/uploads/"
)

type tusHandler struct {
	DB    *gorm.DB
	RDB   *redis.Client
	Queue *queue.Client
}

// SetupRoutes monta el protocolo TUS tras RequireAuth. El estado vive en S3 + Redis: el API sigue stateless.
func SetupRoutes(router *gin.RouterGroup, db *gorm.DB, rdb *redis.Client, q *queue.Client) error {
	store := s3store.New(storage.BucketOriginals, newS3Service())
	// Sidecars de tusd (.info/.part) bajo prefijo propio: el bucket expira
	// ese prefijo por lifecycle y los objetos finales quedan limpios.
	// No se borran al finalizar: rompería HEAD/GET post-finish del protocolo.
	store.MetadataObjectPrefix = storage.TusMetaPrefix

	composer := handler.NewStoreComposer()
	store.UseIn(composer)
	composer.UseLocker(&RedisLocker{RDB: rdb})

	h := &tusHandler{DB: db, RDB: rdb, Queue: q}
	uh, err := handler.NewUnroutedHandler(handler.Config{
		StoreComposer:           composer,
		BasePath:                basePath,
		MaxSize:                 MaxUploadSize,
		NotifyCompleteUploads:   true,
		DisableDownload:         true,
		DisableConcatenation:    true,
		PreUploadCreateCallback: h.preCreate,
	})
	if err != nil {
		return err
	}
	go h.consume(uh)

	g := router.Group("/uploads")
	g.Use(auth.RequireRole(rdb))
	// tusd extrae el ID del path completo: se reescribe a raíz + id.
	g.POST("", rewrite(uh.PostFile, "/"))
	g.HEAD("/:id", rewriteID(uh.HeadFile))
	g.PATCH("/:id", rewriteID(uh.PatchFile))
	g.DELETE("/:id", rewriteID(uh.DelFile))
	return nil
}

// rewrite fija el path visto por tusd (raíz para creación).
func rewrite(h func(http.ResponseWriter, *http.Request), path string) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Request.URL.Path = path
		h(c.Writer, c.Request)
	}
}

// rewriteID fija el path visto por tusd (/ + id del upload).
func rewriteID(h func(http.ResponseWriter, *http.Request)) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Request.URL.Path = "/" + c.Param("id")
		h(c.Writer, c.Request)
	}
}

// newS3Service construye el cliente S3 (AWS SDK) contra el endpoint S3-compatible.
func newS3Service() *s3.Client {
	scheme := "http://"
	if utils.GetEnv("S3_USE_SSL", "false") == "true" {
		scheme = "https://"
	}
	return s3.New(s3.Options{
		Region:       utils.GetEnv("S3_REGION", "us-east-1"),
		Credentials:  credentials.NewStaticCredentialsProvider(utils.GetEnv("S3_ACCESS_KEY", "minioadmin"), utils.GetEnv("S3_SECRET_KEY", "minioadmin"), ""),
		BaseEndpoint: aws.String(scheme + utils.GetEnv("S3_ENDPOINT", "localhost:9000")),
		UsePathStyle: true,
	})
}

// preCreate valida autoría sobre el recurso (metadata resource_id) y anota
// mime/tamaño. Sin restricciones de borrador: igual que upload-url, subir
// bytes no es edición estructural (el gate de publicación sigue mandando).
func (h *tusHandler) preCreate(event handler.HookEvent) (handler.HTTPResponse, handler.FileInfoChanges, error) {
	deny := func(code int, msg string) (handler.HTTPResponse, handler.FileInfoChanges, error) {
		return handler.HTTPResponse{}, handler.FileInfoChanges{}, handler.NewError("ERR_UPLOAD_REJECTED", msg, code)
	}
	token := strings.TrimSpace(strings.TrimPrefix(event.HTTPRequest.Header.Get("Authorization"), "Bearer "))
	username, _, err := auth.ResolveSessionTokenWithRole(token, h.RDB)
	if err != nil || username == "" {
		return deny(401, "sesión inválida")
	}
	id, err := strconv.ParseUint(event.Upload.MetaData["resource_id"], 10, 32)
	if err != nil || id == 0 {
		return deny(400, "metadata resource_id requerido")
	}
	r, c, _, err := courses.LocateResource(h.DB, uint(id))
	if err != nil {
		return deny(404, "recurso no encontrado")
	}
	uid, role := auth.LookupUser(h.DB, username)
	if !courses.IsOwnerOrAdmin(c, uid, role) {
		return deny(403, "sin permiso")
	}
	if mt := event.Upload.MetaData["filetype"]; mt != "" {
		r.MimeType = mt
	}
	r.SizeBytes = event.Upload.Size
	if err := h.DB.Save(r).Error; err != nil {
		return deny(500, "no se pudo preparar el recurso")
	}
	return handler.HTTPResponse{}, handler.FileInfoChanges{}, nil
}

// consume encadena el scan al completarse cada upload.
func (h *tusHandler) consume(uh *handler.UnroutedHandler) {
	for event := range uh.CompleteUploads {
		h.finish(event)
	}
}

// s3ObjectKey extrae la clave final del objeto del ID compuesto de
// tusd-S3 (`<uuid>+<multipartId>`): el objeto vive bajo el uuid.
func s3ObjectKey(uploadID string) string {
	before, _, _ := strings.Cut(uploadID, "+")
	return before
}

// finish vincula el objeto finalizado al recurso y encola el scan
// (misma ruta idempotente que la subida prefirmada; cierra su race
// porque solo corre cuando los bytes ya están completos).
// El recurso viaja en la metadata (persistida en el .info de S3),
// no en Redis: sin TTL que gestionar ni huérfanos que barrer.
func (h *tusHandler) finish(event handler.HookEvent) {
	id, err := strconv.ParseUint(event.Upload.MetaData["resource_id"], 10, 32)
	if err != nil || id == 0 {
		log.Printf("tus: finish sin resource_id id=%s", event.Upload.ID)
		return
	}
	var r courses.Resource
	if err := h.DB.First(&r, uint(id)).Error; err != nil {
		log.Printf("tus: recurso %d no encontrado", id)
		return
	}
	r.ObjectKey = s3ObjectKey(event.Upload.ID)
	r.ScanStatus = courses.ScanPending
	if courses.IsMediaType(r.Type) {
		r.ProcessingStatus = courses.ProcessingPending
		r.HLSKey = ""
	} else {
		r.ProcessingStatus = courses.ProcessingNone
	}
	if err := h.DB.Save(&r).Error; err != nil {
		log.Printf("tus: no se pudo vincular recurso %d: %v", id, err)
		return
	}
	if h.Queue == nil {
		return
	}
	key := "scan-" + r.StableID
	if _, err := h.Queue.EnqueueScan(queue.ScanPayload{
		ResourceID:   r.ID,
		ObjectKey:    r.ObjectKey,
		TranscodeKey: "transcode-" + r.StableID,
	}, key); err != nil {
		log.Printf("tus: no se pudo encolar scan recurso %d: %v", id, err)
	}
}
