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
)

var hopByHopHeaders = map[string]struct{}{
	"Content-Length":    {},
	"Transfer-Encoding": {},
	"Connection":        {},
	"Trailer":           {},
	"Date":              {},
	"Server":            {},
}

type Router struct {
	chiRouter *chi.Mux

	lumos          *lumos
	dispatcher     dispatcher
	aggregator     aggregator
	Flows          []Flow
	TrustedProxies []string

	log     *zap.Logger
	metrics metric.Metrics

	rateLimiter *ratelimit.RateLimit
}

// ServeHTTP handles incoming HTTP requests through the full router pipeline.
//
// The processing steps are:
//
// 1. Rate limiting (if enabled) – rejects requests exceeding allowed limits.
// 2. Flow matching – finds a Flow that matches the request method and path.
//   - If no route is found, responds with 404.
//
// 3. Middleware execution – wraps the route handler with all configured middlewares in reverse order.
// 4. Request-phase plugins – executed before upstream dispatch. Can modify the request context.
// 5. Upstream dispatch – sends the request to all configured upstreams via the dispatcher.
//   - If the dispatch fails (e.g., body too large), responds with an appropriate error.
//     6. Response aggregation – combines multiple upstream responses according to the route's aggregation strategy
//     ("merge" or "array") and the allowPartialResults flag.
//     7. Response-phase plugins – executed after aggregation, can modify headers or the response body.
//     8. Response writing – writes the aggregated response, appropriate HTTP status code, and headers
//     to the client.
//
// Status code determination:
//
// - 200 OK: all upstreams succeeded, no errors.
// - 206 Partial Content: allowPartialResults=true, at least one upstream failed.
// - 500 Internal Server Error: allowPartialResults=false, at least one upstream failed.
//
// The final response always includes a JSON body with `data` and `errors` fields, and a `X-Request-ID` header.
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

func (r *Router) newFlowHandler(flow *Flow) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		start := time.Now()
		defer r.metrics.UpdateRequestsDuration(flow.Path, flow.Method, start)

		requestID := getOrCreateRequestID(req)
		req = req.WithContext(withRequestID(req.Context(), requestID))
		req = req.WithContext(withRoute(req.Context(), flow.Path))

		log := r.log.With(zap.String("request_id", requestID))

		kctx := newContext(req)

		if !r.runRequestPlugins(w, kctx, flow, log) {
			return
		}

		req, ok := r.runLuaScripts(w, req, flow, log)
		if !ok {
			return
		}

		upstreamResponses := r.dispatcher.dispatch(flow, req)
		if upstreamResponses == nil {
			r.log.Error("request body too large", zap.Int("max_body_size", maxBodySize))
			WriteError(w, ClientErrPayloadTooLarge, http.StatusRequestEntityTooLarge)

			return
		}

		if r.log.Core().Enabled(zap.DebugLevel) {
			r.log.Debug("dispatched responses", zap.Any("responses", upstreamResponses))
		}

		httpResp := r.buildResponse(req.Context(), upstreamResponses, flow, log) //nolint:bodyclose // closes in copyResponse

		kctx.SetResponse(httpResp)

		if !r.runResponsePlugins(w, kctx, flow, log) {
			return
		}

		w.Header().Set("Content-Length", strconv.Itoa(int(kctx.Response().ContentLength))) //nolint:bodyclose // closes in copyResponse

		r.metrics.IncRequestsTotal(flow.Path, req.Method, kctx.Response().StatusCode) //nolint:bodyclose // closes in copyResponse
		copyResponse(w, kctx.Response())                                              //nolint:bodyclose // closes in copyResponse
	})
}

func (r *Router) runRequestPlugins(w http.ResponseWriter, kctx Context, flow *Flow, log *zap.Logger) bool {
	for _, p := range flow.Plugins {
		if p.Type() != PluginTypeRequest {
			continue
		}

		log.Debug("executing request plugin", zap.String("name", p.Info().Name))

		if err := p.Execute(kctx); err != nil {
			log.Error("failed to execute request plugin",
				zap.String("name", p.Info().Name),
				zap.Error(err),
			)
			WriteError(w, ClientErrInternal, http.StatusInternalServerError)

			return false
		}
	}

	return true
}

func (r *Router) runResponsePlugins(w http.ResponseWriter, kctx Context, flow *Flow, log *zap.Logger) bool {
	for _, p := range flow.Plugins {
		if p.Type() != PluginTypeResponse {
			continue
		}

		log.Debug("executing response plugin", zap.String("name", p.Info().Name))

		if err := p.Execute(kctx); err != nil {
			log.Error("failed to execute response plugin",
				zap.String("name", p.Info().Name),
				zap.Error(err),
			)
			WriteError(w, ClientErrInternal, http.StatusInternalServerError)

			return false
		}
	}

	return true
}

func (r *Router) runLuaScripts(w http.ResponseWriter, req *http.Request, flow *Flow, log *zap.Logger) (*http.Request, bool) {
	for _, script := range flow.Scripts {
		if script.Source != sourceFile {
			continue
		}

		lumosResp, err := r.lumos.SendGet(req.Context(), req, script.Path)
		if err != nil {
			log.Error("failed to execute lua script",
				zap.String("script", script.Path),
				zap.Error(err),
			)
			WriteError(w, ClientErrInternal, http.StatusInternalServerError)

			return nil, false
		}

		switch lumosResp.Action {
		case lumosActionContinue:
			req = applyLumosResponse(req, lumosResp)
		case lumosActionAbort:
			log.Error("lumos aborted request")
			WriteError(w, ClientErrAborted, lumosResp.HTTPStatus)

			return nil, false
		default:
			log.Error("unknown action from lumos",
				zap.String("action", lumosResp.Action),
			)
			WriteError(w, ClientErrInternal, http.StatusInternalServerError)

			return nil, false
		}
	}

	return req, true
}

func applyLumosResponse(req *http.Request, lumosResp *LumosJSONResponse) *http.Request {
	req.Method = lumosResp.Method
	req.URL.Path = lumosResp.Path
	req.URL.RawQuery = lumosResp.Query
	req.Header = lumosResp.Headers

	return req
}

func (r *Router) buildResponse(ctx context.Context, upstreamResponses []UpstreamResponse, flow *Flow, log *zap.Logger) *http.Response {
	aggregated := r.aggregator.aggregate(flow.Upstreams, upstreamResponses, flow.Aggregation, log.Named("aggregated"))

	headers := aggregated.Headers
	if headers == nil {
		headers = make(http.Header)
	}

	headers.Set("X-Request-ID", requestIDFromContext(ctx))
	headers.Set("Content-Type", "application/json; charset=utf-8")

	if log.Core().Enabled(zap.DebugLevel) {
		log.Debug("aggregated responses",
			zap.String("strategy", flow.Aggregation.Strategy.String()),
			zap.Any("aggregated", aggregated),
		)
	}

	status := statusFromErrors(aggregated.Errors, aggregated.Partial)
	body := buildResponseBody(aggregated)

	return &http.Response{
		Status:        fmt.Sprintf("%d %s", status, http.StatusText(status)),
		StatusCode:    status,
		ContentLength: int64(len(body)),
		Body:          io.NopCloser(bytes.NewBuffer(body)),
		Header:        headers,
	}
}

func buildResponseBody(aggregated AggregatedResponse) []byte {
	switch {
	case len(aggregated.Errors) > 0 && !aggregated.Partial:
		return mustMarshal(ClientResponse{
			Data:   nil,
			Errors: aggregated.Errors,
		})
	case aggregated.Partial:
		return mustMarshal(ClientResponse{
			Data:   aggregated.Data,
			Errors: aggregated.Errors,
		})
	default:
		return mustMarshal(ClientResponse{
			Data:   aggregated.Data,
			Errors: nil,
		})
	}
}

// copyResponse copies the *http.Response to the http.ResponseWriter.
func copyResponse(w http.ResponseWriter, resp *http.Response) {
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
		_ = resp.Body.Close()
	}
}

// mustMarshal marshals the given value to JSON.
func mustMarshal(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		return []byte(`{"errors":[{"code":"INTERNAL"}]}`)
	}

	return b
}

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

func statusFromErrors(errors []ClientError, partial bool) int {
	if partial {
		return http.StatusPartialContent
	}

	if len(errors) == 0 {
		return http.StatusOK
	}

	var selected ClientError
	maxPriority := -1

	for _, e := range errors {
		p := errorPriority(e)
		if p > maxPriority {
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
	default:
		return http.StatusInternalServerError
	}
}

//nolint:mnd // error priority be configurable in future releases
func errorPriority(e ClientError) int {
	switch e {
	case ClientErrRateLimitExceeded:
		return 100
	case ClientErrPayloadTooLarge,
		ClientErrUpstreamBodyTooLarge:
		return 90
	case ClientErrValueConflict:
		return 80
	case ClientErrUpstreamUnavailable,
		ClientErrUpstreamError,
		ClientErrUpstreamMalformed:
		return 50
	case ClientErrInternal:
		return 10
	default:
		return 0
	}
}
