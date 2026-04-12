package metric

import (
	"context"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	otelprometheus "go.opentelemetry.io/otel/exporters/prometheus"
	otelmetric "go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
)

type otelMetrics struct {
	requestsTotal         otelmetric.Int64Counter
	requestsDuration      otelmetric.Float64Histogram
	requestsInFlight      otelmetric.Int64UpDownCounter
	failedRequestsTotal   otelmetric.Int64Counter
	upstreamLatency       otelmetric.Float64Histogram
	upstreamRequestsTotal otelmetric.Int64Counter
	upstreamErrorsTotal   otelmetric.Int64Counter
	upstreamRetriesTotal  otelmetric.Int64Counter
	circuitBreakerState   otelmetric.Float64Gauge
}

func newMeterProvider(reader sdkmetric.Reader) *sdkmetric.MeterProvider {
	return sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(reader),
		sdkmetric.WithView(
			sdkmetric.NewView(
				sdkmetric.Instrument{Name: "kono.requests.duration"},
				sdkmetric.Stream{
					Aggregation: sdkmetric.AggregationExplicitBucketHistogram{
						Boundaries: []float64{.001, .005, .01, .025, .05, .1, .25, .5, 1, 2.5},
					},
				},
			),
			sdkmetric.NewView(
				sdkmetric.Instrument{Name: "kono.upstream.latency"},
				sdkmetric.Stream{
					Aggregation: sdkmetric.AggregationExplicitBucketHistogram{
						Boundaries: []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5},
					},
				},
			),
		),
	)
}

func newInstruments(provider *sdkmetric.MeterProvider) (*otelMetrics, error) {
	meter := provider.Meter("kono")

	m := &otelMetrics{}

	var initErr error

	m.requestsTotal, initErr = meter.Int64Counter("kono.requests.total",
		otelmetric.WithDescription("Total number of incoming requests"),
	)
	if initErr != nil {
		return nil, initErr
	}

	m.requestsDuration, initErr = meter.Float64Histogram(
		"kono.requests.duration",
		otelmetric.WithDescription("End-to-end request duration in seconds"),
		otelmetric.WithUnit("s"),
	)
	if initErr != nil {
		return nil, initErr
	}

	m.requestsInFlight, initErr = meter.Int64UpDownCounter(
		"kono.requests.in_flight",
		otelmetric.WithDescription("Current number of in-flight requests"),
	)
	if initErr != nil {
		return nil, initErr
	}

	m.failedRequestsTotal, initErr = meter.Int64Counter(
		"kono.failed_requests.total",
		otelmetric.WithDescription("Total number of requests rejected before flow processing"),
	)
	if initErr != nil {
		return nil, initErr
	}

	m.upstreamLatency, initErr = meter.Float64Histogram(
		"kono.upstream.latency",
		otelmetric.WithDescription("Upstream response latency in seconds"),
		otelmetric.WithUnit("s"),
	)
	if initErr != nil {
		return nil, initErr
	}

	m.upstreamRequestsTotal, initErr = meter.Int64Counter(
		"kono.upstream.requests.total",
		otelmetric.WithDescription("Total number of requests dispatched to upstreams"),
	)
	if initErr != nil {
		return nil, initErr
	}

	m.upstreamErrorsTotal, initErr = meter.Int64Counter(
		"kono.upstream.errors.total",
		otelmetric.WithDescription("Total number of upstream errors by kind"),
	)
	if initErr != nil {
		return nil, initErr
	}

	m.upstreamRetriesTotal, initErr = meter.Int64Counter(
		"kono.upstream.retries.total",
		otelmetric.WithDescription("Total number of upstream retry attempts"),
	)
	if initErr != nil {
		return nil, initErr
	}

	m.circuitBreakerState, initErr = meter.Float64Gauge(
		"kono.circuit_breaker.state",
		otelmetric.WithDescription("Circuit breaker state: 0=closed, 1=open, 2=half-open"),
	)
	if initErr != nil {
		return nil, initErr
	}

	return m, nil
}

func NewOtelPrometheus() (Metrics, *prometheus.Registry, error) {
	reg := prometheus.NewRegistry()

	exporter, err := otelprometheus.New(otelprometheus.WithRegisterer(reg))
	if err != nil {
		return nil, nil, err
	}

	provider := newMeterProvider(exporter)
	otel.SetMeterProvider(provider)

	m, err := newInstruments(provider)
	if err != nil {
		return nil, nil, err
	}

	return m, reg, nil
}

func NewOtelOTLP(endpoint string, insecure bool, interval time.Duration) (Metrics, *sdkmetric.MeterProvider, error) {
	opts := []otlpmetrichttp.Option{
		otlpmetrichttp.WithEndpoint(endpoint),
	}
	if insecure {
		opts = append(opts, otlpmetrichttp.WithInsecure())
	}

	exporter, err := otlpmetrichttp.New(context.Background(), opts...)
	if err != nil {
		return nil, nil, err
	}

	if interval == 0 {
		interval = 60 * time.Second
	}

	reader := sdkmetric.NewPeriodicReader(exporter, sdkmetric.WithInterval(interval))
	provider := newMeterProvider(reader)
	otel.SetMeterProvider(provider)

	m, err := newInstruments(provider)
	return m, provider, err
}

func (m *otelMetrics) IncRequestsTotal(route, method string, status int) {
	m.requestsTotal.Add(context.Background(), 1,
		otelmetric.WithAttributes(
			attribute.String("route", route),
			attribute.String("method", method),
			attribute.Int("status", status),
		),
	)
}

func (m *otelMetrics) UpdateRequestsDuration(route, method string, start time.Time) {
	m.requestsDuration.Record(context.Background(),
		time.Since(start).Seconds(),
		otelmetric.WithAttributes(
			attribute.String("route", route),
			attribute.String("method", method),
		),
	)
}

func (m *otelMetrics) IncRequestsInFlight() {
	m.requestsInFlight.Add(context.Background(), 1)
}

func (m *otelMetrics) DecRequestsInFlight() {
	m.requestsInFlight.Add(context.Background(), -1)
}

func (m *otelMetrics) IncFailedRequestsTotal(reason FailReason) {
	m.failedRequestsTotal.Add(context.Background(), 1,
		otelmetric.WithAttributes(
			attribute.String("reason", string(reason)),
		),
	)
}

func (m *otelMetrics) UpdateUpstreamLatency(route, upstream string, start time.Time) {
	m.upstreamLatency.Record(context.Background(),
		time.Since(start).Seconds(),
		otelmetric.WithAttributes(
			attribute.String("route", route),
			attribute.String("upstream", upstream),
		),
	)
}

func (m *otelMetrics) IncUpstreamRequestsTotal(route, upstream string) {
	m.upstreamRequestsTotal.Add(context.Background(), 1,
		otelmetric.WithAttributes(
			attribute.String("route", route),
			attribute.String("upstream", upstream),
		),
	)
}

func (m *otelMetrics) IncUpstreamErrorsTotal(route, upstream, kind string) {
	m.upstreamErrorsTotal.Add(context.Background(), 1,
		otelmetric.WithAttributes(
			attribute.String("route", route),
			attribute.String("upstream", upstream),
			attribute.String("kind", kind),
		),
	)
}

func (m *otelMetrics) IncUpstreamRetriesTotal(route, upstream string) {
	m.upstreamRetriesTotal.Add(context.Background(), 1,
		otelmetric.WithAttributes(
			attribute.String("route", route),
			attribute.String("upstream", upstream),
		),
	)
}

func (m *otelMetrics) SetCircuitBreakerState(upstream string, state float64) {
	m.circuitBreakerState.Record(context.Background(), state,
		otelmetric.WithAttributes(
			attribute.String("upstream", upstream),
		),
	)
}
