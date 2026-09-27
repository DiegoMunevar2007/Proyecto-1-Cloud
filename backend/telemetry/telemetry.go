// Package telemetry expone la observabilidad de la plataforma MOOC en formato
// Prometheus.
//
// No hay un servidor Prometheus en el despliegue: la recolección la hace el Ops
// Agent de Google Cloud, que corre en cada máquina, scrapea estos endpoints y
// reenvía las series a Cloud Monitoring / Managed Service for Prometheus. Por eso
// la exposición usa la etiqueta component= y no job=: el receptor de Prometheus
// del agente convierte el job_name del scrape_config en una etiqueta job, y una
// job horneada en el texto chocaría con ella y el scrape se descartaría.
package telemetry

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
)

const (
	// ComponentAPI etiqueta las series que expone la API.
	ComponentAPI = "api"
	// ComponentWorker etiqueta las series que expone el worker.
	ComponentWorker = "worker"
)

// NewRegistry construye un registro Prometheus con los colectores de runtime y de
// proceso, y devuelve también el registrador ya etiquetado con component para que
// las métricas propias del proceso usen la misma etiqueta.
//
// Se usa un registro propio y no el global: el s3store de tusd ya registra sus
// colectores tusd_s3_* en el registro por defecto, y registrar otra vez el
// colector de runtime ahí produce un panic por nombre duplicado.
func NewRegistry(component string) (*prometheus.Registry, prometheus.Registerer) {
	reg := prometheus.NewRegistry()
	labeled := prometheus.WrapRegistererWith(prometheus.Labels{"component": component}, reg)
	labeled.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)
	return reg, labeled
}
