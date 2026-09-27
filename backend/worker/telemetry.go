package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/DiegoMunevar2007/Proyecto-1-Cloud.git/telemetry"
	"github.com/DiegoMunevar2007/Proyecto-1-Cloud.git/utils"
	"github.com/hibiken/asynq"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// metricsAddrDefault es el puerto que publica el worker. El compose lo publica
// solo en loopback para que el Ops Agent de la VM lo alcance sin exponerlo.
const metricsAddrDefault = ":9101"

// jobMetrics son los contadores de trabajo del worker. Se publican en /metrics y
// de ahí los lleva el Ops Agent a Cloud Monitoring, así que el estado del worker
// queda en la misma fuente que el de la API y el de las máquinas.
type jobMetrics struct {
	registry *prometheus.Registry
	active   prometheus.Gauge
	ffmpeg   prometheus.Gauge
	done     prometheus.Counter
	failed   prometheus.Counter
	retry    prometheus.Counter
}

// jobs es el registro de telemetría del worker. Se construye al arrancar el
// paquete para que ningún manejador tenga que comprobar si ya existe.
var jobs = newJobMetrics()

// newJobMetrics construye el registro del worker con sus contadores.
func newJobMetrics() *jobMetrics {
	registry, labeled := telemetry.NewRegistry(telemetry.ComponentWorker)
	j := &jobMetrics{
		registry: registry,
		active: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "mooc_worker_jobs_active",
			Help: "Trabajos de media en ejecución ahora mismo en el worker.",
		}),
		ffmpeg: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "mooc_worker_ffmpeg_active",
			Help: "Procesos ffmpeg simultáneos, que es lo que satura los 2 vCPU del worker.",
		}),
		done: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "mooc_worker_jobs_completed_total",
			Help: "Trabajos de media completados correctamente desde el arranque.",
		}),
		failed: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "mooc_worker_jobs_failed_total",
			Help: "Ejecuciones que terminaron en error, incluidas las que se van a reintentar.",
		}),
		retry: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "mooc_worker_jobs_retry_scheduled_total",
			Help: "Reintentos programados por el backoff de asynq.",
		}),
	}
	labeled.MustRegister(j.active, j.ffmpeg, j.done, j.failed, j.retry)
	return j
}

// JobStarted marca el inicio de un trabajo de media.
func (j *jobMetrics) JobStarted() { j.active.Inc() }

// JobFinished cierra el trabajo y contabiliza el resultado.
func (j *jobMetrics) JobFinished(err error) {
	j.active.Dec()
	if err != nil {
		j.failed.Inc()
		return
	}
	j.done.Inc()
}

// RetryScheduled cuenta un reintento programado por el backoff.
func (j *jobMetrics) RetryScheduled() { j.retry.Inc() }

// FFmpegStarted marca el arranque de un proceso ffmpeg.
func (j *jobMetrics) FFmpegStarted() { j.ffmpeg.Inc() }

// FFmpegFinished marca la salida de un proceso ffmpeg.
func (j *jobMetrics) FFmpegFinished() { j.ffmpeg.Dec() }

// instrumentar envuelve un manejador de asynq para contabilizar trabajos activos
// y resultados. El contador de trabajos en curso es la señal directa de si la
// concurrencia del worker está o no tocando su techo.
func instrumentar(fn func(context.Context, *asynq.Task) error) func(context.Context, *asynq.Task) error {
	return func(ctx context.Context, t *asynq.Task) error {
		jobs.JobStarted()
		err := fn(ctx, t)
		jobs.JobFinished(err)
		return err
	}
}

// serveMetrics publica el /metrics del worker hasta que se cancele el contexto.
// El worker no tenía servidor HTTP: el que scrapea es el Ops Agent que corre en
// el host, y por eso el compose publica el puerto solo en 127.0.0.1.
func serveMetrics(ctx context.Context, addr string) {
	if addr == "" {
		addr = metricsAddrDefault
	}

	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(jobs.registry, promhttp.HandlerOpts{
		ErrorHandling: promhttp.ContinueOnError,
	}))
	// Raíz mínima para que un curl a la IP sin ruta no devuelva 404 vacío.
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("mooc worker metrics: /metrics\n"))
	})

	server := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	log.Printf("worker: métricas en %s/metrics (scraping por Ops Agent)", addr)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Printf("worker: servidor de métricas detenido: %v", err)
	}
}

// metricsAddr lee el puerto de métricas del entorno, con el valor por defecto.
func metricsAddr() string {
	return utils.GetEnv("METRICS_ADDR", metricsAddrDefault)
}
