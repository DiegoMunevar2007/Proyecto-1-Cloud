package quiz

import (
	"time"
)

// Estados de intento.
const (
	AttemptDraft     = "draft"
	AttemptSubmitted = "submitted"
	AttemptExpired   = "expired"
)

// Políticas de retroalimentación.
const (
	FeedbackOnSubmit = "on_submit"
	FeedbackNever    = "never"
)

// Quiz cuelga de un Resource tipo quiz y define las reglas del intento.
type Quiz struct {
	ID              uint      `gorm:"primaryKey" json:"id"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
	ResourceID      uint      `gorm:"uniqueIndex;not null" json:"resource_id"`
	AttemptsAllowed int       `gorm:"not null;default:3" json:"attempts_allowed"`
	TimeLimitSec    *int      `json:"time_limit_sec"`
	FeedbackPolicy  string    `gorm:"not null;default:'on_submit'" json:"feedback_policy"`
}

// Question es una pregunta de selección única. CorrectIndex nunca se expone al estudiante.
type Question struct {
	ID           uint      `gorm:"primaryKey" json:"id"`
	CreatedAt    time.Time `json:"created_at"`
	QuizID       uint      `gorm:"not null;index" json:"quiz_id"`
	Position     int       `gorm:"not null;index" json:"position"`
	Prompt       string    `gorm:"type:text;not null" json:"prompt"`
	Choices      string    `gorm:"type:text;not null" json:"choices"`
	CorrectIndex int       `gorm:"not null" json:"correct_index"`
}

// Attempt es un intento con snapshot de preguntas al iniciar.
// La calificación se calcula en servidor al enviar; el envío es idempotente.
type Attempt struct {
	ID             uint       `gorm:"primaryKey" json:"id"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	QuizID         uint       `gorm:"not null;index" json:"quiz_id"`
	StudentID      uint       `gorm:"not null;index" json:"student_id"`
	Snapshot       string     `gorm:"type:text;not null" json:"-"`
	Answers        string     `gorm:"type:text;not null;default:'{}'" json:"-"`
	Status         string     `gorm:"not null;default:'draft';index" json:"status"`
	Score          *int       `json:"score"`
	StartedAt      time.Time  `gorm:"not null" json:"started_at"`
	SubmittedAt    *time.Time `json:"submitted_at"`
	ExpiresAt      *time.Time `json:"expires_at"`
	IdempotencyKey string     `gorm:"uniqueIndex;not null" json:"-"`
}

// SnapshotQuestion congela pregunta + clave al iniciar el intento (solo servidor).
type SnapshotQuestion struct {
	Prompt  string   `json:"prompt"`
	Choices []string `json:"choices"`
	Correct int      `json:"correct"`
}
