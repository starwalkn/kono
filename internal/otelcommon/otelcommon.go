// Package otelcommon builds the shared OpenTelemetry Resource used by both
// the metric and tracing providers, so signals from the same process share
// a consistent identity (service.name, service.version, host, process, …)
// in the observability backend.
package otelcommon

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.40.0"
)

type Provider interface {
	Shutdown(ctx context.Context) error
}

// DefaultServiceName is used when the configured service name is empty.
// Should match cfg.Gateway.Service.Name's default tag in config.go.
const DefaultServiceName = "kono"

func NewResource(ctx context.Context, serviceName, serviceVersion string) (*resource.Resource, error) {
	if serviceName == "" {
		serviceName = DefaultServiceName
	}

	attrs := []attribute.KeyValue{
		semconv.ServiceName(serviceName),
	}
	if serviceVersion != "" {
		attrs = append(attrs, semconv.ServiceVersion(serviceVersion))
	}

	return resource.New(ctx,
		resource.WithSchemaURL(semconv.SchemaURL),
		resource.WithAttributes(attrs...),
		resource.WithTelemetrySDK(),
		resource.WithHost(),
		resource.WithProcess(),
		resource.WithFromEnv(),
	)
}
