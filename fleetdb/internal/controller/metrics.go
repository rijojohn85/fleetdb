package controller

import (
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

var fleetDBCredentialsGenerated = prometheus.NewCounter(
	prometheus.CounterOpts{
		Name: "fleetdb_credentials_generated_total",
		Help: "number of database credential secrets generated.",
	},
)

var fleetDBBackupsScheduled = prometheus.NewCounter(
	prometheus.CounterOpts{
		Name: "fleetdb_backups_scheduled_total",
		Help: "Number of backup cronjobs created for tenants.",
	},
)

func init() {
	metrics.Registry.MustRegister(fleetDBCredentialsGenerated)
	metrics.Registry.MustRegister(fleetDBBackupsScheduled)
}
