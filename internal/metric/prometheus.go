package metric

import (
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

type prometheusMetrics struct {
	RequestsTotal       prometheus.Counter
	RequestsDuration    *prometheus.HistogramVec
	ResponsesTotal      *prometheus.CounterVec
	RequestsInFlight    prometheus.Gauge
	FailedRequestsTotal *prometheus.CounterVec
	UpstreamLatency     *prometheus.HistogramVec
}

func NewPrometheus() Metrics {
	m := &prometheusMetrics{
		RequestsTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "tokka_requests_total",
			Help: "Total number of API requests",
		}),
		RequestsDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "tokka_requests_duration_seconds",
				Help:    "API request duration in seconds",
				Buckets: prometheus.DefBuckets,
			},
			[]string{"route", "method"},
		),
		ResponsesTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "tokka_responses_total",
				Help: "Total number of responses by status code",
			},
			[]string{"status"},
		),
		RequestsInFlight: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "tokka_requests_in_flight",
			Help: "Current number of in-flight requests",
		}),
		FailedRequestsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "tokka_failed_requests_total",
				Help: "Total number of failed requests by reason",
			},
			[]string{"reason"},
		),
		UpstreamLatency: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name: "tokka_upstream_latency",
				Help: "",
			},
			[]string{"route", "method", "upstream"},
		),
	}

	prometheus.MustRegister(
		m.RequestsTotal,
		m.RequestsDuration,
		m.ResponsesTotal,
		m.RequestsInFlight,
		m.FailedRequestsTotal,
	)

	return m
}

func (m *prometheusMetrics) IncRequestsTotal() {
	m.RequestsTotal.Inc()
}

func (m *prometheusMetrics) UpdateRequestsDuration(route, method string, start time.Time) {
	m.RequestsDuration.WithLabelValues(route, method).Observe(time.Since(start).Seconds())
}

func (m *prometheusMetrics) IncResponsesTotal(status int) {
	m.ResponsesTotal.WithLabelValues(strconv.Itoa(status)).Inc()
}

func (m *prometheusMetrics) IncRequestsInFlight() {
	m.RequestsInFlight.Inc()
}

func (m *prometheusMetrics) DecRequestsInFlight() {
	m.RequestsInFlight.Dec()
}

func (m *prometheusMetrics) IncFailedRequestsTotal(reason FailReason) {
	m.FailedRequestsTotal.WithLabelValues(string(reason)).Inc()
}

func (m *prometheusMetrics) UpdateUpstreamLatency(route, method, upstream string, lat time.Duration) {
	m.UpstreamLatency.WithLabelValues(route, method, upstream).Observe(lat.Seconds())
}
