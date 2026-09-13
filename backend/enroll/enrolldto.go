package enroll

import "github.com/DiegoMunevar2007/Proyecto-1-Cloud.git/courses"

// EnrollRequest inscribe al estudiante en un curso publicado.
type EnrollRequest struct {
	CourseID uint `json:"course_id" example:"1"`
}

// EnrollmentsResponse lista las inscripciones del estudiante.
type EnrollmentsResponse struct {
	Enrollments []courses.Matricula `json:"enrollments"`
	Total       int64               `json:"total" example:"3"`
}

// MatriculaEnvelope envuelve una inscripción en la respuesta.
type MatriculaEnvelope struct {
	Enrollment courses.Matricula `json:"enrollment"`
}
