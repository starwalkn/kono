package tokka

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"slices"
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
	Path                string
	Method              string
	Upstreams           []Upstream
	Aggregate           string
	Transform           string
	AllowPartialResults bool
	Plugins             []Plugin
	Middlewares         []Middleware
}

func newDefaultRouter(routesCount int, log *zap.Logger) *Router {
	metrics := metric.NewVictoria()

	return &Router{
		dispatcher: &defaultDispatcher{
			log:     log.Named("dispatcher"),
			metrics: metrics,
		},
		aggregator: &defaultAggregator{
			log: log.Named("aggregator"),
		},
		Routes:      make([]Route, 0, routesCount),
		log:         log,
		metrics:     metrics,
		rateLimiter: nil,
	}
}

type RouterConfigSet struct {
	Version     string
	Routes      []RouteConfig
	Middlewares []MiddlewareConfig
	Features    []FeatureConfig
}

func NewRouter(routerConfigSet RouterConfigSet, log *zap.Logger) *Router {
	var (
		routeConfigs            = routerConfigSet.Routes
		globalMiddlewareConfigs = routerConfigSet.Middlewares
		featureConfigs          = routerConfigSet.Features
	)

	// Global middlewares.
	globalMiddlewareIndices, globalMiddlewares := initGlobalMiddlewares(globalMiddlewareConfigs, log)

	router := newDefaultRouter(len(routeConfigs), log)

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

	for _, rcfg := range routeConfigs {
		// Per-route middlewares.
		routeMiddlewares := make([]Middleware, 0, len(rcfg.Middlewares))
		for _, mcfg := range rcfg.Middlewares {
			soMiddleware := loadMiddlewareFromSO(mcfg.Path, mcfg.Config, log)
			if soMiddleware == nil {
				log.Error(
					"cannot load middleware from .so",
					zap.String("name", mcfg.Name),
				)

				if !mcfg.CanFailOnLoad {
					panic("cannot load middleware from .so")
				}

				continue
			}

			log.Info(
				"middleware initialized",
				zap.String("name", soMiddleware.Name()),
				zap.String("route", rcfg.Method+" "+rcfg.Path),
			)

			if mcfg.Override {
				if idx, ok := globalMiddlewareIndices[soMiddleware.Name()]; ok {
					globalMiddlewares[idx] = soMiddleware
					continue
				}
			}

			routeMiddlewares = append(routeMiddlewares, soMiddleware)
		}

		middlewares := append(globalMiddlewares, routeMiddlewares...) //nolint:gocritic // we do not want to modify globalMiddlewares here

		route := Route{
			Path:                rcfg.Path,
			Method:              rcfg.Method,
			Upstreams:           initUpstreams(rcfg.Upstreams),
			Aggregate:           rcfg.Aggregate,
			Transform:           rcfg.Transform,
			AllowPartialResults: rcfg.AllowPartialResults,
			Plugins:             initPlugins(rcfg.Plugins, log),
			Middlewares:         middlewares,
		}

		router.Routes = append(router.Routes, route)
	}

	return router
}

func initGlobalMiddlewares(cfgs []MiddlewareConfig, log *zap.Logger) (map[string]int, []Middleware) {
	globalMiddlewareIndices := make(map[string]int)
	globalMiddlewares := make([]Middleware, 0, len(cfgs))

	for i, cfg := range cfgs {
		soMiddleware := loadMiddlewareFromSO(cfg.Path, cfg.Config, log)
		if soMiddleware == nil {
			log.Error(
				"cannot load middleware from .so",
				zap.String("name", cfg.Name),
			)

			if !cfg.CanFailOnLoad {
				panic("cannot load middleware from .so")
			}

			continue
		}

		globalMiddlewares = append(globalMiddlewares, soMiddleware)
		globalMiddlewareIndices[soMiddleware.Name()] = i
	}

	return globalMiddlewareIndices, globalMiddlewares
}

func initUpstreams(cfgs []UpstreamConfig) []Upstream {
	upstreams := make([]Upstream, 0, len(cfgs))

	//nolint:mnd // be configurable in future
	transport := &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 10,
		IdleConnTimeout:     90 * time.Second,
		ForceAttemptHTTP2:   true,
	}

	for _, cfg := range cfgs {
		policy := UpstreamPolicy{
			AllowedStatuses:     cfg.Policy.AllowedStatuses,
			RequireBody:         cfg.Policy.RequireBody,
			MapStatusCodes:      cfg.Policy.MapStatusCodes,
			MaxResponseBodySize: cfg.Policy.MaxResponseBodySize,
			RetryPolicy: UpstreamRetryPolicy{
				MaxRetries:      cfg.Policy.RetryConfig.MaxRetries,
				RetryOnStatuses: cfg.Policy.RetryConfig.RetryOnStatuses,
				BackoffDelay:    cfg.Policy.RetryConfig.BackoffDelay,
			},
		}

		upstream := &httpUpstream{
			name:                fmt.Sprintf("%s_%s", cfg.Method, cfg.URL),
			url:                 cfg.URL,
			method:              cfg.Method,
			timeout:             cfg.Timeout,
			headers:             cfg.Headers,
			forwardHeaders:      cfg.ForwardHeaders,
			forwardQueryStrings: cfg.ForwardQueryStrings,
			policy:              policy,
			client: &http.Client{
				Transport: transport,
			},
		}

		upstreams = append(upstreams, upstream)
	}

	return upstreams
}

func initPlugins(cfgs []PluginConfig, log *zap.Logger) []Plugin {
	plugins := make([]Plugin, 0, len(cfgs))

	for _, cfg := range cfgs {
		cfn := func(plugin Plugin) bool {
			return plugin.Name() == cfg.Name
		}

		if slices.ContainsFunc(plugins, cfn) {
			continue
		}

		soPlugin := loadPluginFromSO(cfg.Path, cfg.Config, log)
		if soPlugin == nil {
			log.Error(
				"cannot load plugin from .so",
				zap.String("name", cfg.Name),
				zap.String("path", cfg.Path),
			)
			continue
		}

		log.Info(
			"plugin initialized",
			zap.String("name", soPlugin.Name()),
		)

		plugins = append(plugins, soPlugin)
	}

	return plugins
}

/*
ServeHTTP is the incoming requests pipeline:

	├─ execute middlewares
	├─ match route
	├─ execute request plugins
	├─ dispatch upstreams
	├─ aggregate response
	├─ execute response plugins
	└─ write response
*/
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
			http.Error(w, jsonErrRateLimitExceeded, http.StatusTooManyRequests)
			return
		}
	}

	rt := r.match(req)
	if rt == nil {
		r.log.Error("no route found", zap.String("request_uri", req.URL.RequestURI()))
		r.metrics.IncFailedRequestsTotal(metric.FailReasonNoMatchedRoute)

		http.NotFound(w, req)

		return
	}

	var routeHandler http.Handler = http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		tctx := newContext(req) // Tokka context.

		// Request-phase plugins.
		for _, p := range rt.Plugins {
			if p.Type() != PluginTypeRequest {
				continue
			}

			r.log.Debug("executing request plugin", zap.String("name", p.Name()))

			p.Execute(tctx)
		}

		// Upstream dispatch.
		responses := r.dispatcher.dispatch(rt, req)
		if responses == nil {
			r.log.Error("request body too large", zap.Int("max_body_size", maxBodySize))
			http.Error(w, jsonErrPayloadTooLarge, http.StatusRequestEntityTooLarge)

			return
		}

		r.log.Debug("dispatched responses", zap.Any("responses", responses))

		aggregated := r.aggregator.aggregate(responses, rt.Aggregate, rt.AllowPartialResults)

		r.log.Debug("aggregated responses",
			zap.String("strategy", rt.Aggregate),
			zap.Any("aggregated", aggregated),
		)

		// Response-phase plugins.
		resp := &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewReader(aggregated)),
			Header:     make(http.Header),
		}

		tctx.SetResponse(resp)
		for _, p := range rt.Plugins {
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

	for i := len(rt.Middlewares) - 1; i >= 0; i-- {
		routeHandler = rt.Middlewares[i].Handler(routeHandler)
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
			w.Header().Add(k, v)
		}
	}

	w.WriteHeader(resp.StatusCode)
	if resp.Body != nil {
		_, _ = io.Copy(w, resp.Body)
		_ = resp.Body.Close()
	}
}
