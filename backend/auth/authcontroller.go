package auth

import (
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

func SetupAuthRoutes(router *gin.Engine, db *gorm.DB, rdb *redis.Client) {
	/*
		Configura las rutas de autenticación en el enrutador Gin.
		Se definen las rutas para el registro y la autenticación de usuarios.
	*/
	authGroup := router.Group("/auth")
	{
		authGroup.POST("/register", func(c *gin.Context) {
			var request struct {
				Username string `form:"username" json:"username"`
				Email    string `form:"email" json:"email"`
				Password string `form:"password" json:"password"`
				Role     string `form:"role" json:"role"`
			}
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
				_, role, err := ResolveSessionTokenWithRole(token, rdb)
				if err != nil {
					c.JSON(401, gin.H{"error": "Token de sesión inválido o expirado"})
					return
				}
				if NormalizeRole(role) != RoleAdmin {
					c.JSON(403, gin.H{"error": "Solo un administrador puede crear profesores"})
					return
				}
			}

			message, status := RegisterUser(request.Username, request.Email, request.Password, requestedRole, db, rdb)
			c.JSON(status, gin.H{"message": message})
		})

		authGroup.POST("/login", func(c *gin.Context) {
			var request struct {
				Username string `form:"username" json:"username"`
				Password string `form:"password" json:"password"`
			}
			if err := c.ShouldBind(&request); err != nil {
				c.JSON(400, gin.H{"error": "Solicitud inválida"})
				return
			}
			ok, reason := AuthenticateUserDetailed(request.Username, request.Password, db)
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
			userID, err := GetUserID(request.Username, db)
			if err != nil {
				c.JSON(401, gin.H{"error": "Nombre de usuario o contraseña incorrectos"})
				return
			}
			token, err := CreateSession(userID, db, rdb)
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
		})

		authGroup.POST("/logout", RequireAuth(rdb), func(c *gin.Context) {
			// El logout invalida el token actual; ignoramos el error si ya
			// no existe (p. ej. si se llama dos veces con el mismo token).
			_ = DeleteSession(c.GetString("session_token"), rdb)
			c.JSON(200, gin.H{"message": "Sesión cerrada"})
		})

		authGroup.GET("/me", RequireAuth(rdb), func(c *gin.Context) {
			c.JSON(200, gin.H{"username": c.GetString("username"), "role": c.GetString("role")})
		})

		// Recibe el token por body JSON/form o header Authorization.
		// Usa POST y elimina el JWT de Redis. Sin RequireAuth para pruebas.
		authGroup.POST("/revoke-session", func(c *gin.Context) {
			var request struct {
				Token string `json:"token" form:"token"`
			}
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
			if err := RevokeSession(token, rdb); err != nil {
				c.JSON(500, gin.H{"error": "No se pudo revocar la sesión: " + err.Error()})
				return
			}
			c.JSON(200, gin.H{"message": "Sesión revocada correctamente"})
		})

		authGroup.GET("/verify", func(c *gin.Context) {
			var request struct {
				Username         string `form:"username" json:"username"`
				VerificationCode string `form:"code" json:"code"`
			}
			if err := c.ShouldBind(&request); err != nil {
				c.JSON(400, gin.H{"error": "Solicitud inválida"})
				return
			}
			err := VerifyUser(request.Username, request.VerificationCode, db, rdb)
			if err != nil {
				c.JSON(400, gin.H{"error": err.Error()})
				return
			}
			c.JSON(200, gin.H{"message": "Usuario verificado exitosamente"})
		})

		authGroup.POST("/resend-verification", func(c *gin.Context) {
			var request struct {
				Username string `form:"username" json:"username"`
				Email    string `form:"email" json:"email"`
			}
			if err := c.ShouldBind(&request); err != nil {
				c.JSON(400, gin.H{"error": "Solicitud inválida"})
				return
			}
			message := SendVerificationCode(request.Username, request.Email, rdb)
			c.JSON(200, gin.H{"message": message})
		})

		authGroup.GET("/send-recovery-code", func(c *gin.Context) {
			var request struct {
				Username string `form:"username" json:"username"`
				Email    string `form:"email" json:"email"`
			}
			if err := c.ShouldBind(&request); err != nil {
				c.JSON(400, gin.H{"error": "Solicitud inválida"})
				return
			}
			message := RecoverPassword(request.Username, db, rdb)
			c.JSON(200, gin.H{"message": message})
		})

		authGroup.POST("/reset-password", func(c *gin.Context) {
			var request struct {
				Username         string `form:"username" json:"username"`
				VerificationCode string `form:"code" json:"code"`
				NewPassword      string `form:"new_password" json:"new_password"`
			}
			if err := c.ShouldBind(&request); err != nil {
				c.JSON(400, gin.H{"error": "Solicitud inválida"})
				return
			}
			message := ResetPassword(request.Username, request.VerificationCode, request.NewPassword, db, rdb)
			c.JSON(200, gin.H{"message": message})
		})
	}
}
