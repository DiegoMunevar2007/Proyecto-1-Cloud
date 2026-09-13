package progress

import (
	"time"
)

// Estados de inscripción/avance (vive en Matricula.Status).
const (
	StatusInProgress = "in_progress"
	StatusCompleted  = "completed"
	StatusApproved   = "approved"
)

// Progress registra el avance por stable_id: sobrevive a nuevas versiones.
type Progress struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	StudentID   uint      `gorm:"not null;index:idx_student_stable,unique" json:"student_id"`
	StableID    string    `gorm:"not null;index:idx_student_stable,unique" json:"stable_id"`
	PositionSec int       `gorm:"not null;default:0" json:"position_sec"`
	DurationSec int       `gorm:"not null;default:0" json:"duration_sec"`
	Completed   bool      `gorm:"not null;default:false" json:"completed"`
}
