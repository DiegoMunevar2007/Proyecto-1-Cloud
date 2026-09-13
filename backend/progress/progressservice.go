package progress

import (
	"errors"
	"log"

	"github.com/DiegoMunevar2007/Proyecto-1-Cloud.git/badges"
	"github.com/DiegoMunevar2007/Proyecto-1-Cloud.git/courses"
	"github.com/DiegoMunevar2007/Proyecto-1-Cloud.git/enroll"
	"github.com/DiegoMunevar2007/Proyecto-1-Cloud.git/quiz"
	"gorm.io/gorm"
)

// ErrClientProgress indica manipulación: el cliente envió avance declarado.
var ErrClientProgress = errors.New("el progreso lo calcula el servidor; percent/completed no se aceptan")

// stableCourse resuelve recurso + curso verificando inscripción o propiedad.
func stableCourse(db *gorm.DB, studentID uint, stableID string) (*courses.Resource, *courses.Course, error) {
	r, err := courses.ByStableID(db, stableID)
	if err != nil {
		return nil, nil, err
	}
	_, c, _, err := courses.LocateResource(db, r.ID)
	if err != nil {
		return nil, nil, err
	}
	if !enroll.IsEnrolled(db, studentID, c.ID) && !courses.IsOwnerOrAdmin(c, studentID, "") {
		return nil, nil, courses.ErrForbidden
	}
	return r, c, nil
}

// Heartbeat registra reproducción/apertura. Completed es monótono (nunca retrocede).
func Heartbeat(db *gorm.DB, studentID uint, in HeartbeatInput) (*Progress, error) {
	if in.Percent != nil || in.Completed != nil {
		log.Printf("AUDIT progreso manipulado student=%d stable=%s", studentID, in.StableID)
		return nil, ErrClientProgress
	}
	if in.StableID == "" || in.PositionSec < 0 || in.DurationSec < 0 || in.Page < 0 || in.TotalPages < 0 {
		return nil, courses.ErrInvalidPayload
	}
	r, _, err := stableCourse(db, studentID, in.StableID)
	if err != nil {
		return nil, err
	}
	var p Progress
	if err := db.Where("student_id = ? AND stable_id = ?", studentID, in.StableID).First(&p).Error; err != nil {
		p = Progress{StudentID: studentID, StableID: in.StableID}
	}
	if in.PositionSec > p.PositionSec {
		p.PositionSec = in.PositionSec
	}
	if in.DurationSec > 0 {
		p.DurationSec = in.DurationSec
	}
	if in.Page > p.Page {
		p.Page = in.Page
	}
	if in.TotalPages > 0 {
		p.TotalPages = in.TotalPages
	}
	if !p.Completed {
		p.Completed = complete(r.Type, p.PositionSec, p.DurationSec, in.Event)
	}
	if p.ID == 0 {
		err = db.Create(&p).Error
	} else {
		err = db.Save(&p).Error
	}
	return &p, err
}

// complete decide finalización: multimedia exige ≥90% de duración;
// el resto se completa al abrirse (evento explícito del reproductor/visor).
func complete(resourceType string, position, duration int, event string) bool {
	switch courses.NormalizeResourceType(resourceType) {
	case courses.ResourceTypeVideo, courses.ResourceTypeAudio:
		return duration > 0 && position*10 >= duration*9
	default:
		switch event {
		case "open", "playing", "pdf_open":
			return true
		}
		return false
	}
}

// Position retorna la última posición reportada (visor/reproductor),
// incluyendo recursos no visibles ni obligatorios.
func Position(db *gorm.DB, studentID uint, stableID string) (*Progress, error) {
	if _, _, err := stableCourse(db, studentID, stableID); err != nil {
		return nil, err
	}
	var p Progress
	if err := db.Where("student_id = ? AND stable_id = ?", studentID, stableID).First(&p).Error; err != nil {
		return nil, courses.ErrNotFound
	}
	return &p, nil
}

// CourseProgress calcula el avance sobre recursos obligatorios visibles,
// actualiza Matricula.Status y emite la insignia al aprobar.
func CourseProgress(db *gorm.DB, studentID, courseID uint) (*CourseProgressResponse, error) {
	if !enroll.IsEnrolled(db, studentID, courseID) {
		return nil, courses.ErrForbidden
	}
	_, tree, err := courses.GetFullTree(db, courseID)
	if err != nil {
		return nil, err
	}
	total, done := 0, 0
	allQuizPassed := true
	positions := map[string]PositionInfo{}
	for _, m := range tree.Modules {
		for _, u := range m.Units {
			for _, r := range u.Resources {
				if !r.IsVisible || !r.IsRequired {
					continue
				}
				total++
				// Los quizzes se completan por aprobación, no por heartbeat.
				if courses.NormalizeResourceType(r.Type) == courses.ResourceTypeQuiz {
					q, qerr := quiz.QuizByResource(db, r.ID)
					if qerr != nil || !quiz.Passed(db, studentID, q.ID, tree.MinRequiredPct) {
						allQuizPassed = false
						continue
					}
					done++
					continue
				}
				var p Progress
				if db.Where("student_id = ? AND stable_id = ?", studentID, r.StableID).First(&p).Error == nil {
					positions[r.StableID] = PositionInfo{PositionSec: p.PositionSec, DurationSec: p.DurationSec, Page: p.Page, Completed: p.Completed}
					if p.Completed {
						done++
					}
				}
			}
		}
	}
	pct := 0
	if total > 0 {
		pct = done * 100 / total
	}
	status := StatusInProgress
	if total > 0 && done == total {
		status = StatusCompleted
	}
	badgeCode := ""
	if status == StatusCompleted && allQuizPassed {
		status = StatusApproved
		if b, err := badges.EnsureBadge(db, studentID, courseID); err == nil {
			badgeCode = b.Code
		}
	}
	_ = db.Model(&courses.Matricula{}).Where("student_id = ? AND course_id = ?", studentID, courseID).Update("status", status).Error
	return &CourseProgressResponse{Total: total, Done: done, Percent: pct, Status: status, Badge: badgeCode, Positions: positions}, nil
}
