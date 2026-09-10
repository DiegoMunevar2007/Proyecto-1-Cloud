package main

import (
	"context"
	"log"
	"os"
	"strconv"

	"github.com/DiegoMunevar2007/Proyecto-1-Cloud.git/courses"
	"github.com/DiegoMunevar2007/Proyecto-1-Cloud.git/queue"
	"github.com/DiegoMunevar2007/Proyecto-1-Cloud.git/storage"
	"github.com/DiegoMunevar2007/Proyecto-1-Cloud.git/utils"
	"github.com/hibiken/asynq"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func main() {
	redisOpt := asynq.RedisClientOpt{
		Addr:     utils.GetEnv("REDIS_ADDR", "localhost:6379"),
		Password: utils.GetEnv("REDIS_PASSWORD", ""),
		DB:       0,
	}
	concurrency, _ := strconv.Atoi(utils.GetEnv("WORKER_CONCURRENCY", "10"))
	if concurrency <= 0 {
		concurrency = 10
	}

	dsn := utils.GetPostgresDSN()
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		log.Fatalf("worker: no se pudo conectar a Postgres: %v", err)
	}
	// AutoMigrate compartido con la API (misma fuente de verdad).
	if err := db.AutoMigrate(
		&courses.Course{},
		&courses.CourseVersion{},
		&courses.Module{},
		&courses.Unit{},
		&courses.Resource{},
		&courses.Matricula{},
	); err != nil {
		log.Fatalf("worker: no se pudo migrar: %v", err)
	}

	store, err := storage.NewClient()
	if err != nil {
		log.Fatalf("worker: no se pudo crear cliente S3: %v", err)
	}

	h := &MediaHandler{DB: db, Store: store}

	srv := asynq.NewServer(redisOpt, asynq.Config{
		Concurrency: concurrency,
		Queues: map[string]int{
			"media":   6,
			"default": 3,
		},
		RetryDelayFunc: queue.RetryDelayFunc(),
		ErrorHandler: asynq.ErrorHandlerFunc(func(ctx context.Context, task *asynq.Task, err error) {
			// Tras agotar reintentos asynq archiva (DLQ) y reporta aquí: emitir alerta observable.
			retried, _ := asynq.GetRetryCount(ctx)
			log.Printf("ALERT worker task=%s archived/failed retry=%d err=%v payload=%s", task.Type(), retried, err, string(task.Payload()))
		}),
	})

	mux := asynq.NewServeMux()
	mux.HandleFunc(queue.TypeMediaTranscode, h.HandleTranscode)

	log.Printf("worker: escuchando colas media/default (concurrency=%d)", concurrency)
	if err := srv.Run(mux); err != nil {
		log.Fatalf("worker: %v", err)
		os.Exit(1)
	}
}
