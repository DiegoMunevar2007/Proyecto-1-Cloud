package queue

import (
	"time"

	"github.com/hibiken/asynq"
)

// RetryDelayFunc define backoff exponencial para reintentos (DLQ tras MaxRetry).
func RetryDelayFunc() func(n int, err error, task *asynq.Task) time.Duration {
	return func(n int, _ error, _ *asynq.Task) time.Duration {
		// 30s, 2m, 8m aprox.
		switch n {
		case 1:
			return 30 * time.Second
		case 2:
			return 2 * time.Minute
		default:
			return 8 * time.Minute
		}
	}
}
