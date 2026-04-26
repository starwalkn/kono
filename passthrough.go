package kono

import (
	"context"
	"fmt"
	"io"
	"net/http"

	"go.uber.org/zap"

	"github.com/starwalkn/kono/sdk"
)

const streamBuffer = 4096

type proxyCapable interface {
	proxy(ctx context.Context, w http.ResponseWriter, original *http.Request) error
}

type trackingWriter struct {
	http.ResponseWriter
	written bool
}

func (tw *trackingWriter) WriteHeader(code int) {
	tw.written = true
	tw.ResponseWriter.WriteHeader(code)
}

func (tw *trackingWriter) Write(b []byte) (int, error) {
	tw.written = true
	return tw.ResponseWriter.Write(b)
}

func (tw *trackingWriter) Flush() {
	if f, ok := tw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// handlePassthrough streams a request directly to the single upstream without buffering,
// enabling SSE and chunked transfer. Request plugins still run.
func (r *Router) handlePassthrough(w http.ResponseWriter, req *http.Request, flow *flow, log *zap.Logger) {
	if len(flow.upstreams) != 1 {
		log.Error("passthrough flow must have exactly one upstream",
			zap.Int("configured", len(flow.upstreams)),
		)
		WriteError(w, ClientErrInternal, http.StatusInternalServerError)

		return
	}

	u := flow.upstreams[0]

	proxy, ok := u.(proxyCapable)
	if !ok {
		log.Error("upstream does not support passthrough (does not implement proxyCapable)",
			zap.String("upstream", u.name()),
		)
		WriteError(w, ClientErrInternal, http.StatusInternalServerError)

		return
	}

	kctx := newContext(req)

	if !r.executePlugins(sdk.PluginTypeRequest, w, kctx, flow, log) {
		return
	}

	tw := &trackingWriter{ResponseWriter: w}

	if err := proxy.proxy(req.Context(), tw, kctx.Request()); err != nil {
		if !tw.written {
			WriteError(w, ClientErrUpstreamUnavailable, http.StatusBadGateway)
		}

		log.Error("passthrough upstream error", zap.Error(err))
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
