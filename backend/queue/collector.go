package queue

import (
	"github.com/prometheus/client_golang/prometheus"
)

// StatsProvider es la fuente del estado de la cola. Es una función y no una
// interfaz porque solo hay una implementación real (InspectMediaQueue) y los
// tests inyectan un doble: una función admite ambos sin indirección.
type StatsProvider func() (*QueueStats, error)

// Collector publica el estado de la cola de media como métricas Prometheus.
//
// Es la fuente de verdad de profundidad, antigüedad y tasa de procesamiento,
// que el enunciado exige medir y que ningún componente expone por otra vía.
type Collector struct {
	provider StatsProvider
	all      []prometheus.Metric

	exists      prometheus.Gauge
	pending     prometheus.Gauge
	active      prometheus.Gauge
	scheduled   prometheus.Gauge
	retry       prometheus.Gauge
	archived    prometheus.Gauge
	completed   prometheus.Gauge
	aggregating prometheus.Gauge
	size        prometheus.Gauge
	memoryBytes prometheus.Gauge
	// ageSeconds es la latencia de la cola medida sobre el pendiente más viejo:
	// la señal directa de si los workers dan abasto a la tasa de entrada.
	ageSeconds prometheus.Gauge
	// processed y failed son acumulados desde que existe la cola, así que se
	// publican como contadores: la diferencia respecto de la lectura anterior es
	// el_delta real aunque asynq devuelva un total y no un incremento.
	processed prometheus.Counter
	failed    prometheus.Counter
	// scrapeOK distingue "la cola está vacía" de "no se pudo leer la cola". Sin
	// esto, un fallo de Redis publicaría ceros y el informe concluiría que la
	// cola drenó, que es justo la conclusión equivocada.
	scrapeOK     prometheus.Gauge
	scrapeErrors prometheus.Counter

	lastProcessed float64
	lastFailed    float64
}

// NewCollector construye el collector sobre una fuente de estado. Registra las
// métricas en el registrador que se le pase, que debe llevar la etiqueta
// component del proceso.
func NewCollector(provider StatsProvider) *Collector {
	c := &Collector{
		provider: provider,
		exists: gauge("mooc_queue_exists",
			"1 si la cola existe en Redis."),
		pending: gauge("mooc_queue_pending",
			"Trabajos pendientes de ejecución."),
		active: gauge("mooc_queue_active",
			"Trabajos tomados por un worker y en ejecución."),
		scheduled: gauge("mooc_queue_scheduled",
			"Trabajos programados para ejecutarse más tarde."),
		retry: gauge("mooc_queue_retry",
			"Trabajos esperando un reintento tras un fallo."),
		archived: gauge("mooc_queue_archived",
			"Trabajos en la DLQ tras agotar los reintentos."),
		completed: gauge("mooc_queue_completed",
			"Trabajos completados que aún están en Redis."),
		aggregating: gauge("mooc_queue_aggregating",
			"Trabajos en grupos de agregación pendientes."),
		size: gauge("mooc_queue_size",
			"Total de trabajos en la cola, en cualquier estado."),
		memoryBytes: gauge("mooc_queue_memory_bytes",
			"Uso aproximado de Redis de la cola y sus trabajos."),
		ageSeconds: gauge("mooc_queue_age_seconds",
			"Antigüedad del trabajo pendiente más viejo."),
		processed: counter("mooc_queue_processed_total",
			"Trabajos procesados desde que existe la cola."),
		failed: counter("mooc_queue_failed_total",
			"Trabajos que fallaron definitivamente desde que existe la cola."),
		scrapeOK: gauge("mooc_queue_scrape_ok",
			"1 si la última lectura del estado de la cola tuvo éxito."),
		scrapeErrors: counter("mooc_queue_scrape_errors_total",
			"Fallos al leer el estado de la cola."),
	}
	// all es el conjunto que Collect emite, incluidas las métricas que conservan
	// su último valor: una lista para no repetir 15 emisiones en dos sitios.
	c.all = []prometheus.Metric{c.exists, c.pending, c.active, c.scheduled, c.retry, c.archived, c.completed, c.aggregating, c.size, c.memoryBytes, c.ageSeconds, c.processed, c.failed, c.scrapeOK, c.scrapeErrors}
	return c
}

// Register registra el collector en el registrador del proceso.
func (c *Collector) Register(r prometheus.Registerer) error {
	return r.Register(c)
}

// Describe implementa prometheus.Collector. Las métricas se describen en
// Collect porque el conjunto es fijo y ya se conoce aquí.
func (c *Collector) Describe(ch chan<- *prometheus.Desc) {}

// Collect lee el estado de la cola y publica las métricas. Ante un fallo deja las
// últimas valeurs conocidas y marca scrapeOK en 0: publicar ceros en un fallo
// sería indistinguible de una cola drenada.
func (c *Collector) Collect(ch chan<- prometheus.Metric) {
	stats, err := c.provider()
	if err != nil {
		c.scrapeErrors.Inc()
		c.scrapeOK.Set(0)
		c.publish(ch)
		return
	}
	c.scrapeOK.Set(1)

	c.exists.Set(boolValue(stats.Exists))
	c.pending.Set(float64(stats.Pending))
	c.active.Set(float64(stats.Active))
	c.scheduled.Set(float64(stats.Scheduled))
	c.retry.Set(float64(stats.Retry))
	c.archived.Set(float64(stats.Archived))
	c.completed.Set(float64(stats.Completed))
	c.aggregating.Set(float64(stats.Aggregating))
	c.size.Set(float64(stats.Size))
	c.memoryBytes.Set(float64(stats.MemoryUsage))
	c.ageSeconds.Set(stats.AgeOldestPendingSec)

	// asynq entrega acumulados, no incrementos. Solo se suma la diferencia
	// respecto de la lectura previa, y si el total decreased (la cola se borró y
	// se recreó) se ignora: un Counter de Prometheus no puede retroceder.
	if v := float64(stats.ProcessedTotal); v >= c.lastProcessed {
		c.processed.Add(v - c.lastProcessed)
		c.lastProcessed = v
	}
	if v := float64(stats.FailedTotal); v >= c.lastFailed {
		c.failed.Add(v - c.lastFailed)
		c.lastFailed = v
	}

	c.publish(ch)
}

// publish emite todas las métricas, incluidas las que conservan su último valor.
func (c *Collector) publish(ch chan<- prometheus.Metric) {
	for _, m := range c.all {
		ch <- m
	}
}

func gauge(name, help string) prometheus.Gauge {
	return prometheus.NewGauge(prometheus.GaugeOpts{Name: name, Help: help})
}

func counter(name, help string) prometheus.Counter {
	return prometheus.NewCounter(prometheus.CounterOpts{Name: name, Help: help})
}

func boolValue(b bool) float64 {
	if b {
		return 1
	}
	return 0
}
