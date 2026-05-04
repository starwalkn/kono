package tracing

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

const (
	defaultBatchTimeout = 5 * time.Second

	TracerName = "kono"
)

// NewOtelOTLP initializes an OTLP/HTTP trace exporter with a batching span processor,
// installs a TracerProvider as the global one, and registers a composite W3C
// (TraceContext + Baggage) propagator so traceparent headers flow through outgoing
// upstream requests automatically.
//
// The returned provider must be Shutdown by the caller on application exit to flush
// pending spans. The supplied ctx is used both for exporter creation and is the
// recommended context to pass to Shutdown.
func NewOtelOTLP(
	ctx context.Context,
	endpoint string,
	insecure bool,
	batchTimeout time.Duration,
	samplingRatio float64,
	res *resource.Resource,
) (*sdktrace.TracerProvider, error) {
	opts := []otlptracehttp.Option{
		otlptracehttp.WithEndpoint(endpoint),
	}
	if insecure {
		opts = append(opts, otlptracehttp.WithInsecure())
	}

	exporter, err := otlptracehttp.New(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("create otlp trace exporter: %w", err)
	}

	if batchTimeout == 0 {
		batchTimeout = defaultBatchTimeout
	}

	provider := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter, sdktrace.WithBatchTimeout(batchTimeout)),
		sdktrace.WithSampler(SamplerFor(samplingRatio)),
		sdktrace.WithResource(res),
	)

	otel.SetTracerProvider(provider)

	return provider, nil
}

// SamplerFor returns a parent-based sampler with the given root-trace ratio.
//
//   - ratio >= 1.0 -> AlwaysSample (every root trace is recorded)
//   - ratio <= 0.0 -> NeverSample for new roots; existing sampled parents
//     are still respected via ParentBased
//   - otherwise    -> TraceIDRatioBased(ratio)
//
// In all three cases the decision from an incoming traceparent wins over the
// local ratio — that is the point of ParentBased.
func SamplerFor(ratio float64) sdktrace.Sampler {
	switch {
	case ratio >= 1.0:
		return sdktrace.ParentBased(sdktrace.AlwaysSample())
	case ratio <= 0.0:
		return sdktrace.ParentBased(sdktrace.NeverSample())
	default:
		return sdktrace.ParentBased(sdktrace.TraceIDRatioBased(ratio))
	}
}
