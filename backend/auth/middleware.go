package auth

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

func RequireAuth(rdb *redis.Client) gin.HandlerFunc {
	/*
		Middleware que autentica una solicitud a partir del JWT enviado
		en el header "Authorization: Bearer <token>" (obtenido en
		/auth/login). Valida firma, expiración y existencia en Redis.
		Si el token es válido, guarda username, role y user_id en el contexto;
		si no, responde 401. Usa únicamente Redis, sin consulta a la DB.
	*/
	return func(c *gin.Context) {
		token := bearerToken(c)
		if token == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Se requiere un token de sesión"})
			c.Abort()
			return
		}
		username, role, err := ResolveSessionTokenWithRole(token, rdb)
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Token de sesión inválido o expirado"})
			c.Abort()
			return
		}
		c.Set("username", username)
		c.Set("role", role)
		c.Set("session_token", token)
		// user_id se puede extraer del claim si se necesita, pero el JWT ya fue validado;
		// para evitar parsing extra, se deja solo username/role. Si se necesita, parsear claims aquí.
		c.Next()
	}
}

func RequireRole(rdb *redis.Client, allowedRoles ...string) gin.HandlerFunc {
	/*
		Middleware que autentica una solicitud y verifica que el rol del usuario
		esté entre los permitidos. Usa únicamente Redis, sin consulta a la DB.
		Si el token es válido y el rol permitido, guarda username, role y user_id en el contexto;
		si no, responde 401 o 403 según corresponda.
	*/
	allowed := make(map[string]bool, len(allowedRoles))
	for _, r := range allowedRoles {
		allowed[NormalizeRole(r)] = true
	}
	return func(c *gin.Context) {
		token := bearerToken(c)
		if token == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Se requiere un token de sesión"})
			c.Abort()
			return
		}
		username, role, err := ResolveSessionTokenWithRole(token, rdb)
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Token de sesión inválido o expirado"})
			c.Abort()
			return
		}
		normalizedRole := NormalizeRole(role)
		if !allowed[normalizedRole] {
			c.JSON(http.StatusForbidden, gin.H{"error": "Permiso denegado. Se requiere uno de los roles: " + strings.Join(allowedRoles, ", ")})
			c.Abort()
			return
		}
		c.Set("username", username)
		c.Set("role", normalizedRole)
		c.Set("session_token", token)
		c.Next()
	}
}

func bearerToken(c *gin.Context) string {
	/*
		bearerToken extrae el token del header "Authorization: Bearer <token>".
		Devuelve una cadena vacía si el header no tiene ese formato.
	*/
	header := c.GetHeader("Authorization")
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(header, prefix))
}

// BearerToken exportado para reutilizar en controladores (ej. register con rol profesor).
func BearerToken(c *gin.Context) string {
	return bearerToken(c)
}
