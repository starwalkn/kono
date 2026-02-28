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
	"strings"
	"time"

	"github.com/oklog/ulid/v2"
	"go.uber.org/zap"

	"github.com/starwalkn/kono/internal/metric"
	"github.com/starwalkn/kono/internal/ratelimit"
)

const luaWorkerSocketPath = "/tmp/kono-lua.sock"

type Router struct {
	dispatcher     dispatcher
	aggregator     aggregator
	Flows          []Flow
	TrustedProxies []string

	log     *zap.Logger
	metrics metric.Metrics

	rateLimiter *ratelimit.RateLimit
}

type RoutingConfigSet struct {
	Routing RoutingConfig
	Metrics MetricsConfig
}

func NewRouter(routingConfigSet RoutingConfigSet, log *zap.Logger) *Router {
	var (
		routingConfig = routingConfigSet.Routing
		metricsConfig = routingConfigSet.Metrics
	)

	router := initMinimalRouter(len(routingConfig.Flows), log)

	if metricsConfig.Enabled {
		switch metricsConfig.Provider {
		case "prometheus":
			router.metrics = metric.NewPrometheus()
		default:
			router.metrics = metric.NewNop()
		}
	}

	if routingConfig.RateLimiter.Enabled {
		router.rateLimiter = ratelimit.New(routingConfig.RateLimiter.Config)

		err := router.rateLimiter.Start()
		if err != nil {
			log.Fatal("failed to start ratelimit feature", zap.Error(err))
		}
	}

	trustedProxies := make([]*net.IPNet, 0, len(routingConfig.TrustedProxies))
	for _, proxy := range routingConfig.TrustedProxies {
		_, ipnet, err := net.ParseCIDR(proxy)
		if err != nil {
			log.Fatal("failed to parse trusted proxy CIDR", zap.Error(err))
		}

		trustedProxies = append(trustedProxies, ipnet)
	}

	for _, rcfg := range routingConfig.Flows {
		router.Flows = append(router.Flows, initRoute(rcfg, trustedProxies, log))
	}

	return router
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
//
//nolint:gocognit,funlen // refactor in future
func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	r.metrics.IncRequestsTotal()

	r.metrics.IncRequestsInFlight()
	defer r.metrics.DecRequestsInFlight()

	matchedFlow := r.match(req)
	if matchedFlow == nil {
		r.log.Error("no flow matched", zap.String("request_uri", req.URL.RequestURI()))
		r.metrics.IncFailedRequestsTotal(metric.FailReasonNoMatchedFlow)

		http.NotFound(w, req)

		return
	}

	if r.rateLimiter != nil {
		if !r.rateLimiter.Allow(extractClientIP(req)) {
			WriteError(w, ClientErrRateLimitExceeded, http.StatusTooManyRequests)
			return
		}
	}

	var flowHandler http.Handler = http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		start := time.Now()
		defer r.metrics.UpdateRequestsDuration(matchedFlow.Path, matchedFlow.Method, start)

		// Kono internal context
		tctx := newContext(req)

		requestID := getOrCreateRequestID(req)

		// Request-phase plugins
		for _, p := range matchedFlow.Plugins {
			if p.Type() != PluginTypeRequest {
				continue
			}

			r.log.Debug("executing request plugin", zap.String("name", p.Info().Name))

			if err := p.Execute(tctx); err != nil {
				r.log.Error("failed to execute request plugin", zap.String("name", p.Info().Name), zap.Error(err))
				WriteError(w, ClientErrInternal, http.StatusInternalServerError)

				return
			}
		}

		for _, script := range matchedFlow.Scripts {
			if script.Source == sourceFile {
				luaResp, err := r.luaSendGet(req.Context(), req, requestID)
				if err != nil {
					r.log.Error("failed to send-get request to lua worker", zap.String("request_id", requestID), zap.Error(err))
					WriteError(w, ClientErrInternal, http.StatusInternalServerError)

					return
				}

				switch luaResp.Action {
				case luaActionContinue:
					for k, v := range luaResp.Headers {
						if len(v) == 0 {
							continue
						}

						if len(v) > 1 {
							for _, vv := range v {
								w.Header().Add(k, vv)
							}
						} else {
							req.Header.Set(k, v[0])
						}
					}

					req.Method = luaResp.Method
					req.URL.Path = luaResp.Path
					req.URL.RawQuery = luaResp.Query
					req.Header = luaResp.Headers

					continue
				case luaActionAbort:
					r.log.Error("lua worker aborted request")
					WriteError(w, ClientErrAborted, luaResp.Status)

					return
				default:
					r.log.Error("unknown action from lua worker response", zap.String("action", luaResp.Action))
					WriteError(w, ClientErrInternal, http.StatusInternalServerError)

					return
				}
			}
		}

		// Upstream dispatch
		responses := r.dispatcher.dispatch(matchedFlow, req)
		if responses == nil {
			// Currently, responses can only be nil if the body size limit is exceeded or body read fails
			r.log.Error("request body too large", zap.Int("max_body_size", maxBodySize))
			WriteError(w, ClientErrPayloadTooLarge, http.StatusRequestEntityTooLarge)

			return
		}

		headers := http.Header{
			"X-Request-ID": []string{requestID},
			// TODO: Think about several encoding options
			"Content-Type": []string{"application/json; charset=utf-8"},
		}

		// Sets backends response headers
		for _, resp := range responses {
			// TODO: Consider a blacklist of returning headers
			for k, v := range resp.Headers {
				headers[k] = v
			}
		}

		r.log.Debug("dispatched responses", zap.Any("responses", responses))

		// Aggregate upstream responses
		aggregated := r.aggregator.aggregate(responses, matchedFlow.Aggregation)

		r.log.Debug("aggregated responses",
			zap.String("strategy", matchedFlow.Aggregation.Strategy),
			zap.Any("aggregated", aggregated),
		)

		var responseBody []byte

		status := http.StatusOK
		switch {
		case len(aggregated.Errors) > 0 && !aggregated.Partial:
			status = http.StatusInternalServerError

			responseBody = mustMarshal(ClientResponse{
				Data:   nil,
				Errors: aggregated.Errors,
			})
		case aggregated.Partial:
			status = http.StatusPartialContent

			responseBody = mustMarshal(ClientResponse{
				Data:   aggregated.Data,
				Errors: aggregated.Errors,
			})
		default:
			responseBody = mustMarshal(ClientResponse{
				Data:   aggregated.Data,
				Errors: nil,
			})
		}

		resp := &http.Response{
			Status:     fmt.Sprintf("%d %s", status, http.StatusText(status)),
			StatusCode: status,
			Body:       io.NopCloser(bytes.NewReader(responseBody)),
			Header:     headers,
		}

		// Sets the response to the internal context for plugins
		tctx.SetResponse(resp)

		// Response-phase plugins
		for _, p := range matchedFlow.Plugins {
			if p.Type() != PluginTypeResponse {
				continue
			}

			r.log.Debug("executing response plugin", zap.String("name", p.Info().Name))

			if err := p.Execute(tctx); err != nil {
				r.log.Error("failed to execute response plugin", zap.String("name", p.Info().Name), zap.Error(err))
				WriteError(w, ClientErrInternal, http.StatusInternalServerError)

				return
			}
		}

		r.metrics.IncResponsesTotal(matchedFlow.Path, tctx.Response().StatusCode) //nolint:bodyclose // body closes in copyResponse

		// Write final output.
		copyResponse(w, tctx.Response()) //nolint:bodyclose // body closes in copyResponse
	})

	for i := len(matchedFlow.Middlewares) - 1; i >= 0; i-- {
		flowHandler = matchedFlow.Middlewares[i].Handler(flowHandler)
	}

	flowHandler.ServeHTTP(w, req)
}

// match matches the given request to a flow.
func (r *Router) match(req *http.Request) *Flow {
	for i := range r.Flows {
		flow := &r.Flows[i]

		if flow.Method != "" && !strings.EqualFold(flow.Method, req.Method) {
			continue
		}

		if flow.Path != "" && req.URL.Path == flow.Path {
			return flow
		}
	}

	return nil
}

// copyResponse copies the *http.Response to the http.ResponseWriter.
func copyResponse(w http.ResponseWriter, resp *http.Response) {
	for k, vv := range resp.Header {
		for _, v := range vv {
			w.Header().Set(k, v)
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
		return []byte(`{"errors":[{"code":"internal","message":"internal error"}]}`)
	}

	return b
}

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

func getOrCreateRequestID(r *http.Request) string {
	requestID := r.Header.Get("X-Request-ID")
	if requestID != "" {
		return requestID
	}

	t := time.Now()
	entropy := ulid.Monotonic(rand.Reader, math.MaxInt64)

	return strings.ToLower(ulid.MustNew(ulid.Timestamp(t), entropy).String())
}

// luaSendGet sends request data from the client to LuaWorker over a unix socket
// and returns a modified request in the response with the action field.
//
// Action is a special field from LuaWorker that indicates the latest gateway action
// with a request and can have one of two values - "continue" or "abort".
func (r *Router) luaSendGet(ctx context.Context, req *http.Request, requestID string) (*LuaJSONResponse, error) {
	dialer := &net.Dialer{}

	conn, err := dialer.DialContext(ctx, "unix", luaWorkerSocketPath)
	if err != nil {
		return nil, fmt.Errorf("failed to dial lua worker socket: %w", err)
	}
	defer conn.Close()

	luaReq := LuaJSONRequest{
		RequestID: requestID,
		Method:    req.Method,
		Path:      req.URL.Path,
		Query:     req.URL.RawQuery,
		Headers:   req.Header.Clone(),
		Body:      nil,
		ClientIP:  extractClientIP(req),
	}

	msg, err := json.Marshal(&luaReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal lua request: %w", err)
	}

	if _, err = conn.Write(msg); err != nil {
		return nil, fmt.Errorf("failed to write data to lua worker socket: %w", err)
	}

	if len(msg) > luaMsgMaxSize {
		return nil, fmt.Errorf("request message exceeds max size: %d bytes (limit %d)", len(msg), luaMsgMaxSize)
	}

	totalSize64 := int64(len(msg)) + int64(luaMsgExtraBufSize)
	if totalSize64 < 0 || totalSize64 > int64(math.MaxInt) {
		return nil, fmt.Errorf("calculated size of the response buffer overflows: %d", totalSize64)
	}

	rawLuaResp := make([]byte, totalSize64)

	_, err = conn.Read(rawLuaResp)
	if err != nil {
		return nil, fmt.Errorf("failed to read data from lua worker socket: %w", err)
	}

	var luaResp = new(LuaJSONResponse)
	if err = json.Unmarshal(rawLuaResp, luaResp); err != nil {
		return nil, fmt.Errorf("cannot unmarshal response from lua worker socket: %w", err)
	}

	return luaResp, nil
}
