package badges

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"time"

	"github.com/DiegoMunevar2007/Proyecto-1-Cloud.git/courses"
	"gorm.io/gorm"
)

// ErrNotFound indica insignia inexistente o revocada.
var ErrNotFound = errors.New("insignia no encontrada")

// genCode genera un código público de 12 hex chars (sin datos del estudiante).
func genCode() string {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "000000000000"
	}
	return hex.EncodeToString(b[:])
}

// EnsureBadge emite la insignia una sola vez (unique estudiante+curso).
// Idempotente ante doble entrega: retoma la existente.
func EnsureBadge(db *gorm.DB, studentID, courseID uint) (*Badge, error) {
	var b Badge
	if err := db.Where("student_id = ? AND course_id = ?", studentID, courseID).First(&b).Error; err == nil {
		return &b, nil
	}
	b = Badge{StudentID: studentID, CourseID: courseID, Code: genCode(), IssuedAt: time.Now()}
	if err := db.Create(&b).Error; err != nil {
		// Carrera resolvida por el índice único: releer.
		if db.Where("student_id = ? AND course_id = ?", studentID, courseID).First(&b).Error == nil {
			return &b, nil
		}
		return nil, err
	}
	return &b, nil
}

// Mine lista las insignias del estudiante.
func Mine(db *gorm.DB, studentID uint) ([]Badge, error) {
	var out []Badge
	err := db.Where("student_id = ?", studentID).Order("created_at desc").Find(&out).Error
	return out, err
}

// Verify resuelve el código público. Revocadas responden no encontradas.
func Verify(db *gorm.DB, code string) (*Badge, error) {
	var b Badge
	if err := db.Where("code = ?", code).First(&b).Error; err != nil {
		return nil, ErrNotFound
	}
	if b.RevokedAt != nil {
		return nil, ErrNotFound
	}
	return &b, nil
}

// Revoke invalida la insignia (auditable por el llamador).
func Revoke(db *gorm.DB, id uint) error {
	var b Badge
	if err := db.First(&b, id).Error; err != nil {
		return courses.ErrNotFound
	}
	now := time.Now()
	b.RevokedAt = &now
	return db.Save(&b).Error
}
