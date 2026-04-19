package kono

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/oklog/ulid/v2"
	"go.uber.org/zap"

	"github.com/starwalkn/kono/internal/metric"
	"github.com/starwalkn/kono/internal/ratelimit"
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

type Router struct {
	chiRouter  *chi.Mux
	scatter    scatter
	aggregator aggregator
	flows      []flow

	log         *zap.Logger
	metrics     metric.Metrics
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

	clientIP := extractClientIP(req)
	req = req.WithContext(withClientIP(req.Context(), clientIP))

	if !r.allowRequest(w, clientIP) {
		return
	}

	r.chiRouter.ServeHTTP(w, req)
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

		requestID := getOrCreateRequestID(req)
		req = req.WithContext(withRequestID(req.Context(), requestID))
		req = req.WithContext(withRoute(req.Context(), f.path))

		log := r.log.With(zap.String("request_id", requestID))

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
			r.log.Debug("dispatched responses", zap.Any("responses", upstreamResponses))
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
		r.copyResponse(w, finalResp)
	})
}

// executePlugins runs all plugins of the given type in order.
// On the first plugin error it writes a 500 to w and returns false —
// the caller must treat false as "response already sent, stop processing".
func (r *Router) executePlugins(pluginType sdk.PluginType, w http.ResponseWriter, kctx sdk.Context, f *flow, log *zap.Logger) bool {
	for _, p := range f.plugins {
		if p.Type() != pluginType {
			continue
		}

		log.Debug("executing plugin",
			zap.String("type", pluginPhaseName(pluginType)),
			zap.String("name", p.Info().Name),
		)

		if err := p.Execute(kctx); err != nil {
			log.Error("plugin execution failed",
				zap.String("type", pluginPhaseName(pluginType)),
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

	headers.Set("X-Request-ID", requestID)
	headers.Set("Content-Type", "application/json; charset=utf-8")

	if log.Core().Enabled(zap.DebugLevel) {
		log.Debug("aggregated responses",
			zap.String("strategy", f.aggregation.strategy.String()),
			zap.Any("aggregated", aggregated),
		)
	}

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

func pluginPhaseName(phase sdk.PluginType) string {
	switch phase {
	case sdk.PluginTypeRequest:
		return "request"
	case sdk.PluginTypeResponse:
		return "response"
	default:
		return "unknown"
	}
}
