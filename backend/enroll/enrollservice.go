package enroll

import (
	"errors"
	"time"

	"github.com/DiegoMunevar2007/Proyecto-1-Cloud.git/courses"
	"gorm.io/gorm"
)

// ErrNotPublished indica que el curso no admite inscripción (solo publicados).
var ErrNotPublished = errors.New("curso no publicado")

// Enroll inscribe al estudiante (crea o reactiva). Idempotente: re-POST no duplica.
// El progreso y las notas viven en tablas separadas y se conservan siempre.
func Enroll(db *gorm.DB, studentID, courseID uint) (*courses.Matricula, bool, error) {
	var c courses.Course
	if err := db.First(&c, courseID).Error; err != nil {
		return nil, false, courses.ErrNotFound
	}
	if c.Status != courses.CourseStatusPublished {
		return nil, false, ErrNotPublished
	}
	var m courses.Matricula
	if err := db.Where("student_id = ? AND course_id = ?", studentID, courseID).First(&m).Error; err == nil {
		if m.Inscrito {
			return &m, false, nil
		}
		m.Inscrito = true
		return &m, false, db.Save(&m).Error
	}
	m = courses.Matricula{StudentID: studentID, CourseID: courseID, Inscrito: true, FechaInscripcion: time.Now()}
	if err := db.Create(&m).Error; err != nil {
		return nil, false, err
	}
	return &m, true, nil
}

// Withdraw marca retiro sin borrar progreso ni resultados.
func Withdraw(db *gorm.DB, studentID, courseID uint) error {
	var m courses.Matricula
	if err := db.Where("student_id = ? AND course_id = ?", studentID, courseID).First(&m).Error; err != nil {
		return courses.ErrNotFound
	}
	m.Inscrito = false
	return db.Save(&m).Error
}

// IsEnrolled indica si el estudiante está inscrito y activo.
func IsEnrolled(db *gorm.DB, studentID, courseID uint) bool {
	var m courses.Matricula
	if err := db.Where("student_id = ? AND course_id = ?", studentID, courseID).First(&m).Error; err != nil {
		return false
	}
	return m.Inscrito
}

// Mine lista las inscripciones del estudiante.
func Mine(db *gorm.DB, studentID uint) ([]courses.Matricula, error) {
	var out []courses.Matricula
	err := db.Where("student_id = ?", studentID).Order("created_at desc").Find(&out).Error
	return out, err
}

// MineByCourse lista inscritos del curso (autor o admin; el controlador autoriza).
func MineByCourse(db *gorm.DB, courseID uint) ([]courses.Matricula, error) {
	var out []courses.Matricula
	err := db.Where("course_id = ? AND inscrito = ?", courseID, true).Order("fecha_inscripcion asc").Find(&out).Error
	return out, err
}
