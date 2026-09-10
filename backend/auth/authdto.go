package auth

// RegisterRequest contiene los datos para registrar un usuario.
// Solo el rol professor requiere autenticación previa de un administrador.
type RegisterRequest struct {
	Username string `json:"username" example:"estudiante1"`
	Email    string `json:"email" example:"estudiante1@ejemplo.com"`
	Password string `json:"password" example:"ClaveSegura123"`
	Role     string `json:"role" example:"student"`
}

// LoginRequest contiene las credenciales de inicio de sesión.
type LoginRequest struct {
	Username string `json:"username" example:"estudiante1"`
	Password string `json:"password" example:"ClaveSegura123"`
}

// LoginResponse retorna el resultado de una autenticación exitosa.
type LoginResponse struct {
	Message  string `json:"message" example:"Autenticación exitosa"`
	Token    string `json:"token" example:"eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9..."`
	Username string `json:"username" example:"estudiante1"`
}

// MeResponse retorna el perfil básico de la sesión actual.
type MeResponse struct {
	Username string `json:"username" example:"estudiante1"`
	Role     string `json:"role" example:"student"`
}

// TokenRequest transporta un token de sesión a revocar.
type TokenRequest struct {
	Token string `json:"token" example:"eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9..."`
}

// VerifyRequest contiene los datos para verificar una cuenta recién registrada.
type VerifyRequest struct {
	Username string `json:"username" example:"estudiante1"`
	Code     string `json:"code" example:"a1b2c3"`
}

// ResendVerificationRequest solicita reenviar el código de verificación.
type ResendVerificationRequest struct {
	Username string `json:"username" example:"estudiante1"`
	Email    string `json:"email" example:"estudiante1@ejemplo.com"`
}

// RecoveryRequest solicita el código de recuperación de contraseña.
type RecoveryRequest struct {
	Username string `json:"username" example:"estudiante1"`
	Email    string `json:"email" example:"estudiante1@ejemplo.com"`
}

// ResetPasswordRequest restablece la contraseña con el código de recuperación.
type ResetPasswordRequest struct {
	Username    string `json:"username" example:"estudiante1"`
	Code        string `json:"code" example:"a1b2c3"`
	NewPassword string `json:"new_password" example:"NuevaClave123"`
}

// UserResponse es la representación pública de un usuario (sin contraseña).
type UserResponse struct {
	ID         uint   `json:"id" example:"1"`
	Username   string `json:"username" example:"estudiante1"`
	Email      string `json:"email" example:"estudiante1@ejemplo.com"`
	Role       string `json:"role" example:"student"`
	Status     string `json:"status" example:"active"`
	IsVerified bool   `json:"is_verified" example:"true"`
	CreatedAt  string `json:"created_at" example:"2026-09-10T02:00:00Z"`
	UpdatedAt  string `json:"updated_at" example:"2026-09-10T02:00:00Z"`
}

// ToUserResponse convierte un UserModel a su representación pública.
func ToUserResponse(u UserModel) UserResponse {
	return UserResponse{
		ID:         u.ID,
		Username:   u.Username,
		Email:      u.Email,
		Role:       u.Role,
		Status:     u.Status,
		IsVerified: u.IsVerified,
		CreatedAt:  u.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		UpdatedAt:  u.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"),
	}
}
