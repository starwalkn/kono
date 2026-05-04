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
	"go.opentelemetry.io/otel/sdk/resource"
)

const MeterName = "kono"

const defaultMetricsInterval = 60 * time.Second

type FailReason string

const (
	FailReasonNoMatchedFlow   FailReason = "no_matched_flow"
	FailReasonBodyTooLarge    FailReason = "body_too_large"
	FailReasonTooManyRequests FailReason = "too_many_requests"
)

type Metrics struct {
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

func New() (*Metrics, error) {
	meter := otel.Meter(MeterName)
	m := &Metrics{}

	var err error

	m.requestsTotal, err = meter.Int64Counter("kono.requests.total",
		otelmetric.WithDescription("Total number of incoming requests"),
	)
	if err != nil {
		return nil, err
	}

	m.requestsDuration, err = meter.Float64Histogram(
		"kono.requests.duration",
		otelmetric.WithDescription("End-to-end request duration in seconds"),
		otelmetric.WithUnit("s"),
	)
	if err != nil {
		return nil, err
	}

	m.requestsInFlight, err = meter.Int64UpDownCounter(
		"kono.requests.in_flight",
		otelmetric.WithDescription("Current number of in-flight requests"),
	)
	if err != nil {
		return nil, err
	}

	m.failedRequestsTotal, err = meter.Int64Counter(
		"kono.failed_requests.total",
		otelmetric.WithDescription("Total number of requests rejected before flow processing"),
	)
	if err != nil {
		return nil, err
	}

	m.upstreamLatency, err = meter.Float64Histogram(
		"kono.upstream.latency",
		otelmetric.WithDescription("Upstream response latency in seconds"),
		otelmetric.WithUnit("s"),
	)
	if err != nil {
		return nil, err
	}

	m.upstreamRequestsTotal, err = meter.Int64Counter(
		"kono.upstream.requests.total",
		otelmetric.WithDescription("Total number of requests dispatched to upstreams"),
	)
	if err != nil {
		return nil, err
	}

	m.upstreamErrorsTotal, err = meter.Int64Counter(
		"kono.upstream.errors.total",
		otelmetric.WithDescription("Total number of upstream errors by kind"),
	)
	if err != nil {
		return nil, err
	}

	m.upstreamRetriesTotal, err = meter.Int64Counter(
		"kono.upstream.retries.total",
		otelmetric.WithDescription("Total number of upstream retry attempts"),
	)
	if err != nil {
		return nil, err
	}

	m.circuitBreakerState, err = meter.Float64Gauge(
		"kono.circuit_breaker.state",
		otelmetric.WithDescription("Circuit breaker state: 0=closed, 1=open, 2=half-open"),
	)
	if err != nil {
		return nil, err
	}

	return m, nil
}

func NewOtelPrometheus(res *resource.Resource) (*sdkmetric.MeterProvider, *prometheus.Registry, error) {
	reg := prometheus.NewRegistry()

	exporter, err := otelprometheus.New(otelprometheus.WithRegisterer(reg))
	if err != nil {
		return nil, nil, err
	}

	provider := newMeterProvider(exporter, res)
	otel.SetMeterProvider(provider)

	return provider, reg, nil
}

func NewOtelOTLP(
	ctx context.Context,
	endpoint string,
	insecure bool,
	interval time.Duration,
	res *resource.Resource,
) (*sdkmetric.MeterProvider, error) {
	opts := []otlpmetrichttp.Option{
		otlpmetrichttp.WithEndpoint(endpoint),
	}
	if insecure {
		opts = append(opts, otlpmetrichttp.WithInsecure())
	}

	exporter, err := otlpmetrichttp.New(ctx, opts...)
	if err != nil {
		return nil, err
	}

	if interval == 0 {
		interval = defaultMetricsInterval
	}

	reader := sdkmetric.NewPeriodicReader(exporter, sdkmetric.WithInterval(interval))
	provider := newMeterProvider(reader, res)
	otel.SetMeterProvider(provider)

	return provider, nil
}

func newMeterProvider(reader sdkmetric.Reader, res *resource.Resource) *sdkmetric.MeterProvider {
	return sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(res),
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

func (m *Metrics) IncRequestsTotal(route, method string, status int) {
	m.requestsTotal.Add(context.Background(), 1,
		otelmetric.WithAttributes(
			attribute.String("route", route),
			attribute.String("method", method),
			attribute.Int("status", status),
		),
	)
}

func (m *Metrics) UpdateRequestsDuration(route, method string, start time.Time) {
	m.requestsDuration.Record(context.Background(),
		time.Since(start).Seconds(),
		otelmetric.WithAttributes(
			attribute.String("route", route),
			attribute.String("method", method),
		),
	)
}

func (m *Metrics) IncRequestsInFlight() {
	m.requestsInFlight.Add(context.Background(), 1)
}

func (m *Metrics) DecRequestsInFlight() {
	m.requestsInFlight.Add(context.Background(), -1)
}

func (m *Metrics) IncFailedRequestsTotal(reason FailReason) {
	m.failedRequestsTotal.Add(context.Background(), 1,
		otelmetric.WithAttributes(
			attribute.String("reason", string(reason)),
		),
	)
}

func (m *Metrics) UpdateUpstreamLatency(route, upstream string, start time.Time) {
	m.upstreamLatency.Record(context.Background(),
		time.Since(start).Seconds(),
		otelmetric.WithAttributes(
			attribute.String("route", route),
			attribute.String("upstream", upstream),
		),
	)
}

func (m *Metrics) IncUpstreamRequestsTotal(route, upstream string) {
	m.upstreamRequestsTotal.Add(context.Background(), 1,
		otelmetric.WithAttributes(
			attribute.String("route", route),
			attribute.String("upstream", upstream),
		),
	)
}

func (m *Metrics) IncUpstreamErrorsTotal(route, upstream, kind string) {
	m.upstreamErrorsTotal.Add(context.Background(), 1,
		otelmetric.WithAttributes(
			attribute.String("route", route),
			attribute.String("upstream", upstream),
			attribute.String("kind", kind),
		),
	)
}

func (m *Metrics) IncUpstreamRetriesTotal(route, upstream string) {
	m.upstreamRetriesTotal.Add(context.Background(), 1,
		otelmetric.WithAttributes(
			attribute.String("route", route),
			attribute.String("upstream", upstream),
		),
	)
}

func (m *Metrics) SetCircuitBreakerState(upstream string, state float64) {
	m.circuitBreakerState.Record(context.Background(), state,
		otelmetric.WithAttributes(
			attribute.String("upstream", upstream),
		),
	)
}
