package courses

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

var (
	ErrNotFound       = errors.New("recurso no encontrado")
	ErrImmutable      = errors.New("la versión publicada es inmutable; cree una nueva versión borrador")
	ErrPublishedEdit  = errors.New("el curso está publicado; despublíquelo temporalmente para editar (MVP)")
	ErrForbidden      = errors.New("no tiene permiso sobre este curso")
	ErrInvalidPayload = errors.New("datos inválidos")
)

var slugRe = regexp.MustCompile(`[^a-z0-9-]+`)

// Slugify genera un slug URL-safe a partir del título.
func Slugify(title string) string {
	s := strings.TrimSpace(strings.ToLower(title))
	s = strings.ReplaceAll(s, " ", "-")
	s = strings.ReplaceAll(s, "_", "-")
	s = slugRe.ReplaceAllString(s, "")
	s = strings.Trim(s, "-")
	if s == "" {
		s = "curso"
	}
	return s
}

func newStableID() string {
	return uuid.NewString()
}

// IsOwnerOrAdmin verifica propiedad del curso o rol admin.
func IsOwnerOrAdmin(course *Course, userID uint, role string) bool {
	if role == "admin" {
		return true
	}
	return course.CreatedByID == userID
}

func getCourse(db *gorm.DB, courseID uint) (*Course, error) {
	var c Course
	if err := db.First(&c, courseID).Error; err != nil {
		return nil, ErrNotFound
	}
	return &c, nil
}

func getVersion(db *gorm.DB, versionID uint) (*CourseVersion, error) {
	var v CourseVersion
	if err := db.First(&v, versionID).Error; err != nil {
		return nil, ErrNotFound
	}
	return &v, nil
}

func editableVersion(db *gorm.DB, courseID uint) (*CourseVersion, error) {
	var v CourseVersion
	if err := db.Where("course_id = ? AND status = ?", courseID, VersionStatusDraft).Order("number desc").First(&v).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrImmutable
		}
		return nil, err
	}
	if v.IsImmutable {
		return nil, ErrImmutable
	}
	return &v, nil
}

func maxPosition(db *gorm.DB, model interface{}, scope string, scopeID uint) int {
	var pos *int
	db.Model(model).Where(scope+" = ?", scopeID).Select("MAX(position)").Scan(&pos)
	if pos == nil {
		return 0
	}
	return *pos
}

// CreateCourse crea el curso (borrador) con su versión 1 draft.
func CreateCourse(db *gorm.DB, userID uint, title, slug, description, thumbnailKey string, minRequiredPct int) (*Course, *CourseVersion, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return nil, nil, fmt.Errorf("%w: título requerido", ErrInvalidPayload)
	}
	if slug == "" {
		slug = Slugify(title)
	} else {
		slug = Slugify(slug)
	}
	if minRequiredPct <= 0 || minRequiredPct > 100 {
		minRequiredPct = 80
	}
	course := &Course{
		Title:        title,
		Slug:         slug,
		Description:  description,
		ThumbnailKey: thumbnailKey,
		Status:       CourseStatusDraft,
		CreatedByID:  userID,
	}
	if err := db.Create(course).Error; err != nil {
		return nil, nil, err
	}
	version := &CourseVersion{
		CourseID:       course.ID,
		Number:         1,
		Status:         VersionStatusDraft,
		MinRequiredPct: minRequiredPct,
	}
	if err := db.Create(version).Error; err != nil {
		return nil, nil, err
	}
	return course, version, nil
}

// UpdateCourseMeta edita metadatos solo si el curso no está publicado.
func UpdateCourseMeta(db *gorm.DB, courseID uint, userID uint, role, title, description, thumbnailKey string) (*Course, error) {
	c, err := getCourse(db, courseID)
	if err != nil {
		return nil, err
	}
	if !IsOwnerOrAdmin(c, userID, role) {
		return nil, ErrForbidden
	}
	if c.Status == CourseStatusPublished {
		return nil, ErrPublishedEdit
	}
	if title != "" {
		c.Title = strings.TrimSpace(title)
	}
	if description != "" {
		c.Description = description
	}
	if thumbnailKey != "" {
		c.ThumbnailKey = thumbnailKey
	}
	if err := db.Save(c).Error; err != nil {
		return nil, err
	}
	return c, nil
}

// AddModule agrega un módulo al borrador editable.
func AddModule(db *gorm.DB, courseID uint, userID uint, role, title, description string) (*Module, error) {
	c, err := getCourse(db, courseID)
	if err != nil {
		return nil, err
	}
	if !IsOwnerOrAdmin(c, userID, role) {
		return nil, ErrForbidden
	}
	if c.Status == CourseStatusPublished {
		return nil, ErrPublishedEdit
	}
	v, err := editableVersion(db, courseID)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(title) == "" {
		return nil, fmt.Errorf("%w: título de módulo requerido", ErrInvalidPayload)
	}
	m := &Module{
		CourseVersionID: v.ID,
		StableID:        newStableID(),
		Position:        maxPosition(db, &Module{}, "course_version_id", v.ID) + 1,
		Title:           strings.TrimSpace(title),
		Description:     description,
	}
	if err := db.Create(m).Error; err != nil {
		return nil, err
	}
	return m, nil
}

// AddUnit agrega una unidad a un módulo del borrador.
func AddUnit(db *gorm.DB, moduleID uint, userID uint, role, title, description string) (*Unit, error) {
	var m Module
	if err := db.First(&m, moduleID).Error; err != nil {
		return nil, ErrNotFound
	}
	var v CourseVersion
	if err := db.First(&v, m.CourseVersionID).Error; err != nil {
		return nil, ErrNotFound
	}
	var c Course
	if err := db.First(&c, v.CourseID).Error; err != nil {
		return nil, ErrNotFound
	}
	if !IsOwnerOrAdmin(&c, userID, role) {
		return nil, ErrForbidden
	}
	if v.IsImmutable || v.Status != VersionStatusDraft {
		return nil, ErrImmutable
	}
	if c.Status == CourseStatusPublished {
		return nil, ErrPublishedEdit
	}
	if strings.TrimSpace(title) == "" {
		return nil, fmt.Errorf("%w: título de unidad requerido", ErrInvalidPayload)
	}
	u := &Unit{
		ModuleID:    m.ID,
		StableID:    newStableID(),
		Position:    maxPosition(db, &Unit{}, "module_id", m.ID) + 1,
		Title:       strings.TrimSpace(title),
		Description: description,
	}
	if err := db.Create(u).Error; err != nil {
		return nil, err
	}
	return u, nil
}

// ResourceInput es la entrada para crear/actualizar recursos.
type ResourceInput struct {
	Type          string `json:"type" example:"text"`
	Title         string `json:"title" example:"Lección 1: Variables"`
	IsVisible     *bool  `json:"is_visible" example:"true"`
	IsRequired    *bool  `json:"is_required" example:"true"`
	AllowDownload bool   `json:"allow_download" example:"false"`
	MarkdownBody  string `json:"markdown_body" example:"# Variables en Go"`
	ExternalURL   string `json:"external_url" example:"https://ejemplo.com/recurso"`
	MimeType      string `json:"mime_type" example:"video/mp4"`
	SizeBytes     int64  `json:"size_bytes" example:"52428800"`
}

func validateResourceInput(in ResourceInput) error {
	t := NormalizeResourceType(in.Type)
	if !IsValidResourceType(t) {
		return fmt.Errorf("%w: tipo de recurso inválido", ErrInvalidPayload)
	}
	if strings.TrimSpace(in.Title) == "" {
		return fmt.Errorf("%w: título de recurso requerido", ErrInvalidPayload)
	}
	switch t {
	case ResourceTypeText:
		if strings.TrimSpace(in.MarkdownBody) == "" {
			return fmt.Errorf("%w: text requiere markdown_body", ErrInvalidPayload)
		}
	case ResourceTypeLink, ResourceTypeIframe:
		if strings.TrimSpace(in.ExternalURL) == "" {
			return fmt.Errorf("%w: link/iframe requiere external_url", ErrInvalidPayload)
		}
	}
	return nil
}

// AddResource agrega un recurso a una unidad del borrador.
func AddResource(db *gorm.DB, unitID uint, userID uint, role string, in ResourceInput) (*Resource, error) {
	var u Unit
	if err := db.First(&u, unitID).Error; err != nil {
		return nil, ErrNotFound
	}
	var m Module
	if err := db.First(&m, u.ModuleID).Error; err != nil {
		return nil, ErrNotFound
	}
	var v CourseVersion
	if err := db.First(&v, m.CourseVersionID).Error; err != nil {
		return nil, ErrNotFound
	}
	var c Course
	if err := db.First(&c, v.CourseID).Error; err != nil {
		return nil, ErrNotFound
	}
	if !IsOwnerOrAdmin(&c, userID, role) {
		return nil, ErrForbidden
	}
	if v.IsImmutable || v.Status != VersionStatusDraft {
		return nil, ErrImmutable
	}
	if c.Status == CourseStatusPublished {
		return nil, ErrPublishedEdit
	}
	if err := validateResourceInput(in); err != nil {
		return nil, err
	}
	proc := ProcessingNone
	if IsMediaType(in.Type) {
		proc = ProcessingPending
	}
	visible, required := true, true
	if in.IsVisible != nil {
		visible = *in.IsVisible
	}
	if in.IsRequired != nil {
		required = *in.IsRequired
	}
	r := &Resource{
		UnitID:           u.ID,
		StableID:         newStableID(),
		Position:         maxPosition(db, &Resource{}, "unit_id", u.ID) + 1,
		Type:             NormalizeResourceType(in.Type),
		Title:            strings.TrimSpace(in.Title),
		IsVisible:        visible,
		IsRequired:       required,
		AllowDownload:    in.AllowDownload,
		MarkdownBody:     in.MarkdownBody,
		ExternalURL:      strings.TrimSpace(in.ExternalURL),
		MimeType:         in.MimeType,
		SizeBytes:        in.SizeBytes,
		ProcessingStatus: proc,
	}
	if err := db.Create(r).Error; err != nil {
		return nil, err
	}
	return r, nil
}

// UpdateResource edita un recurso del borrador (título, visibilidad, markdown, url).
func UpdateResource(db *gorm.DB, resourceID uint, userID uint, role string, in ResourceInput) (*Resource, error) {
	var r Resource
	if err := db.First(&r, resourceID).Error; err != nil {
		return nil, ErrNotFound
	}
	var u Unit
	if err := db.First(&u, r.UnitID).Error; err != nil {
		return nil, ErrNotFound
	}
	var m Module
	if err := db.First(&m, u.ModuleID).Error; err != nil {
		return nil, ErrNotFound
	}
	var v CourseVersion
	if err := db.First(&v, m.CourseVersionID).Error; err != nil {
		return nil, ErrNotFound
	}
	var c Course
	if err := db.First(&c, v.CourseID).Error; err != nil {
		return nil, ErrNotFound
	}
	if !IsOwnerOrAdmin(&c, userID, role) {
		return nil, ErrForbidden
	}
	if v.IsImmutable || v.Status != VersionStatusDraft {
		return nil, ErrImmutable
	}
	if in.Title != "" {
		r.Title = strings.TrimSpace(in.Title)
	}
	if in.IsVisible != nil {
		r.IsVisible = *in.IsVisible
	}
	if in.IsRequired != nil {
		r.IsRequired = *in.IsRequired
	}
	r.AllowDownload = in.AllowDownload
	if in.MarkdownBody != "" {
		r.MarkdownBody = in.MarkdownBody
	}
	if in.ExternalURL != "" {
		r.ExternalURL = strings.TrimSpace(in.ExternalURL)
	}
	if err := db.Save(&r).Error; err != nil {
		return nil, err
	}
	return &r, nil
}

// ReorderModules reordena módulos según lista de IDs.
func ReorderModules(db *gorm.DB, courseID uint, userID uint, role string, orderedIDs []uint) error {
	c, err := getCourse(db, courseID)
	if err != nil {
		return err
	}
	if !IsOwnerOrAdmin(c, userID, role) {
		return ErrForbidden
	}
	v, err := editableVersion(db, courseID)
	if err != nil {
		return err
	}
	return db.Transaction(func(tx *gorm.DB) error {
		for i, id := range orderedIDs {
			var m Module
			if err := tx.Where("id = ? AND course_version_id = ?", id, v.ID).First(&m).Error; err != nil {
				return fmt.Errorf("%w: módulo %d", ErrNotFound, id)
			}
			m.Position = i + 1
			if err := tx.Save(&m).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// ReorderUnits reordena unidades dentro de un módulo.
func ReorderUnits(db *gorm.DB, moduleID uint, userID uint, role string, orderedIDs []uint) error {
	var m Module
	if err := db.First(&m, moduleID).Error; err != nil {
		return ErrNotFound
	}
	var v CourseVersion
	if err := db.First(&v, m.CourseVersionID).Error; err != nil {
		return ErrNotFound
	}
	var c Course
	if err := db.First(&c, v.CourseID).Error; err != nil {
		return ErrNotFound
	}
	if !IsOwnerOrAdmin(&c, userID, role) {
		return ErrForbidden
	}
	if v.IsImmutable {
		return ErrImmutable
	}
	return db.Transaction(func(tx *gorm.DB) error {
		for i, id := range orderedIDs {
			var u Unit
			if err := tx.Where("id = ? AND module_id = ?", id, m.ID).First(&u).Error; err != nil {
				return fmt.Errorf("%w: unidad %d", ErrNotFound, id)
			}
			u.Position = i + 1
			if err := tx.Save(&u).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// ReorderResources reordena recursos dentro de una unidad.
func ReorderResources(db *gorm.DB, unitID uint, userID uint, role string, orderedIDs []uint) error {
	var u Unit
	if err := db.First(&u, unitID).Error; err != nil {
		return ErrNotFound
	}
	var m Module
	if err := db.First(&m, u.ModuleID).Error; err != nil {
		return ErrNotFound
	}
	var v CourseVersion
	if err := db.First(&v, m.CourseVersionID).Error; err != nil {
		return ErrNotFound
	}
	var c Course
	if err := db.First(&c, v.CourseID).Error; err != nil {
		return ErrNotFound
	}
	if !IsOwnerOrAdmin(&c, userID, role) {
		return ErrForbidden
	}
	if v.IsImmutable {
		return ErrImmutable
	}
	return db.Transaction(func(tx *gorm.DB) error {
		for i, id := range orderedIDs {
			var r Resource
			if err := tx.Where("id = ? AND unit_id = ?", id, u.ID).First(&r).Error; err != nil {
				return fmt.Errorf("%w: recurso %d", ErrNotFound, id)
			}
			r.Position = i + 1
			if err := tx.Save(&r).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// GetFullTree retorna el curso con versión, módulos, unidades y recursos ordenados (previsualización).
func GetFullTree(db *gorm.DB, courseID uint) (*Course, *CourseVersion, error) {
	c, err := getCourse(db, courseID)
	if err != nil {
		return nil, nil, err
	}
	var v CourseVersion
	q := db.Where("course_id = ?", courseID).Order("number desc")
	if c.CurrentVersionID != nil {
		// Preferir versión vigente para estudiantes; si hay draft, quien previsualiza usa el draft.
		var draft CourseVersion
		if err := db.Where("course_id = ? AND status = ?", courseID, VersionStatusDraft).Order("number desc").First(&draft).Error; err == nil {
			v = draft
		} else if err := db.First(&v, *c.CurrentVersionID).Error; err != nil {
			return nil, nil, ErrNotFound
		}
	} else {
		if err := q.First(&v).Error; err != nil {
			return nil, nil, ErrNotFound
		}
	}
	if err := db.Where("course_version_id = ?", v.ID).Order("position asc").
		Preload("Units", func(db *gorm.DB) *gorm.DB { return db.Order("position asc") }).
		Preload("Units.Resources", func(db *gorm.DB) *gorm.DB { return db.Order("position asc") }).
		Find(&v.Modules).Error; err != nil {
		return nil, nil, err
	}
	// Cargar unidades con recursos ya viene por preload anidado manual:
	for i := range v.Modules {
		for j := range v.Modules[i].Units {
			var res []Resource
			db.Where("unit_id = ?", v.Modules[i].Units[j].ID).Order("position asc").Find(&res)
			v.Modules[i].Units[j].Resources = res
		}
	}
	return c, &v, nil
}

// ValidatePublishable retorna la lista exhaustiva de errores que impiden publicar.
func ValidatePublishable(db *gorm.DB, courseID uint) []string {
	errs := []string{}
	c, err := getCourse(db, courseID)
	if err != nil {
		return []string{"curso no encontrado"}
	}
	if strings.TrimSpace(c.Title) == "" {
		errs = append(errs, "título del curso requerido")
	}
	if strings.TrimSpace(c.Description) == "" {
		errs = append(errs, "descripción del curso requerida")
	}
	v, err := editableVersion(db, courseID)
	if err != nil {
		return append(errs, "no hay versión borrador editable")
	}
	if v.MinRequiredPct <= 0 || v.MinRequiredPct > 100 {
		errs = append(errs, "criterio de aprobación inválido (min_required_pct 1-100)")
	}
	var modules []Module
	db.Where("course_version_id = ?", v.ID).Order("position asc").Find(&modules)
	if len(modules) == 0 {
		errs = append(errs, "el curso debe tener al menos un módulo")
		return errs
	}
	hasVisibleReady := false
	for _, m := range modules {
		if strings.TrimSpace(m.Title) == "" {
			errs = append(errs, fmt.Sprintf("módulo %d sin título", m.ID))
		}
		var units []Unit
		db.Where("module_id = ?", m.ID).Order("position asc").Find(&units)
		if len(units) == 0 {
			errs = append(errs, fmt.Sprintf("módulo '%s' debe tener al menos una unidad", m.Title))
			continue
		}
		for _, u := range units {
			if strings.TrimSpace(u.Title) == "" {
				errs = append(errs, fmt.Sprintf("unidad %d sin título", u.ID))
			}
			var res []Resource
			db.Where("unit_id = ?", u.ID).Order("position asc").Find(&res)
			if len(res) == 0 {
				errs = append(errs, fmt.Sprintf("unidad '%s' debe tener al menos un recurso", u.Title))
				continue
			}
			for _, r := range res {
				if !r.IsVisible {
					continue
				}
				switch NormalizeResourceType(r.Type) {
				case ResourceTypeVideo, ResourceTypeAudio:
					if r.ProcessingStatus != ProcessingReady {
						errs = append(errs, fmt.Sprintf("recurso visible '%s' no disponible (processing=%s)", r.Title, r.ProcessingStatus))
					} else {
						hasVisibleReady = true
					}
				case ResourceTypeText:
					if strings.TrimSpace(r.MarkdownBody) == "" {
						errs = append(errs, fmt.Sprintf("recurso visible '%s' sin contenido markdown", r.Title))
					} else {
						hasVisibleReady = true
					}
				default:
					if r.ObjectKey == "" && strings.TrimSpace(r.ExternalURL) == "" && strings.TrimSpace(r.MarkdownBody) == "" {
						errs = append(errs, fmt.Sprintf("recurso visible '%s' sin contenido disponible", r.Title))
					} else {
						hasVisibleReady = true
					}
				}
			}
		}
	}
	if !hasVisibleReady {
		errs = append(errs, "el curso debe tener al menos un recurso visible y disponible")
	}
	return errs
}

// PublishVersion valida y publica el borrador como versión inmutable.
func PublishVersion(db *gorm.DB, courseID uint, userID uint, role string) (*CourseVersion, error) {
	c, err := getCourse(db, courseID)
	if err != nil {
		return nil, err
	}
	if !IsOwnerOrAdmin(c, userID, role) {
		return nil, ErrForbidden
	}
	if errs := ValidatePublishable(db, courseID); len(errs) > 0 {
		return nil, fmt.Errorf("%w: %s", ErrInvalidPayload, strings.Join(errs, "; "))
	}
	v, err := editableVersion(db, courseID)
	if err != nil {
		return nil, err
	}
	now := v.CreatedAt
	_ = now
	err = db.Transaction(func(tx *gorm.DB) error {
		v.Status = VersionStatusPublished
		v.IsImmutable = true
		t := v.UpdatedAt
		_ = t
		if err := tx.Save(v).Error; err != nil {
			return err
		}
		c.Status = CourseStatusPublished
		c.CurrentVersionID = &v.ID
		return tx.Save(c).Error
	})
	if err != nil {
		return nil, err
	}
	return v, nil
}

// UnpublishCourse despublica temporalmente (MVP) para permitir edición.
func UnpublishCourse(db *gorm.DB, courseID uint, userID uint, role string) (*Course, error) {
	c, err := getCourse(db, courseID)
	if err != nil {
		return nil, err
	}
	if !IsOwnerOrAdmin(c, userID, role) {
		return nil, ErrForbidden
	}
	c.Status = CourseStatusUnpublished
	if err := db.Save(c).Error; err != nil {
		return nil, err
	}
	return c, nil
}

// NewDraftVersion crea un borrador de actualización clonando la versión vigente y preservando stable_id.
func NewDraftVersion(db *gorm.DB, courseID uint, userID uint, role string) (*CourseVersion, error) {
	c, err := getCourse(db, courseID)
	if err != nil {
		return nil, err
	}
	if !IsOwnerOrAdmin(c, userID, role) {
		return nil, ErrForbidden
	}
	var existing CourseVersion
	if err := db.Where("course_id = ? AND status = ?", courseID, VersionStatusDraft).First(&existing).Error; err == nil {
		return nil, fmt.Errorf("%w: ya existe un borrador", ErrInvalidPayload)
	}
	var current *CourseVersion
	if c.CurrentVersionID != nil {
		var cv CourseVersion
		if err := db.First(&cv, *c.CurrentVersionID).Error; err == nil {
			current = &cv
		}
	}
	if current == nil {
		if err := db.Where("course_id = ?", courseID).Order("number desc").First(&current).Error; err != nil {
			return nil, ErrNotFound
		}
	}
	var nextNum int
	db.Model(&CourseVersion{}).Where("course_id = ?", courseID).Select("COALESCE(MAX(number),0)+1").Scan(&nextNum)
	draft := &CourseVersion{
		CourseID:       courseID,
		Number:         nextNum,
		Status:         VersionStatusDraft,
		MinRequiredPct: current.MinRequiredPct,
	}
	err = db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(draft).Error; err != nil {
			return err
		}
		var modules []Module
		if err := tx.Where("course_version_id = ?", current.ID).Order("position asc").Find(&modules).Error; err != nil {
			return err
		}
		for _, m := range modules {
			nm := Module{CourseVersionID: draft.ID, StableID: m.StableID, Position: m.Position, Title: m.Title, Description: m.Description}
			if err := tx.Create(&nm).Error; err != nil {
				return err
			}
			var units []Unit
			if err := tx.Where("module_id = ?", m.ID).Order("position asc").Find(&units).Error; err != nil {
				return err
			}
			for _, u := range units {
				nu := Unit{ModuleID: nm.ID, StableID: u.StableID, Position: u.Position, Title: u.Title, Description: u.Description}
				if err := tx.Create(&nu).Error; err != nil {
					return err
				}
				var res []Resource
				if err := tx.Where("unit_id = ?", u.ID).Order("position asc").Find(&res).Error; err != nil {
					return err
				}
				for _, r := range res {
					nr := Resource{
						UnitID: r.UnitID, StableID: r.StableID, Position: r.Position,
						Type: r.Type, Title: r.Title, IsVisible: r.IsVisible, IsRequired: r.IsRequired,
						AllowDownload: r.AllowDownload, MarkdownBody: r.MarkdownBody, ObjectKey: r.ObjectKey,
						HLSKey: r.HLSKey, ProcessingStatus: r.ProcessingStatus, MimeType: r.MimeType,
						SizeBytes: r.SizeBytes, ExternalURL: r.ExternalURL,
					}
					nr.UnitID = nu.ID
					if err := tx.Create(&nr).Error; err != nil {
						return err
					}
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return draft, nil
}
