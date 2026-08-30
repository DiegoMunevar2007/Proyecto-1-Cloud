package utils

import (
	"os"

	"github.com/redis/go-redis/v9"
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

	return "host=" + host + " user=" + user + " password=" + password + " dbname=" + dbname + " port=" + port + " sslmode=disable"
}

func GetJWTSecret() string {
	// Obtiene el secreto para firmar los JWT desde la variable de entorno JWT_SECRET.
	// Si no está definida, usa un valor por defecto solo para desarrollo.
	return GetEnv("JWT_SECRET", "dev-jwt-secret-change-in-prod")
}

func GetRedisOptions() *redis.Options {
	// Construye las opciones de conexión para Redis a partir de variables de entorno.
	addr := GetEnv("REDIS_ADDR", "localhost:6379")
	password := GetEnv("REDIS_PASSWORD", "")
	db := 0 // use default DB

	return &redis.Options{
		Addr:     addr,
		Password: password,
		DB:       db,
	}
}
