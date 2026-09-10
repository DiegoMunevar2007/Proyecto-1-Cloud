package courses

// CreateCourseRequest contiene los metadatos para crear un curso (borrador).
type CreateCourseRequest struct {
	Title          string `json:"title" example:"Introducción a Go"`
	Slug           string `json:"slug" example:"introduccion-a-go"`
	Description    string `json:"description" example:"Curso inicial de programación en Go"`
	ThumbnailKey   string `json:"thumbnail_key" example:"thumbs/intro-go.png"`
	MinRequiredPct int    `json:"min_required_pct" example:"80"`
}

// UpdateCourseRequest edita metadatos del curso (solo no publicado).
type UpdateCourseRequest struct {
	Title        string `json:"title" example:"Introducción a Go v2"`
	Description  string `json:"description" example:"Nueva descripción"`
	ThumbnailKey string `json:"thumbnail_key" example:"thumbs/intro-go-v2.png"`
}

// CreateModuleRequest crea un módulo en el borrador editable.
type CreateModuleRequest struct {
	Title       string `json:"title" example:"Módulo 1: Fundamentos"`
	Description string `json:"description" example:"Variables, tipos y funciones"`
}

// CreateUnitRequest crea una unidad dentro de un módulo.
type CreateUnitRequest struct {
	Title       string `json:"title" example:"Unidad 1: Variables"`
	Description string `json:"description" example:"Declaración y uso de variables"`
}

// OrderedIDsRequest reordena elementos según la lista de IDs (posición 1-based).
type OrderedIDsRequest struct {
	OrderedIDs []uint `json:"ordered_ids" example:"3,1,2"`
}

// UploadURLRequest solicita una URL prefirmada de subida directa.
type UploadURLRequest struct {
	ObjectKey string `json:"object_key" example:"videos/leccion-1.mp4"`
	MimeType  string `json:"mime_type" example:"video/mp4"`
	SizeBytes int64  `json:"size_bytes" example:"52428800"`
}

// CoursesResponse es el catálogo público paginado (solo publicados).
type CoursesResponse struct {
	Courses []Course `json:"courses"`
	Total   int64    `json:"total" example:"12"`
	Page    int      `json:"page" example:"1"`
	Limit   int      `json:"limit" example:"20"`
}

// CourseDetailResponse retorna el curso con su árbol de contenido ordenado.
type CourseDetailResponse struct {
	Course  Course        `json:"course"`
	Version CourseVersion `json:"version"`
}

// CourseCreatedResponse retorna el curso y su versión 1 borrador.
type CourseCreatedResponse struct {
	Course  Course        `json:"course"`
	Version CourseVersion `json:"version"`
}

// ModuleEnvelope envuelve un módulo en la respuesta.
type ModuleEnvelope struct {
	Module Module `json:"module"`
}

// UnitEnvelope envuelve una unidad en la respuesta.
type UnitEnvelope struct {
	Unit Unit `json:"unit"`
}

// ResourceEnvelope envuelve un recurso en la respuesta.
type ResourceEnvelope struct {
	Resource Resource `json:"resource"`
}

// ValidateResponse retorna la lista exhaustiva de errores de publicación.
type ValidateResponse struct {
	Valid  bool     `json:"valid" example:"true"`
	Errors []string `json:"errors"`
}

// PublishResponse confirma la publicación de una versión inmutable.
type PublishResponse struct {
	Message string        `json:"message" example:"versión publicada"`
	Version CourseVersion `json:"version"`
}

// UnpublishResponse confirma el despublicado temporal del curso.
type UnpublishResponse struct {
	Message string `json:"message" example:"curso despublicado temporalmente"`
	Course  Course `json:"course"`
}

// VersionEnvelope envuelve una versión en la respuesta.
type VersionEnvelope struct {
	Version CourseVersion `json:"version"`
}

// UploadURLResponse entrega la URL prefirmada y el trabajo de transcode.
type UploadURLResponse struct {
	UploadURL string   `json:"upload_url" example:"http://localhost:9000/originals/videos/leccion-1.mp4?X-Amz-Algorithm=..."`
	TaskID    string   `json:"task_id" example:"transcode-550e8400-e29b-41d4-a716-446655440000"`
	Resource  Resource `json:"resource"`
}

// DownloadURLResponse entrega la URL de consumo del recurso.
type DownloadURLResponse struct {
	URL string `json:"url" example:"http://localhost:9000/hls/550e8400/index.m3u8"`
	HLS bool   `json:"hls" example:"true"`
}
