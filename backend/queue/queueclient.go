package queue

import (
	"encoding/json"
	"time"

	"github.com/DiegoMunevar2007/Proyecto-1-Cloud.git/utils"
	"github.com/hibiken/asynq"
)

// Client publica trabajos a Redis vía asynq.
type Client struct {
	client *asynq.Client
}

// NewClient crea un cliente asynq contra Redis.
func NewClient() *Client {
	redisOpt := asynq.RedisClientOpt{
		Addr:     utils.GetEnv("REDIS_ADDR", "localhost:6379"),
		Password: utils.GetEnv("REDIS_PASSWORD", ""),
		DB:       0,
	}
	return &Client{
		client: asynq.NewClient(redisOpt),
	}
}

// Close libera conexiones.
func (c *Client) Close() error {
	if c == nil || c.client == nil {
		return nil
	}
	return c.client.Close()
}

// EnqueueTranscode publica un trabajo de transcodificación HLS.
// Es idempotente a nivel de entrega: TaskID = idempotencyKey cuando se provee,
// con reintentos (3) y backoff exponencial; tras agotar reintentos va a DLQ (archived).
func (c *Client) EnqueueTranscode(p TranscodePayload, idempotencyKey string) (string, error) {
	if idempotencyKey == "" {
		idempotencyKey = p.IdempotencyKey
	}
	payload, _ := json.Marshal(p)
	task := asynq.NewTask(TypeMediaTranscode, payload)
	opts := []asynq.Option{
		asynq.MaxRetry(3),
		asynq.Timeout(30 * time.Minute),
		asynq.Queue("media"),
		asynq.Unique(24 * time.Hour),
	}
	if idempotencyKey != "" {
		opts = append(opts, asynq.TaskID(idempotencyKey))
	}
	info, err := c.client.Enqueue(task, opts...)
	if err != nil {
		// Si ya existe (entrega duplicada con misma clave), no es error fatal.
		if err == asynq.ErrTaskIDConflict {
			return idempotencyKey, nil
		}
		return "", err
	}
	return info.ID, nil
}

// EnqueueScan publica un trabajo de escaneo antimalware (fail-closed:
// el transcode solo se encola si el archivo está limpio).
// Misma semántica idempotente que EnqueueTranscode.
func (c *Client) EnqueueScan(p ScanPayload, idempotencyKey string) (string, error) {
	payload, _ := json.Marshal(p)
	task := asynq.NewTask(TypeMediaScan, payload)
	opts := []asynq.Option{
		asynq.MaxRetry(3),
		asynq.Timeout(10 * time.Minute),
		asynq.Queue("media"),
		asynq.Unique(24 * time.Hour),
	}
	if idempotencyKey != "" {
		opts = append(opts, asynq.TaskID(idempotencyKey))
	}
	info, err := c.client.Enqueue(task, opts...)
	if err != nil {
		if err == asynq.ErrTaskIDConflict {
			return idempotencyKey, nil
		}
		return "", err
	}
	return info.ID, nil
}
