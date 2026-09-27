package queue

import (
	"time"

	"github.com/hibiken/asynq"
)

// MediaQueue es la única cola encolada por la aplicación.
const MediaQueue = "media"

// QueueStats es la instantánea de una cola para observabilidad y análisis de
// capacidad. Refleja el estado que asynq mantiene en Redis, no estado en proceso
// del worker.
type QueueStats struct {
	Queue       string `json:"queue"`
	Exists      bool   `json:"exists"`
	Paused      bool   `json:"paused"`
	Pending     int    `json:"pending"`
	Active      int    `json:"active"`
	Scheduled   int    `json:"scheduled"`
	Retry       int    `json:"retry"`
	Archived    int    `json:"archived"`
	Completed   int    `json:"completed"`
	Aggregating int    `json:"aggregating"`
	Size        int    `json:"size"`
	Groups      int    `json:"groups"`
	// MemoryUsageBytes es el uso aproximado de Redis de la cola y sus trabajos.
	MemoryUsage int64 `json:"memory_usage_bytes"`
	// AgeOldestPendingSec es la antigüedad del trabajo pendiente más viejo: la
	// medida directa de si los workers dan abasto a la tasa de entrada.
	AgeOldestPendingSec float64 `json:"age_oldest_pending_sec"`
	// Processed y Failed reinician a medianoche; los *Total son acumulados desde
	// que existe la cola. Se exponen ambos para no leer un contador diario como
	// si fuera histórico.
	Processed      int       `json:"processed_today"`
	Failed         int       `json:"failed_today"`
	ProcessedTotal int       `json:"processed_total"`
	FailedTotal    int       `json:"failed_total"`
	ObservedAt     time.Time `json:"observed_at"`
}

// InspectQueue lee el estado de una cola desde Redis. Crea el Inspector en cada
// llamada porque el consumidor es un endpoint de sondeo, no un cliente de larga
// vida.
//
// Si la cola todavía no existe, devuelve el snapshot a cero con Exists en false:
// en un despliegue recién arrancado, o después de que se haya drenado y borrado,
// ese es el estado normal y no un error. La ausencia se distingue consultando el
// listado de colas del propio Inspector, porque el error que devuelve
// GetQueueInfo cuando la clave no existe no es el centinela ErrQueueNotFound sino
// el error de Redis, y comparar contra el centinela dejaría pasar el caso más
// frecuente como si fuera una falla.
func InspectQueue(name string) (*QueueStats, error) {
	inspector := asynq.NewInspector(RedisOpt())
	defer inspector.Close()

	info, err := inspector.GetQueueInfo(name)
	if err != nil {
		vacia := QueueStats{Queue: name, Exists: false, ObservedAt: time.Now().UTC()}
		if !existe(inspector, name) {
			return &vacia, nil
		}
		return nil, err
	}
	return &QueueStats{
		Queue:               name,
		Exists:              true,
		Paused:              info.Paused,
		Pending:             info.Pending,
		Active:              info.Active,
		Scheduled:           info.Scheduled,
		Retry:               info.Retry,
		Archived:            info.Archived,
		Completed:           info.Completed,
		Aggregating:         info.Aggregating,
		Size:                info.Size,
		Groups:              info.Groups,
		MemoryUsage:         info.MemoryUsage,
		AgeOldestPendingSec: info.Latency.Seconds(),
		Processed:           info.Processed,
		Failed:              info.Failed,
		ProcessedTotal:      info.ProcessedTotal,
		FailedTotal:         info.FailedTotal,
		ObservedAt:          time.Now().UTC(),
	}, nil
}

// existe informa si la cola está presente en Redis. Un error al listar se
// interpreta como "no existe", que es la respuesta conservadora: devolver ceros
// es mejor que devolver 500 en un endpoint de sondeo.
func existe(inspector *asynq.Inspector, name string) bool {
	colas, err := inspector.Queues()
	if err != nil {
		return false
	}
	for _, c := range colas {
		if c == name {
			return true
		}
	}
	return false
}

// InspectMediaQueue inspecciona la cola de media, la única que usa la aplicación.
func InspectMediaQueue() (*QueueStats, error) {
	return InspectQueue(MediaQueue)
}
