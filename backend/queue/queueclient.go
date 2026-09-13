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

// RedisOpt construye la conexión asynq a Redis desde env (compartida API/worker).
func RedisOpt() asynq.RedisClientOpt {
	return asynq.RedisClientOpt{
		Addr:     utils.GetEnv("REDIS_ADDR", "localhost:6379"),
		Password: utils.GetEnv("REDIS_PASSWORD", ""),
		DB:       0,
	}
}

// NewClient crea un cliente asynq contra Redis.
func NewClient() *Client {
	return &Client{
		client: asynq.NewClient(RedisOpt()),
	}
}

// Close libera conexiones.
func (c *Client) Close() error {
	if c == nil || c.client == nil {
		return nil
	}
	return c.client.Close()
}

// enqueue publica un trabajo idempotente (TaskID = clave, DLQ tras reintentos).
func (c *Client) enqueue(taskType string, payload interface{}, timeout time.Duration, idempotencyKey string) (string, error) {
	data, _ := json.Marshal(payload)
	task := asynq.NewTask(taskType, data)
	opts := []asynq.Option{
		asynq.MaxRetry(3),
		asynq.Timeout(timeout),
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

// EnqueueTranscode publica un trabajo de transcodificación HLS.
func (c *Client) EnqueueTranscode(p TranscodePayload, idempotencyKey string) (string, error) {
	if idempotencyKey == "" {
		idempotencyKey = p.IdempotencyKey
	}
	return c.enqueue(TypeMediaTranscode, p, 30*time.Minute, idempotencyKey)
}

// EnqueueScan publica un trabajo de escaneo antimalware (fail-closed).
func (c *Client) EnqueueScan(p ScanPayload, idempotencyKey string) (string, error) {
	return c.enqueue(TypeMediaScan, p, 10*time.Minute, idempotencyKey)
}
