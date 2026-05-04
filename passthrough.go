package kono

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"github.com/starwalkn/kono/internal/tracing"
	"github.com/starwalkn/kono/sdk"
)

const streamBuffer = 4096

type proxyCapable interface {
	proxy(ctx context.Context, w http.ResponseWriter, original *http.Request) error
}

type trackingWriter struct {
	http.ResponseWriter
	written    bool
	statusCode int
}

func (tw *trackingWriter) WriteHeader(code int) {
	tw.written = true
	tw.statusCode = code
	tw.ResponseWriter.WriteHeader(code)
}

func (tw *trackingWriter) Write(b []byte) (int, error) {
	tw.written = true

	if tw.statusCode == 0 {
		tw.statusCode = http.StatusOK
	}

	return tw.ResponseWriter.Write(b)
}

func (tw *trackingWriter) Flush() {
	if f, ok := tw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// handlePassthrough streams a request directly to the single upstream without buffering,
// enabling SSE and chunked transfer. Request plugins still run.
func (r *Router) handlePassthrough(w http.ResponseWriter, req *http.Request, f *flow, log *zap.Logger) {
	if len(f.upstreams) != 1 {
		log.Error("passthrough flow must have exactly one upstream",
			zap.Int("configured", len(f.upstreams)),
		)
		WriteError(w, ClientErrInternal, http.StatusInternalServerError)

		return
	}

	u := f.upstreams[0]

	proxy, ok := u.(proxyCapable)
	if !ok {
		log.Error("upstream does not support passthrough (does not implement proxyCapable)",
			zap.String("upstream", u.name()),
		)
		WriteError(w, ClientErrInternal, http.StatusInternalServerError)

		return
	}

	rc := http.NewResponseController(w)
	if err := rc.SetWriteDeadline(time.Time{}); err != nil {
		log.Warn("cannot disable write deadline for passthrough", zap.Error(err))
	}
	if err := rc.SetReadDeadline(time.Time{}); err != nil {
		log.Warn("cannot disable read deadline for passthrough", zap.Error(err))
	}

	kctx := newContext(req)

	if !r.executePlugins(sdk.PluginTypeRequest, w, kctx, f, log) {
		return
	}

	tracer := otel.Tracer(tracing.TracerName)
	ctx, span := tracer.Start(kctx.Request().Context(), "kono.upstream",
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(
			attribute.String("kono.upstream.name", u.name()),
			attribute.String("kono.upstream.mode", "passthrough"),
			attribute.String("kono.flow.path", f.path),
		),
	)
	defer span.End()

	tw := &trackingWriter{ResponseWriter: w}

	err := proxy.proxy(ctx, tw, kctx.Request())
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "passthrough upstream error")

		if !tw.written {
			WriteError(w, ClientErrUpstreamUnavailable, http.StatusBadGateway)
		}

		log.Error("passthrough upstream error", zap.Error(err))
	}

	if tw.statusCode != 0 {
		span.SetAttributes(attribute.Int("http.status_code", tw.statusCode))
		if tw.statusCode >= http.StatusInternalServerError && err == nil {
			span.SetStatus(codes.Error, http.StatusText(tw.statusCode))
		}
	}
}

// streamCopy copies src to dst, flushing after each read if the writer supports it.
// buf size of 4 KiB is intentional: small enough for SSE events, large enough for chunked blobs.
func streamCopy(dst http.ResponseWriter, src io.Reader) error {
	flusher, canFlush := dst.(http.Flusher)

	buf := make([]byte, streamBuffer)

	for {
		n, readErr := src.Read(buf)
		if n > 0 {
			if _, writeErr := dst.Write(buf[:n]); writeErr != nil {
				return fmt.Errorf("write to client: %w", writeErr)
			}

			if canFlush {
				flusher.Flush()
			}
		}

		if readErr == io.EOF {
			return nil
		}

		if readErr != nil {
			return fmt.Errorf("read from upstream: %w", readErr)
		}
	}
}
