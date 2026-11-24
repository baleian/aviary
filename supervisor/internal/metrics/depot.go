package metrics

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"aviary/supervisor/internal/depot"
)

// Depot counts and times the requests that a depot serves, and reports the
// volume that holds the objects. Each depot reports only its own requests, so
// that the metrics of all depots add up. They share the volume, so each
// reports the same of it.
type Depot struct {
	registry *prometheus.Registry
	took     *prometheus.HistogramVec
	moved    *prometheus.CounterVec
}

// The operations of a depot, by the method of their requests.
var depotOperations = map[string]string{
	http.MethodPut: "write", http.MethodGet: "read", http.MethodDelete: "delete",
}

// NewDepot returns the metrics of a depot with every series at zero. volume
// returns the size of the volume and how much of it is free, in bytes, and
// is called at every scrape.
func NewDepot(volume func() (capacity, available uint64, err error)) *Depot {
	d := &Depot{
		registry: prometheus.NewRegistry(),
		took: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "aviary_depot_request_duration_seconds",
			Help:    "Time that a depot took to serve a request for an object, in seconds, by operation and outcome. A request is refused if it could not be served as it was made, and failed if the depot or its volume failed it.",
			Buckets: []float64{0.01, 0.05, 0.1, 0.5, 1, 5, 10, 30, 60, 120, 300, 600},
		}, []string{"operation", "outcome"}),
		moved: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "aviary_depot_object_bytes_total",
			Help: "Bytes of objects that a depot has written or read, by operation.",
		}, []string{"operation"}),
	}
	d.registry.MustRegister(
		d.took, d.moved,
		depotVolume{
			read: volume,
			capacity: prometheus.NewDesc(
				"aviary_depot_volume_capacity_bytes", "Size of the volume that holds the objects, in bytes.", nil, nil,
			),
			available: prometheus.NewDesc(
				"aviary_depot_volume_available_bytes", "Bytes of the volume that holds the objects that are free.", nil, nil,
			),
		},
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)
	for _, operation := range depotOperations {
		for _, outcome := range []string{depot.Done, depot.Refused, depot.Failed} {
			d.took.WithLabelValues(operation, outcome)
		}
	}
	d.moved.WithLabelValues("write")
	d.moved.WithLabelValues("read")
	return d
}

// Handler serves the metrics. A scrape fails if the volume cannot be read.
func (d *Depot) Handler() http.Handler {
	return promhttp.HandlerFor(d.registry, promhttp.HandlerOpts{})
}

// Served records a request for an object: its method and outcome, how long
// it took and the bytes of the object that were written or read.
func (d *Depot) Served(method, outcome string, took time.Duration, moved int64) {
	operation := depotOperations[method]
	d.took.WithLabelValues(operation, outcome).Observe(took.Seconds())
	if moved > 0 {
		d.moved.WithLabelValues(operation).Add(float64(moved))
	}
}

// depotVolume collects the size of the volume and how much of it is free.
type depotVolume struct {
	read                func() (capacity, available uint64, err error)
	capacity, available *prometheus.Desc
}

func (v depotVolume) Describe(descs chan<- *prometheus.Desc) {
	descs <- v.capacity
	descs <- v.available
}

func (v depotVolume) Collect(collected chan<- prometheus.Metric) {
	capacity, available, err := v.read()
	if err != nil {
		slog.Error("read the volume", "error", err)
		collected <- prometheus.NewInvalidMetric(v.capacity, errors.New("the volume could not be read"))
		return
	}
	collected <- prometheus.MustNewConstMetric(v.capacity, prometheus.GaugeValue, float64(capacity))
	collected <- prometheus.MustNewConstMetric(v.available, prometheus.GaugeValue, float64(available))
}
