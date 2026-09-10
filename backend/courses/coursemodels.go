package courses

import (
	"strings"
	"time"

	"github.com/DiegoMunevar2007/Proyecto-1-Cloud.git/auth"
)

// Estados de curso y de versión.
const (
	CourseStatusDraft       = "draft"
	CourseStatusPublished   = "published"
	CourseStatusUnpublished = "unpublished"
)

var validCourseStatus = map[string]bool{
	CourseStatusDraft:       true,
	CourseStatusPublished:   true,
	CourseStatusUnpublished: true,
}

// NormalizeCourseStatus normaliza el estado del curso; vacío -> draft.
func NormalizeCourseStatus(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return CourseStatusDraft
	}
	return s
}

// IsValidCourseStatus verifica si el estado es permitido.
func IsValidCourseStatus(s string) bool {
	return validCourseStatus[s]
}

// Estados de versión.
const (
	VersionStatusDraft     = "draft"
	VersionStatusPublished = "published"
	VersionStatusArchived  = "archived"
)

var validVersionStatus = map[string]bool{
	VersionStatusDraft:     true,
	VersionStatusPublished: true,
	VersionStatusArchived:  true,
}

// NormalizeVersionStatus normaliza el estado de versión; vacío -> draft.
func NormalizeVersionStatus(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return VersionStatusDraft
	}
	return s
}

// IsValidVersionStatus verifica si el estado de versión es permitido.
func IsValidVersionStatus(s string) bool {
	return validVersionStatus[s]
}

// Tipos de recurso admitidos en el MVP.
const (
	ResourceTypeText  = "text"
	ResourceTypeImage = "image"
	ResourceTypeVideo = "video"
	ResourceTypeAudio = "audio"
	ResourceTypePDF   = "pdf"
	ResourceTypeSlides = "slides"
	ResourceTypeFile  = "file"
	ResourceTypeIframe = "iframe"
	ResourceTypeLink  = "link"
	ResourceTypeQuiz  = "quiz"
)

var validResourceType = map[string]bool{
	ResourceTypeText:   true,
	ResourceTypeImage:  true,
	ResourceTypeVideo:  true,
	ResourceTypeAudio:  true,
	ResourceTypePDF:    true,
	ResourceTypeSlides: true,
	ResourceTypeFile:   true,
	ResourceTypeIframe: true,
	ResourceTypeLink:   true,
	ResourceTypeQuiz:   true,
}

// NormalizeResourceType normaliza el tipo de recurso.
func NormalizeResourceType(t string) string {
	return strings.TrimSpace(strings.ToLower(t))
}

// IsValidResourceType verifica si el tipo de recurso es permitido.
func IsValidResourceType(t string) bool {
	return validResourceType[NormalizeResourceType(t)]
}

// IsMediaType indica si el tipo requiere procesamiento asíncrono a HLS.
func IsMediaType(t string) bool {
	t = NormalizeResourceType(t)
	return t == ResourceTypeVideo || t == ResourceTypeAudio
}

// Estados de procesamiento de recursos multimedia.
const (
	ProcessingNone       = "none"
	ProcessingPending    = "pending"
	ProcessingProcessing = "processing"
	ProcessingReady      = "ready"
	ProcessingFailed     = "failed"
)

var validProcessingStatus = map[string]bool{
	ProcessingNone:       true,
	ProcessingPending:    true,
	ProcessingProcessing: true,
	ProcessingReady:      true,
	ProcessingFailed:     true,
}

// IsValidProcessingStatus verifica si el estado de procesamiento es permitido.
func IsValidProcessingStatus(s string) bool {
	return validProcessingStatus[s]
}

// Course es el agregado raíz de autoría.
type Course struct {
	ID               uint           `gorm:"primaryKey" json:"id"`
	CreatedAt        time.Time      `json:"created_at"`
	UpdatedAt        time.Time      `json:"updated_at"`
	DeletedAt        *time.Time     `gorm:"index" json:"-"`
	Title            string         `gorm:"not null" json:"title"`
	Slug             string         `gorm:"uniqueIndex;not null" json:"slug"`
	Description      string         `gorm:"type:text;not null" json:"description"`
	ThumbnailKey     string         `json:"thumbnail_key"`
	Status           string         `gorm:"not null;default:'draft';index" json:"status"`
	CurrentVersionID *uint          `json:"current_version_id"`
	CurrentVersion   *CourseVersion `gorm:"foreignKey:CurrentVersionID" json:"current_version,omitempty"`
	CreatedByID      uint           `gorm:"not null;index" json:"created_by_id"`
	CreatedBy        auth.UserModel `gorm:"foreignKey:CreatedByID" json:"-"`
}

// CourseVersion es una versión numerada del curso. Publicada => inmutable.
type CourseVersion struct {
	ID              uint       `gorm:"primaryKey" json:"id"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
	CourseID        uint       `gorm:"not null;index:idx_course_number,unique" json:"course_id"`
	Number          int        `gorm:"not null;index:idx_course_number,unique" json:"number"`
	Status          string     `gorm:"not null;default:'draft';index" json:"status"`
	IsImmutable     bool       `gorm:"not null;default:false" json:"is_immutable"`
	MinRequiredPct  int        `gorm:"not null;default:80" json:"min_required_pct"`
	PublishedAt     *time.Time `json:"published_at"`
	Modules         []Module   `gorm:"foreignKey:CourseVersionID" json:"modules,omitempty"`
}

// Module ordenado dentro de una versión.
type Module struct {
	ID              uint      `gorm:"primaryKey" json:"id"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
	CourseVersionID uint      `gorm:"not null;index" json:"course_version_id"`
	StableID        string    `gorm:"not null;index" json:"stable_id"`
	Position        int       `gorm:"not null;index" json:"position"`
	Title           string    `gorm:"not null" json:"title"`
	Description     string    `gorm:"type:text" json:"description"`
	Units           []Unit    `gorm:"foreignKey:ModuleID" json:"units,omitempty"`
}

// Unit ordenada dentro de un módulo.
type Unit struct {
	ID          uint       `gorm:"primaryKey" json:"id"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	ModuleID    uint       `gorm:"not null;index" json:"module_id"`
	StableID    string     `gorm:"not null;index" json:"stable_id"`
	Position    int        `gorm:"not null;index" json:"position"`
	Title       string     `gorm:"not null" json:"title"`
	Description string     `gorm:"type:text" json:"description"`
	Resources   []Resource `gorm:"foreignKey:UnitID" json:"resources,omitempty"`
}

// Resource es el nivel hoja: contenido o quiz.
type Resource struct {
	ID               uint      `gorm:"primaryKey" json:"id"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
	UnitID           uint      `gorm:"not null;index" json:"unit_id"`
	StableID         string    `gorm:"not null;index" json:"stable_id"`
	Position         int       `gorm:"not null;index" json:"position"`
	Type             string    `gorm:"not null;index" json:"type"`
	Title            string    `gorm:"not null" json:"title"`
	IsVisible        bool      `gorm:"not null;default:true" json:"is_visible"`
	IsRequired       bool      `gorm:"not null;default:true" json:"is_required"`
	AllowDownload    bool      `gorm:"not null;default:false" json:"allow_download"`
	MarkdownBody     string    `gorm:"type:text" json:"markdown_body"`
	ObjectKey        string    `json:"object_key"`
	HLSKey           string    `json:"hls_key"`
	ProcessingStatus string    `gorm:"not null;default:'none';index" json:"processing_status"`
	MimeType         string    `json:"mime_type"`
	SizeBytes        int64     `json:"size_bytes"`
	ExternalURL      string    `json:"external_url"`
}

// Matricula conserva inscripción de estudiantes (progreso se añade en etapa 5.1-10).
type Matricula struct {
	ID               uint           `gorm:"primaryKey" json:"id"`
	CreatedAt        time.Time      `json:"created_at"`
	UpdatedAt        time.Time      `json:"updated_at"`
	StudentID        uint           `gorm:"not null;index:idx_student_course,unique" json:"student_id"`
	Student          auth.UserModel `gorm:"foreignKey:StudentID" json:"-"`
	CourseID         uint           `gorm:"not null;index:idx_student_course,unique" json:"course_id"`
	Course           Course         `gorm:"foreignKey:CourseID" json:"-"`
	Inscrito         bool           `gorm:"not null;default:true" json:"inscrito"`
	FechaInscripcion time.Time      `gorm:"not null" json:"fecha_inscripcion"`
}
