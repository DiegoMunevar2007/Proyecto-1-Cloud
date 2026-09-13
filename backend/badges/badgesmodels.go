package badges

import (
	"time"
)

// Badge es una insignia única por (estudiante, curso), verificable sin exponer el correo.
type Badge struct {
	ID        uint       `gorm:"primaryKey" json:"id"`
	CreatedAt time.Time  `json:"created_at"`
	StudentID uint       `gorm:"not null;index:idx_badge_student_course,unique" json:"-"`
	CourseID  uint       `gorm:"not null;index:idx_badge_student_course,unique" json:"course_id"`
	Code      string     `gorm:"uniqueIndex;not null" json:"code"`
	IssuedAt  time.Time  `gorm:"not null" json:"issued_at"`
	RevokedAt *time.Time `json:"revoked_at"`
}

// BadgeResponse es la vista pública de verificación.
type BadgeResponse struct {
	Code     string `json:"code" example:"a1b2c3d4e5f6"`
	CourseID uint   `json:"course_id" example:"1"`
	IssuedAt string `json:"issued_at" example:"2026-09-10T02:00:00Z"`
	Valid    bool   `json:"valid" example:"true"`
}

// MyBadgesResponse lista las insignias del estudiante.
type MyBadgesResponse struct {
	Badges []Badge `json:"badges"`
	Total  int     `json:"total" example:"1"`
}
