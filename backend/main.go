package main

import (
	"context"

	"github.com/DiegoMunevar2007/Proyecto-1-Cloud.git/auth"
	"github.com/DiegoMunevar2007/Proyecto-1-Cloud.git/utils"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

var ctx = context.Background()

func SetupRouter(db *gorm.DB, rdb *redis.Client) *gin.Engine {
	router := gin.Default()

	router.GET("/health", func(c *gin.Context) {
		// Ping base de datos PostgreSQL
		dbErr := db.Exec("SELECT 1").Error
		// Ping Redis
		redisErr := rdb.Ping(ctx).Err()

		if dbErr != nil {
			c.JSON(500, gin.H{"status": "error", "message": "Error al conectar con la base de datos PostgreSQL"})
			return
		}
		if redisErr != nil {
			c.JSON(500, gin.H{"status": "error", "message": "Error al conectar con Redis"})
			return
		}
		c.JSON(200, gin.H{"status": "ok"})
	})

	return router
}
func initPostgresDB() *gorm.DB {
	// Inicializar conexión a la base de datos PostgreSQL
	dsn := utils.GetPostgresDSN()
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		panic("No se pudo conectar a la base de datos")
	}
	return db
}

func initRedisClient() *redis.Client {
	// Inicializar cliente Redis
	rdb := redis.NewClient(utils.GetRedisOptions())
	if err := rdb.Ping(ctx).Err(); err != nil {
		panic(err)
	}
	return rdb
}

func main() {
	// Inicializar conexión a la base de datos
	db := initPostgresDB()
	rdb := initRedisClient()

	if err := db.AutoMigrate(&auth.UserModel{}); err != nil {
		panic("No se pudo migrar el esquema de autenticación: " + err.Error())
	}

	// Inicializar el enrutador Gin
	router := SetupRouter(db, rdb)
	auth.SetupAuthRoutes(router, db, rdb)

	// Iniciar el servidor
	if err := router.Run(":8080"); err != nil {
		panic(err)
	}
}
