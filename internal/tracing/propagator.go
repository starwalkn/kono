package tracing

import (
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
)

func InstallPropagator() {
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))
}
