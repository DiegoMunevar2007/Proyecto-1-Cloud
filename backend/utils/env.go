package utils

import (
	"os"
)

// GetEnv obtiene el valor de una variable de entorno. Si no está definida, devuelve el valor por defecto.
func GetEnv(key, defaultValue string) string {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	return value
}

func GetPostgresDSN() string {
	// Construye el DSN (Data Source Name) para la conexión a PostgreSQL a partir de variables de entorno.
	host := GetEnv("POSTGRES_HOST", "localhost")
	port := GetEnv("POSTGRES_PORT", "5432")
	user := GetEnv("POSTGRES_USER", "postgres")
	password := GetEnv("POSTGRES_PASSWORD", "postgres")
	dbname := GetEnv("POSTGRES_DB", "mydb")
	sslmode := GetEnv("POSTGRES_SSLMODE", "disable")

	return "host=" + host + " user=" + user + " password=" + password + " dbname=" + dbname + " port=" + port + " sslmode=" + sslmode
}

func GetJWTSecret() string {
	// Obtiene el secreto para firmar los JWT desde la variable de entorno JWT_SECRET.
	// Si no está definida, usa un valor por defecto solo para desarrollo.
	return GetEnv("JWT_SECRET", "dev-jwt-secret-change-in-prod")
}

// RedisAddr y RedisPassword son la conexión a Redis que comparten go-redis (API)
// y asynq (API y worker): se leen una sola vez para que ambos no puedan divergir.
func RedisAddr() string {
	return GetEnv("REDIS_ADDR", "localhost:6379")
}

func RedisPassword() string {
	return GetEnv("REDIS_PASSWORD", "")
}

func GetSMTPConfig() (host, port, user string) {
	// Obtiene la configuración del servidor SMTP desde variables de entorno.
	host = GetEnv("SMTP_HOST", "mailpit")
	port = GetEnv("SMTP_PORT", "1025")
	user = GetEnv("SMTP_USER", "prueba@test.com")
	return host, port, user
}
