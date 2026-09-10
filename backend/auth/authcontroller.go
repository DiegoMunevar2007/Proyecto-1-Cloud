package auth

import (
	"errors"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

// Handler agrupa los endpoints de autenticación con sus dependencias.
type Handler struct {
	DB  *gorm.DB
	RDB *redis.Client
}

// NewHandler crea un Handler de autenticación.
func NewHandler(db *gorm.DB, rdb *redis.Client) *Handler {
	return &Handler{DB: db, RDB: rdb}
}

// SetupAuthRoutes registra las rutas de autenticación en el enrutador Gin.
func SetupAuthRoutes(router *gin.Engine, db *gorm.DB, rdb *redis.Client) {
	h := NewHandler(db, rdb)
	authGroup := router.Group("/auth")
	{
		authGroup.POST("/register", h.Register)
		authGroup.POST("/login", h.Login)
		authGroup.POST("/logout", RequireAuth(rdb), h.Logout)
		authGroup.GET("/me", RequireAuth(rdb), h.Me)
		authGroup.POST("/revoke-session", h.RevokeSession)
		authGroup.GET("/verify", h.Verify)
		authGroup.POST("/resend-verification", h.ResendVerification)
		authGroup.GET("/send-recovery-code", h.SendRecoveryCode)
		authGroup.POST("/reset-password", h.ResetPassword)
	}
}

// Register registra un nuevo usuario.
// Solo la creación de profesores requiere autenticación de administrador.
//
//	@Summary		Registrar usuario
//	@Description	Registra un estudiante o administrador de forma pública. Crear un profesor requiere el token de un administrador.
//	@Tags			Autenticación
//	@Produce		json
//	@Param			request	body		RegisterRequest	true	"Datos de registro"
//	@Param			Authorization	header	string	false	"Bearer <token> (obligatorio solo para rol professor)"
//	@Success		201		{object}	utils.MessageResponse	"Usuario registrado"
//	@Failure		400		{object}	utils.ErrorResponse	"Solicitud inválida o rol inválido"
//	@Failure		401		{object}	utils.ErrorResponse	"Token de administrador inválido"
//	@Failure		403		{object}	utils.ErrorResponse	"Se requiere administrador para crear profesores"
//	@Failure		409		{object}	utils.ErrorResponse	"Usuario o correo ya en uso"
//	@Router			/auth/register [post]
func (h *Handler) Register(c *gin.Context) {
	var request RegisterRequest
	if err := c.ShouldBind(&request); err != nil {
		c.JSON(400, gin.H{"error": "Solicitud inválida"})
		return
	}
	if request.Username == "" || request.Email == "" || request.Password == "" {
		c.JSON(400, gin.H{"error": "Se requieren username, email y password"})
		return
	}

	requestedRole := NormalizeRole(request.Role) // vacío -> student
	if !IsValidRole(requestedRole) {
		c.JSON(400, gin.H{"error": "Rol inválido. Roles permitidos: student, professor, admin"})
		return
	}

	// Solo professor requiere autorización de admin; student y admin son públicos (curso).
	if requestedRole == RoleProfessor {
		token := bearerToken(c)
		if token == "" {
			c.JSON(403, gin.H{"error": "Se requiere autenticación de administrador para crear un profesor"})
			return
		}
		_, role, err := ResolveSessionTokenWithRole(token, h.RDB)
		if err != nil {
			c.JSON(401, gin.H{"error": "Token de sesión inválido o expirado"})
			return
		}
		if NormalizeRole(role) != RoleAdmin {
			c.JSON(403, gin.H{"error": "Solo un administrador puede crear profesores"})
			return
		}
	}

	message, status := RegisterUser(request.Username, request.Email, request.Password, requestedRole, h.DB, h.RDB)
	c.JSON(status, gin.H{"message": message})
}

// Login autentica un usuario y crea una sesión revocable.
//
//	@Summary		Iniciar sesión
//	@Description	Verifica las credenciales y retorna un token JWT de 24 horas para el header Authorization.
//	@Tags			Autenticación
//	@Produce		json
//	@Param			request	body		LoginRequest	true	"Credenciales"
//	@Success		200		{object}	LoginResponse	"Autenticación exitosa"
//	@Failure		400		{object}	utils.ErrorResponse	"Solicitud inválida"
//	@Failure		401		{object}	utils.ErrorResponse	"Credenciales incorrectas"
//	@Failure		403		{object}	utils.ErrorResponse	"Cuenta desactivada o bloqueada"
//	@Router			/auth/login [post]
func (h *Handler) Login(c *gin.Context) {
	var request LoginRequest
	if err := c.ShouldBind(&request); err != nil {
		c.JSON(400, gin.H{"error": "Solicitud inválida"})
		return
	}
	ok, reason := AuthenticateUserDetailed(request.Username, request.Password, h.DB)
	if !ok {
		if reason == StatusInactive {
			c.JSON(403, gin.H{"error": "Cuenta desactivada. Contacte al administrador"})
			return
		}
		if reason == StatusBlocked {
			c.JSON(403, gin.H{"error": "Cuenta bloqueada. Contacte al administrador"})
			return
		}
		c.JSON(401, gin.H{"error": "Nombre de usuario o contraseña incorrectos"})
		return
	}
	userID, err := GetUserID(request.Username, h.DB)
	if err != nil {
		c.JSON(401, gin.H{"error": "Nombre de usuario o contraseña incorrectos"})
		return
	}
	token, err := CreateSession(userID, h.DB, h.RDB)
	if err != nil {
		// Si la cuenta no está activa, CreateSession puede fallar también
		if strings.Contains(err.Error(), "no activa") {
			c.JSON(403, gin.H{"error": "Cuenta no activa. Contacte al administrador"})
			return
		}
		c.JSON(500, gin.H{"error": "No fue posible iniciar sesión: " + err.Error()})
		return
	}
	c.JSON(200, gin.H{"message": "Autenticación exitosa", "token": token, "username": request.Username})
}

// Logout cierra la sesión actual invalidando su token.
//
//	@Summary		Cerrar sesión
//	@Description	Invalida el token de la petición actual. Es idempotente.
//	@Tags			Autenticación
//	@Produce		json
//	@Param			Authorization	header		string	true	"Bearer <token>"
//	@Security		BearerAuth
//	@Success		200				{object}	utils.MessageResponse	"Sesión cerrada"
//	@Failure		401				{object}	utils.ErrorResponse	"Token inválido o ausente"
//	@Router			/auth/logout [post]
func (h *Handler) Logout(c *gin.Context) {
	// El logout invalida el token actual; ignoramos el error si ya
	// no existe (p. ej. si se llama dos veces con el mismo token).
	_ = DeleteSession(c.GetString("session_token"), h.RDB)
	c.JSON(200, gin.H{"message": "Sesión cerrada"})
}

// Me retorna el perfil básico de la sesión actual.
//
//	@Summary		Perfil de la sesión
//	@Description	Retorna el username y rol asociados al token de la petición.
//	@Tags			Autenticación
//	@Produce		json
//	@Param			Authorization	header		string	true	"Bearer <token>"
//	@Security		BearerAuth
//	@Success		200				{object}	MeResponse	"Perfil de la sesión"
//	@Failure		401				{object}	utils.ErrorResponse	"Token inválido o ausente"
//	@Router			/auth/me [get]
func (h *Handler) Me(c *gin.Context) {
	c.JSON(200, gin.H{"username": c.GetString("username"), "role": c.GetString("role")})
}

// RevokeSession revoca un token de sesión arbitrario (uso en pruebas).
//
//	@Summary		Revocar una sesión
//	@Description	Elimina de Redis el token indicado en el cuerpo o en el header Authorization. No requiere autenticación (uso en pruebas).
//	@Tags			Autenticación
//	@Produce		json
//	@Param			request	body		TokenRequest	false	"Token a revocar (o header Authorization)"
//	@Success		200		{object}	utils.MessageResponse	"Sesión revocada"
//	@Failure		400		{object}	utils.ErrorResponse	"Falta el token a revocar"
//	@Failure		500		{object}	utils.ErrorResponse	"No se pudo revocar la sesión"
//	@Router			/auth/revoke-session [post]
func (h *Handler) RevokeSession(c *gin.Context) {
	var request TokenRequest
	// ShouldBind no falla si el body está vacío, por eso se tolera error.
	_ = c.ShouldBind(&request)
	token := strings.TrimSpace(request.Token)
	if token == "" {
		// Fallback: intentar extraer del header Authorization
		token = bearerToken(c)
	}
	if token == "" {
		c.JSON(400, gin.H{"error": "Se requiere el token a revocar (campo 'token' o header Authorization)"})
		return
	}
	if err := RevokeSession(token, h.RDB); err != nil {
		c.JSON(500, gin.H{"error": "No se pudo revocar la sesión: " + err.Error()})
		return
	}
	c.JSON(200, gin.H{"message": "Sesión revocada correctamente"})
}

// Verify verifica la cuenta con el código enviado al correo.
//
//	@Summary		Verificar cuenta
//	@Description	Activa la cuenta recién registrada con el código de verificación de 6 caracteres.
//	@Tags			Autenticación
//	@Produce		json
//	@Param			username	query		string	true	"Nombre de usuario"
//	@Param			code		query		string	true	"Código de verificación"
//	@Success		200			{object}	utils.MessageResponse	"Usuario verificado"
//	@Failure		400			{object}	utils.ErrorResponse	"Solicitud inválida o código incorrecto"
//	@Router			/auth/verify [get]
func (h *Handler) Verify(c *gin.Context) {
	var request VerifyRequest
	// ShouldBind enlaza query params en peticiones GET.
	if err := c.ShouldBind(&request); err != nil {
		c.JSON(400, gin.H{"error": "Solicitud inválida"})
		return
	}
	// Compatibilidad: el query param histórico se llama "code" y el JSON "code".
	if request.Code == "" {
		request.Code = c.Query("code")
	}
	if request.Username == "" {
		request.Username = c.Query("username")
	}
	err := VerifyUser(request.Username, request.Code, h.DB, h.RDB)
	if err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"message": "Usuario verificado exitosamente"})
}

// ResendVerification reenvía el código de verificación al correo.
//
//	@Summary		Reenviar verificación
//	@Description	Genera un nuevo código de verificación y lo envía al correo indicado. Solo para cuentas pendientes de verificación.
//	@Tags			Autenticación
//	@Produce		json
//	@Param			request	body		ResendVerificationRequest	true	"Usuario y correo"
//	@Success		200		{object}	utils.MessageResponse	"Código reenviado"
//	@Failure		400		{object}	utils.ErrorResponse	"Solicitud inválida"
//	@Failure		404		{object}	utils.ErrorResponse	"Usuario no encontrado"
//	@Failure		409		{object}	utils.ErrorResponse	"Usuario ya verificado"
//	@Failure		500		{object}	utils.ErrorResponse	"No se pudo enviar el correo"
//	@Router			/auth/resend-verification [post]
func (h *Handler) ResendVerification(c *gin.Context) {
	var request ResendVerificationRequest
	if err := c.ShouldBind(&request); err != nil {
		c.JSON(400, gin.H{"error": "Solicitud inválida"})
		return
	}
	if err := SendVerificationCode(request.Username, request.Email, h.DB, h.RDB); err != nil {
		switch {
		case errors.Is(err, ErrUserNotFound):
			c.JSON(404, gin.H{"error": err.Error()})
		case errors.Is(err, ErrAlreadyVerified):
			c.JSON(409, gin.H{"error": err.Error()})
		default:
			c.JSON(500, gin.H{"error": err.Error()})
		}
		return
	}
	c.JSON(200, gin.H{"message": "Código de verificación reenviado"})
}

// SendRecoveryCode envía el código de recuperación de contraseña.
//
//	@Summary		Solicitar recuperación
//	@Description	Genera un código de recuperación válido por 10 minutos y lo envía al correo del usuario.
//	@Tags			Autenticación
//	@Produce		json
//	@Param			username	query		string	true	"Nombre de usuario"
//	@Param			email		query		string	false	"Correo (informativo)"
//	@Success		200			{object}	utils.MessageResponse	"Código enviado"
//	@Failure		400			{object}	utils.ErrorResponse	"Solicitud inválida"
//	@Router			/auth/send-recovery-code [get]
func (h *Handler) SendRecoveryCode(c *gin.Context) {
	var request RecoveryRequest
	if err := c.ShouldBind(&request); err != nil {
		c.JSON(400, gin.H{"error": "Solicitud inválida"})
		return
	}
	if request.Username == "" {
		request.Username = c.Query("username")
	}
	message := RecoverPassword(request.Username, h.DB, h.RDB)
	c.JSON(200, gin.H{"message": message})
}

// ResetPassword restablece la contraseña con el código de recuperación.
//
//	@Summary		Restablecer contraseña
//	@Description	Valida el código de recuperación y guarda la nueva contraseña (hasheada con bcrypt).
//	@Tags			Autenticación
//	@Produce		json
//	@Param			request	body		ResetPasswordRequest	true	"Usuario, código y nueva contraseña"
//	@Success		200		{object}	utils.MessageResponse	"Contraseña restablecida"
//	@Failure		400		{object}	utils.ErrorResponse	"Solicitud inválida"
//	@Router			/auth/reset-password [post]
func (h *Handler) ResetPassword(c *gin.Context) {
	var request ResetPasswordRequest
	if err := c.ShouldBind(&request); err != nil {
		c.JSON(400, gin.H{"error": "Solicitud inválida"})
		return
	}
	message := ResetPassword(request.Username, request.Code, request.NewPassword, h.DB, h.RDB)
	c.JSON(200, gin.H{"message": message})
}
