package utils

// ErrorResponse es el formato uniforme de error de la API.
type ErrorResponse struct {
	Error string `json:"error" example:"Se requieren username, email y password"`
}

// MessageResponse es una respuesta simple con mensaje informativo.
type MessageResponse struct {
	Message string `json:"message" example:"Operación exitosa"`
}
