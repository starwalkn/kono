package tokka

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"go.uber.org/zap"

	"github.com/starwalkn/tokka/internal/metric"
	"github.com/starwalkn/tokka/internal/ratelimit"
)

type Router struct {
	dispatcher dispatcher
	aggregator aggregator
	Routes     []Route

	log     *zap.Logger
	metrics metric.Metrics

	rateLimiter *ratelimit.RateLimit
}

type Route struct {
	Path                 string
	Method               string
	Upstreams            []Upstream
	Aggregation          AggregationConfig
	MaxParallelUpstreams int64
	Plugins              []Plugin
	Middlewares          []Middleware
}

type RouterConfigSet struct {
	Version     string
	Routes      []RouteConfig
	Middlewares []MiddlewareConfig
	Features    []FeatureConfig
	Metrics     MetricsConfig
}

func NewRouter(routerConfigSet RouterConfigSet, log *zap.Logger) *Router {
	var (
		routeConfigs            = routerConfigSet.Routes
		globalMiddlewareConfigs = routerConfigSet.Middlewares
		featureConfigs          = routerConfigSet.Features
		metricsConfig           = routerConfigSet.Metrics
	)

	router := initMinimalRouter(len(routeConfigs), log)

	if metricsConfig.Enabled {
		switch metricsConfig.Provider {
		case "victoriametrics":
			router.metrics = metric.NewVictoria()
		default:
			router.metrics = metric.NewNop()
		}
	}

	for _, fcfg := range featureConfigs {
		//nolint:gocritic // for the future
		switch fcfg.Name {
		case "ratelimit":
			router.rateLimiter = ratelimit.New(fcfg.Config)

			err := router.rateLimiter.Start()
			if err != nil {
				log.Fatal("failed to start ratelimit feature", zap.Error(err))
			}
		}
	}

	// Global middlewares.
	globalMiddlewareIndices, globalMiddlewares := initGlobalMiddlewares(globalMiddlewareConfigs, log)

	for _, rcfg := range routeConfigs {
		router.Routes = append(router.Routes, initRoute(rcfg, globalMiddlewares, globalMiddlewareIndices, log))
	}

	return router
}

// ServeHTTP handles incoming HTTP requests through the full router pipeline.
//
// The processing steps are:
//
// 1. Rate limiting (if enabled) – rejects requests exceeding allowed limits.
// 2. Route matching – finds a Route that matches the request method and path.
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
	start := time.Now()

	r.metrics.IncRequestsInFlight()
	defer r.metrics.DecRequestsInFlight()

	defer r.metrics.IncRequestsTotal()
	defer r.metrics.UpdateRequestsDuration(start)

	if r.rateLimiter != nil {
		ip := req.Header.Get("X-Forwarded-For")
		if ip == "" {
			ip = req.RemoteAddr
		}

		if !r.rateLimiter.Allow(ip) {
			WriteError(w, ErrorCodeRateLimitExceeded, "rate limit exceeded", req.Header.Get("X-Request-ID"), http.StatusTooManyRequests)
			return
		}
	}

	matchedRoute := r.match(req)
	if matchedRoute == nil {
		r.log.Error("no route matched", zap.String("request_uri", req.URL.RequestURI()))
		r.metrics.IncFailedRequestsTotal(metric.FailReasonNoMatchedRoute)

		http.NotFound(w, req)

		return
	}

	var routeHandler http.Handler = http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		tctx := newContext(req) // Tokka context.

		requestID := req.Header.Get("X-Request-ID")

		// Request-phase plugins.
		for _, p := range matchedRoute.Plugins {
			if p.Type() != PluginTypeRequest {
				continue
			}

			r.log.Debug("executing request plugin", zap.String("name", p.Name()))

			p.Execute(tctx)
		}

		// Upstream dispatch.
		responses := r.dispatcher.dispatch(matchedRoute, req)
		if responses == nil {
			// Currently, responses can only be nil if the body size limit is exceeded or body read fails.
			r.log.Error("request body too large", zap.Int("max_body_size", maxBodySize))
			WriteError(w, ErrorCodePayloadTooLarge, "request body too large", requestID, http.StatusRequestEntityTooLarge)

			return
		}

		r.log.Debug("dispatched responses", zap.Any("responses", responses))

		// Aggregate upstream responses.
		aggregated := r.aggregator.aggregate(responses, matchedRoute.Aggregation)
		attachRequestID(aggregated.Errors, requestID)

		r.log.Debug("aggregated responses",
			zap.String("strategy", matchedRoute.Aggregation.Strategy),
			zap.Any("aggregated", aggregated),
		)

		var responseBody []byte

		status := http.StatusOK
		switch {
		case len(aggregated.Errors) > 0 && !aggregated.Partial:
			status = http.StatusInternalServerError

			responseBody = mustMarshal(JSONResponse{
				Data:   nil,
				Errors: aggregated.Errors,
			})
		case aggregated.Partial:
			status = http.StatusPartialContent

			responseBody = mustMarshal(JSONResponse{
				Data:   aggregated.Data,
				Errors: aggregated.Errors,
			})
		default:
			responseBody = mustMarshal(JSONResponse{
				Data:   aggregated.Data,
				Errors: nil,
			})
		}

		headers := http.Header{
			"X-Request-ID": []string{requestID},
			"Content-Type": []string{"application/json; charset=utf-8"},
		}

		// Response-phase plugins.
		resp := &http.Response{
			Status:     fmt.Sprintf("%d %s", status, http.StatusText(status)),
			StatusCode: status,
			Body:       io.NopCloser(bytes.NewReader(responseBody)),
			Header:     headers,
		}

		tctx.SetResponse(resp)
		for _, p := range matchedRoute.Plugins {
			if p.Type() != PluginTypeResponse {
				continue
			}

			r.log.Debug("executing response plugin", zap.String("name", p.Name()))

			p.Execute(tctx)
		}

		r.metrics.IncResponsesTotal(tctx.Response().StatusCode) //nolint:bodyclose // body closes in copyResponse

		// Write final output.
		copyResponse(w, tctx.Response()) //nolint:bodyclose // body closes in copyResponse
	})

	for i := len(matchedRoute.Middlewares) - 1; i >= 0; i-- {
		routeHandler = matchedRoute.Middlewares[i].Handler(routeHandler)
	}

	routeHandler.ServeHTTP(w, req)
}

// match matches the given request to a route.
func (r *Router) match(req *http.Request) *Route {
	for _, route := range r.Routes {
		if route.Method != "" && route.Method != req.Method {
			continue
		}

		if route.Path != "" && req.URL.Path == route.Path {
			return &route
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

func attachRequestID(errs []JSONError, requestID string) {
	for i := range errs {
		errs[i].RequestID = requestID
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
