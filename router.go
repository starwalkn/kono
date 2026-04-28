package kono

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/oklog/ulid/v2"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"github.com/starwalkn/kono/internal/metric"
	"github.com/starwalkn/kono/internal/ratelimit"
	"github.com/starwalkn/kono/internal/tracing"
	"github.com/starwalkn/kono/sdk"
)

var hopByHopHeaders = map[string]struct{}{
	"Content-Length":      {},
	"Transfer-Encoding":   {},
	"Connection":          {},
	"Trailer":             {},
	"Date":                {},
	"Server":              {},
	"Keep-Alive":          {},
	"Proxy-Authenticate":  {},
	"Proxy-Authorization": {},
	"TE":                  {},
	"Upgrade":             {},
}

var fingerprintIgnoredHeaders = map[string]struct{}{
	"User-Agent":        {},
	"Cookie":            {},
	"Authorization":     {},
	"X-Request-Id":      {},
	"X-Forwarded-For":   {},
	"X-Forwarded-Proto": {},
	"X-Forwarded-Host":  {},
	"X-Forwarded-Port":  {},
	"X-Real-Ip":         {},
	"Forwarded":         {},
	"Host":              {},
	"Accept-Encoding":   {},
	"Accept-Language":   {},
}

type Router struct {
	chiRouter  *chi.Mux
	scatter    scatter
	aggregator aggregator
	flows      []flow

	log         *zap.Logger
	metrics     *metric.Metrics
	rateLimiter *ratelimit.RateLimit
}

// ServeHTTP handles incoming HTTP requests through the full router pipeline:
//  1. Rate limiting — rejects requests exceeding the configured limit.
//  2. Flow matching — chi router finds the flow by method and path (404 if none).
//  3. Middleware execution — per-flow middlewares wrap the handler.
//  4. Request plugins — run before upstream scatter; may modify the request.
//  5. Upstream scatter — fan-out to all configured upstreams.
//  6. Response aggregation — merge/array/namespace strategies with bestEffort support.
//  7. Response plugins — run after aggregation; may modify headers or body.
//  8. Response writing — status, headers, and JSON body sent to the client.
//
// Status codes: 200 on full success, 206 on partial (bestEffort), 502/500 on failure.
// Every response carries an X-Request-ID header and a JSON body with data/errors fields.
func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	r.metrics.IncRequestsInFlight()
	defer r.metrics.DecRequestsInFlight()

	tracer := otel.Tracer(tracing.TracerName)

	ctx := otel.GetTextMapPropagator().Extract(req.Context(), propagation.HeaderCarrier(req.Header))
	ctx, span := tracer.Start(ctx, "kono.request",
		trace.WithSpanKind(trace.SpanKindServer),
		trace.WithAttributes(
			attribute.String("http.method", req.Method),
			attribute.String("url.path", req.URL.Path),
		),
	)
	defer span.End()

	clientIP := extractClientIP(req)
	ctx = withClientIP(ctx, clientIP)
	req = req.WithContext(ctx)

	if !r.allowRequest(w, clientIP) {
		span.SetAttributes(attribute.Int("http.status_code", http.StatusTooManyRequests))
		span.SetStatus(codes.Error, "rate limited")

		return
	}

	r.chiRouter.ServeHTTP(w, req)
}

func (r *Router) Close() error {
	for i := range r.flows {
		for _, mw := range r.flows[i].middlewares {
			if c, ok := mw.(sdk.Closer); ok {
				if err := c.Close(); err != nil {
					r.log.Error("middleware close failed",
						zap.String("name", mw.Name()),
						zap.Error(err),
					)
				}
			}
		}
	}

	return nil
}

func (r *Router) allowRequest(w http.ResponseWriter, clientIP string) bool {
	if r.rateLimiter == nil {
		return true
	}

	if r.rateLimiter.Allow(clientIP) {
		return true
	}

	r.metrics.IncFailedRequestsTotal(metric.FailReasonTooManyRequests)
	WriteError(w, ClientErrRateLimitExceeded, http.StatusTooManyRequests)

	return false
}

func (r *Router) newFlowHandler(f *flow) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		start := time.Now()
		defer r.metrics.UpdateRequestsDuration(f.path, f.method, start)

		span := trace.SpanFromContext(req.Context())
		span.SetAttributes(attribute.String("http.route", f.path))

		requestID := getOrCreateRequestID(req)
		fingerprint := computeFingerprint(req, f.path)

		ctx := req.Context()
		ctx = withRoute(withRequestID(withFingerprint(ctx, fingerprint), requestID), f.path)

		req = req.WithContext(ctx)

		span.SetAttributes(
			attribute.String("kono.request.id", requestID),
			attribute.String("kono.request.fingerprint", fingerprint),
		)

		log := r.log.With(
			zap.String("request_id", requestID),
			zap.String("fingerprint", fingerprint),
		)

		if f.passthrough {
			r.handlePassthrough(w, req, f, log)
			return
		}

		kctx := newContext(req)

		if !r.executePlugins(sdk.PluginTypeRequest, w, kctx, f, log) {
			return
		}

		upstreamResponses := r.scatter.scatter(f, req)
		if upstreamResponses == nil {
			r.log.Error("request body too large", zap.Int("max_body_size", maxBodySize))
			WriteError(w, ClientErrPayloadTooLarge, http.StatusRequestEntityTooLarge)

			return
		}

		if r.log.Core().Enabled(zap.DebugLevel) {
			var ok, failed int

			for _, resp := range upstreamResponses {
				if resp.err != nil {
					failed++
				} else {
					ok++
				}
			}

			r.log.Debug("scatter finished",
				zap.Int("ok", ok),
				zap.Int("failed", failed),
			)
		}

		httpResp := r.buildResponse(req.Context(), upstreamResponses, f, log)
		defer func() { _ = httpResp.Body.Close() }()

		kctx.SetResponse(httpResp)

		if !r.executePlugins(sdk.PluginTypeResponse, w, kctx, f, log) {
			return
		}

		finalResp := kctx.Response() //nolint:bodyclose // synthetic response, closed by defer above
		if finalResp.Body != nil {
			bodyBytes, _ := io.ReadAll(finalResp.Body)
			finalResp.ContentLength = int64(len(bodyBytes))
			finalResp.Body = io.NopCloser(bytes.NewReader(bodyBytes))
		}

		w.Header().Set("Content-Length", strconv.Itoa(int(finalResp.ContentLength)))

		r.metrics.IncRequestsTotal(f.path, req.Method, finalResp.StatusCode)
		span.SetAttributes(attribute.Int("http.status_code", finalResp.StatusCode))
		if finalResp.StatusCode >= http.StatusInternalServerError {
			span.SetStatus(codes.Error, http.StatusText(finalResp.StatusCode))
		}

		r.copyResponse(w, finalResp)
	})
}

// executePlugins runs all plugins of the given type in order.
// On the first plugin error it writes a 500 to w and returns false —
// the caller must treat false as "response already sent, stop processing".
func (r *Router) executePlugins(pluginType sdk.PluginType, w http.ResponseWriter, kctx sdk.Context, f *flow, log *zap.Logger) bool {
	tracer := otel.Tracer(tracing.TracerName)

	for _, p := range f.plugins {
		if p.Type() != pluginType {
			continue
		}

		log.Debug("executing plugin",
			zap.String("type", pluginType.String()),
			zap.String("name", p.Info().Name),
		)

		ctx, span := tracer.Start(kctx.Request().Context(), "kono.plugin",
			trace.WithAttributes(
				attribute.String("kono.plugin.name", p.Info().Name),
				attribute.String("kono.plugin.type", pluginType.String()),
			),
		)

		kctx.SetRequest(kctx.Request().WithContext(ctx))
		err := p.Execute(kctx)
		span.End()

		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, "plugin execution failed")

			log.Error("plugin execution failed",
				zap.String("type", pluginType.String()),
				zap.String("name", p.Info().Name),
				zap.Error(err),
			)
			WriteError(w, ClientErrInternal, http.StatusInternalServerError)

			return false
		}
	}

	return true
}

func (r *Router) buildResponse(ctx context.Context, upstreamResponses []upstreamResponse, f *flow, log *zap.Logger) *http.Response {
	aggregated := r.aggregator.aggregate(f.upstreams, upstreamResponses, f.aggregation, log.Named("aggregated"))

	headers := aggregated.headers
	if headers == nil {
		headers = make(http.Header)
	}

	requestID := requestIDFromContext(ctx)
	fingerprint := fingerprintFromContext(ctx)

	headers.Set("X-Request-ID", requestID)
	headers.Set("X-Request-Fingerprint", fingerprint)
	headers.Set("Content-Type", "application/json; charset=utf-8")

	log.Debug("aggregation finished",
		zap.String("strategy", f.aggregation.strategy.String()),
		zap.Int("data_bytes", len(aggregated.data)),
		zap.Int("errors", len(aggregated.errors)),
		zap.Bool("partial", aggregated.partial),
	)

	status := r.statusFromErrors(aggregated.errors, aggregated.partial)
	body := r.buildResponseBody(aggregated, requestID)

	return &http.Response{
		Status:        fmt.Sprintf("%d %s", status, http.StatusText(status)),
		StatusCode:    status,
		ContentLength: int64(len(body)),
		Body:          io.NopCloser(bytes.NewBuffer(body)),
		Header:        headers,
	}
}

func (r *Router) buildResponseBody(aggregated aggregatedResponse, requestID string) []byte {
	switch {
	case len(aggregated.errors) > 0 && !aggregated.partial:
		return mustMarshal(ClientResponse{
			Data:   nil,
			Errors: aggregated.errors,
			Meta: ResponseMeta{
				RequestID: requestID,
				Partial:   false,
			},
		})
	case aggregated.partial:
		return mustMarshal(ClientResponse{
			Data:   aggregated.data,
			Errors: aggregated.errors,
			Meta: ResponseMeta{
				RequestID: requestID,
				Partial:   true,
			},
		})
	default:
		return mustMarshal(ClientResponse{
			Data:   aggregated.data,
			Errors: nil,
			Meta: ResponseMeta{
				RequestID: requestID,
				Partial:   false,
			},
		})
	}
}

func (r *Router) copyResponse(w http.ResponseWriter, resp *http.Response) {
	for k, vv := range resp.Header {
		if _, skip := hopByHopHeaders[k]; skip {
			continue
		}

		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}

	w.WriteHeader(resp.StatusCode)

	if resp.Body != nil {
		_, _ = io.Copy(w, resp.Body)
	}
}

// statusFromErrors maps aggregation errors to the most appropriate HTTP status code.
// partial takes precedence: even with errors, 206 signals a partial success.
func (r *Router) statusFromErrors(errors []ClientError, partial bool) int {
	if partial {
		return http.StatusPartialContent
	}

	if len(errors) == 0 {
		return http.StatusOK
	}

	var selected ClientError

	maxPriority := -1

	for _, e := range errors {
		if p := errorPriority(e); p > maxPriority {
			maxPriority = p
			selected = e
		}
	}

	switch selected {
	case ClientErrRateLimitExceeded:
		return http.StatusTooManyRequests
	case ClientErrPayloadTooLarge:
		return http.StatusRequestEntityTooLarge
	case ClientErrUpstreamBodyTooLarge, ClientErrUpstreamUnavailable, ClientErrUpstreamError, ClientErrUpstreamMalformed:
		return http.StatusBadGateway
	case ClientErrValueConflict:
		return http.StatusConflict
	case ClientErrAborted:
		// Client disconnected before the upstream responded; there is nothing
		// meaningful to send back, but we still need a status for logging.
		return http.StatusServiceUnavailable
	case ClientErrInternal:
		return http.StatusInternalServerError
	}

	return http.StatusInternalServerError
}

// ── Package-level helpers ─────────────────────────────────────────────────────

func mustMarshal(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		return []byte(`{"errors":[{"code":"INTERNAL"}]}`)
	}

	return b
}

// extractClientIP resolves the real client IP following the chain:
// X-Forwarded-For → X-Real-IP → RemoteAddr.
func extractClientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		return strings.TrimSpace(parts[0])
	}

	if xrip := r.Header.Get("X-Real-IP"); xrip != "" {
		return xrip
	}

	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}

	return r.RemoteAddr
}

var globalEntropy = ulid.Monotonic(rand.Reader, math.MaxInt64)

func getOrCreateRequestID(r *http.Request) string {
	if id := r.Header.Get("X-Request-ID"); id != "" {
		return id
	}

	return strings.ToLower(ulid.MustNew(ulid.Timestamp(time.Now()), globalEntropy).String())
}

func computeFingerprint(r *http.Request, flowPath string) string {
	var b strings.Builder

	b.WriteString(r.Method)
	b.WriteByte('|')
	b.WriteString(flowPath)
	b.WriteByte('|')

	headers := make([]string, 0, len(r.Header))
	for name := range r.Header {
		if _, skip := fingerprintIgnoredHeaders[name]; skip {
			continue
		}

		headers = append(headers, name)
	}

	sort.Strings(headers)
	b.WriteString(strings.Join(headers, ","))
	b.WriteByte('|')

	if r.URL.RawQuery != "" {
		qs := r.URL.Query()

		queries := make([]string, 0, len(qs))
		for name := range qs {
			queries = append(queries, name)
		}

		sort.Strings(queries)
		b.WriteString(strings.Join(queries, ","))
	}

	sum := sha256.Sum256([]byte(b.String()))

	return hex.EncodeToString(sum[:8])
}

const (
	errPriorityRateLimit   = 100
	errPriorityPayloadSize = 90
	errPriorityConflict    = 80
	errPriorityUpstream    = 50
	errPriorityInternal    = 10
)

// clientErrorPriority determines which error code wins when multiple are present.
// Higher value = more specific HTTP status code returned.
func errorPriority(e ClientError) int {
	switch e {
	case ClientErrRateLimitExceeded:
		return errPriorityRateLimit
	case ClientErrPayloadTooLarge, ClientErrUpstreamBodyTooLarge:
		return errPriorityPayloadSize
	case ClientErrValueConflict:
		return errPriorityConflict
	case ClientErrUpstreamUnavailable, ClientErrUpstreamError, ClientErrUpstreamMalformed, ClientErrAborted:
		return errPriorityUpstream
	case ClientErrInternal:
		return errPriorityInternal
	}

	return 0
}
