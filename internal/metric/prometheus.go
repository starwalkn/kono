package metric

import (
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
)

type prometheusMetrics struct {
	requestsTotal         *prometheus.CounterVec
	requestsDuration      *prometheus.HistogramVec
	requestsInFlight      prometheus.Gauge
	failedRequestsTotal   *prometheus.CounterVec
	upstreamLatency       *prometheus.HistogramVec
	upstreamRequestsTotal *prometheus.CounterVec
	upstreamErrorsTotal   *prometheus.CounterVec
	upstreamRetriesTotal  *prometheus.CounterVec
	circuitBreakerState   *prometheus.GaugeVec
}

func NewPrometheus() (Metrics, *prometheus.Registry) {
	reg := prometheus.NewRegistry()

	m := &prometheusMetrics{
		requestsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "kono_requests_total",
				Help: "Total number of incoming requests",
			},
			[]string{"route", "method", "status"},
		),

		requestsDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "kono_requests_duration_seconds",
				Help:    "End-to-end request duration in seconds",
				Buckets: []float64{.001, .005, .01, .025, .05, .1, .25, .5, 1, 2.5},
			},
			[]string{"route", "method"},
		),

		requestsInFlight: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "kono_requests_in_flight",
			Help: "Current number of in-flight requests",
		}),

		failedRequestsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "kono_failed_requests_total",
				Help: "Total number of requests rejected before flow processing, by reason",
			},
			[]string{"reason"},
		),

		upstreamLatency: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "kono_upstream_latency_seconds",
				Help:    "Upstream response latency in seconds",
				Buckets: []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5},
			},
			[]string{"route", "upstream"},
		),

		upstreamRequestsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "kono_upstream_requests_total",
				Help: "Total number of requests dispatched to upstreams",
			},
			[]string{"route", "upstream"},
		),

		upstreamErrorsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "kono_upstream_errors_total",
				Help: "Total number of upstream errors by upstream and error kind",
			},
			[]string{"route", "upstream", "kind"},
		),

		upstreamRetriesTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "kono_upstream_retries_total",
				Help: "Total number of upstream retry attempts",
			},
			[]string{"route", "upstream"},
		),

		circuitBreakerState: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "kono_circuit_breaker_state",
				Help: "Circuit breaker state per upstream: 0=closed, 1=open, 2=half-open",
			},
			[]string{"upstream"},
		),
	}

	reg.MustRegister(
		m.requestsTotal,
		m.requestsDuration,
		m.requestsInFlight,
		m.failedRequestsTotal,
		m.upstreamLatency,
		m.upstreamRequestsTotal,
		m.upstreamErrorsTotal,
		m.upstreamRetriesTotal,
		m.circuitBreakerState,

		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)

	return m, reg
}

func (m *prometheusMetrics) IncRequestsTotal(route, method string, status int) {
	m.requestsTotal.WithLabelValues(route, method, strconv.Itoa(status)).Inc()
}

func (m *prometheusMetrics) UpdateRequestsDuration(route, method string, start time.Time) {
	m.requestsDuration.WithLabelValues(route, method).Observe(time.Since(start).Seconds())
}

func (m *prometheusMetrics) IncRequestsInFlight() {
	m.requestsInFlight.Inc()
}

func (m *prometheusMetrics) DecRequestsInFlight() {
	m.requestsInFlight.Dec()
}

func (m *prometheusMetrics) IncFailedRequestsTotal(reason FailReason) {
	m.failedRequestsTotal.WithLabelValues(string(reason)).Inc()
}

func (m *prometheusMetrics) UpdateUpstreamLatency(route, upstream string, start time.Time) {
	m.upstreamLatency.WithLabelValues(route, upstream).Observe(time.Since(start).Seconds())
}

func (m *prometheusMetrics) IncUpstreamRequestsTotal(route, upstream string) {
	m.upstreamRequestsTotal.WithLabelValues(route, upstream).Inc()
}

func (m *prometheusMetrics) IncUpstreamErrorsTotal(route, upstream, kind string) {
	m.upstreamErrorsTotal.WithLabelValues(route, upstream, kind).Inc()
}

func (m *prometheusMetrics) IncUpstreamRetriesTotal(route, upstream string) {
	m.upstreamRetriesTotal.WithLabelValues(route, upstream).Inc()
}

func (m *prometheusMetrics) SetCircuitBreakerState(upstream string, state float64) {
	m.circuitBreakerState.WithLabelValues(upstream).Set(state)
}
