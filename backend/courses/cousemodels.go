package courses

import (
	"github.com/DiegoMunevar2007/Proyecto-1-Cloud.git/auth"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

type Course struct {
	gorm.Model
	Name         string         `gorm:"not null"`
	Description  string         `gorm:"not null"`
	Instructor   auth.UserModel `gorm:"not null"`
	InstructorID string         `gorm:"not null"` // Llave FK a UserModel
}

type Module struct {
	gorm.Model
	Title       string `gorm:"not null"`
	Description string `gorm:"not null"`
	Course      Course `gorm:"not null"`
	CourseID    uint   `gorm:"not null"` // Llave FK a CourseModel
}

type Unit struct {
	gorm.Model
	Title    string `gorm:"not null"`
	Content  string `gorm:"not null"`
	Module   Module `gorm:"not null"`
	ModuleID uint   `gorm:"not null"` // Llave FK a ModuleModel
}

type Resource struct {
	gorm.Model
	Name        string `gorm:"not null"`
	Description string `gorm:"not null"`
	URL         string `gorm:"not null"`
	Unit        Unit   `gorm:"not null"`
	UnitID      uint   `gorm:"not null"`               // Llave FK a UnitModel
	EsVisible   bool   `gorm:"not null;default:false"` // Indica si el recurso es visible para los estudiantes

}

type Matricula struct {
	gorm.Model
	Student          auth.UserModel `gorm:"not null"`
	StudentID        string         `gorm:"not null"` // Llave FK a UserModel
	Course           Course         `gorm:"not null"`
	CourseID         uint           `gorm:"not null"`              // Llave FK a CourseModel
	Inscrito         bool           `gorm:"not null;default:true"` // Indica si el estudiante está inscrito en el curso (caso de retiro)
	FechaInscripcion datatypes.Date `gorm:"not null"`
}
