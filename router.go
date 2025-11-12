package tokka

import (
	"bytes"
	"io"
	"net/http"
	"slices"

	"go.uber.org/zap"

	"github.com/starwalkn/tokka/internal/logger"
	"github.com/starwalkn/tokka/internal/plugin/contract"
)

type Router struct {
	dispatcher dispatcher
	aggregator aggregator
	Routes     []Route

	log *zap.Logger
}

type Route struct {
	Path                string
	Method              string
	Backends            []Backend
	Aggregate           string
	Transform           string
	AllowPartialResults bool
	Plugins             []Plugin
	Middlewares         []Middleware
}

type Backend struct {
	URL                 string
	Method              string
	Timeout             int64
	Headers             map[string]string
	ForwardHeaders      []string
	ForwardQueryStrings []string
}

func NewRouter(cfgs []RouteConfig, globalMiddlewareCfgs []MiddlewareConfig) *Router {
	log := logger.New(true)

	// --- global middlewares ---
	globalMiddlewareIndices, globalMiddlewares := initGlobalMiddlewares(globalMiddlewareCfgs, log)

	router := &Router{
		dispatcher: &defaultDispatcher{
			client: &http.Client{},
			log:    log.Named("dispatcher"),
		},
		aggregator: &defaultAggregator{
			log: log.Named("aggregator"),
		},
		Routes: nil,
		log:    log,
	}

	routes := make([]Route, 0, len(cfgs))

	for _, rcfg := range cfgs {
		// --- backends ---
		backends := initBackends(rcfg.Backends)

		// --- plugins ---
		plugins := initPlugins(rcfg.Plugins, log)

		// --- route middlewares ---
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

		// --- route ---
		route := Route{
			Path:                rcfg.Path,
			Method:              rcfg.Method,
			Backends:            backends,
			Aggregate:           rcfg.Aggregate,
			Transform:           rcfg.Transform,
			AllowPartialResults: rcfg.AllowPartialResults,
			Plugins:             plugins,
			Middlewares:         middlewares,
		}

		routes = append(routes, route)
	}

	router.Routes = routes

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

func initBackends(cfgs []BackendConfig) []Backend {
	backends := make([]Backend, 0, len(cfgs))

	for _, cfg := range cfgs {
		//nolint:staticcheck // backend structure may change
		backend := Backend{
			URL:                 cfg.URL,
			Method:              cfg.Method,
			Timeout:             cfg.Timeout,
			Headers:             cfg.Headers,
			ForwardHeaders:      cfg.ForwardHeaders,
			ForwardQueryStrings: cfg.ForwardQueryStrings,
		}

		backends = append(backends, backend)
	}

	return backends
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

	├─ execute core plugins
	├─ execute middlewares
	├─ match route
	├─ execute request plugins
	├─ dispatch backends
	├─ aggregate response
	├─ execute response plugins
	└─ write response
*/
func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	// --- 0. Global (core) plugins, e.g. rate limiter ---
	if rl := getActiveCorePlugin("ratelimit"); rl != nil { //nolint:nolintlint,nestif
		if limiter, ok := rl.(contract.RateLimit); ok {
			ip := req.Header.Get("X-Forwarded-For")
			if ip == "" {
				ip = req.RemoteAddr
			}

			if !limiter.Allow(ip) {
				http.Error(w, jsonErrRateLimitExceeded, http.StatusTooManyRequests)
				return
			}
		}
	}

	rt := r.match(req)
	if rt == nil {
		r.log.Error("no route found", zap.String("request_uri", req.URL.RequestURI()))
		http.NotFound(w, req)

		return
	}

	var routeHandler http.Handler = http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		pctx := newContext(req, rt)

		// --- 1. Request-phase plugins ---
		for _, p := range rt.Plugins {
			if p.Type() != PluginTypeRequest {
				continue
			}

			r.log.Debug("executing request plugin", zap.String("name", p.Name()))

			p.Execute(pctx)
		}

		// --- 2. Backend dispatch ---
		responses := r.dispatcher.dispatch(rt, req)

		r.log.Debug("dispatched responses", zap.Any("responses", responses))

		aggregated := r.aggregator.aggregate(responses, rt.Aggregate, rt.AllowPartialResults)

		r.log.Debug("aggregated responses",
			zap.String("strategy", rt.Aggregate),
			zap.Any("aggregated", aggregated),
		)

		// --- 3. Response-phase plugins ---
		resp := &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewReader(aggregated)),
			Header:     make(http.Header),
		}

		pctx.SetResponse(resp)
		for _, p := range rt.Plugins {
			if p.Type() != PluginTypeResponse {
				continue
			}

			r.log.Debug("executing response plugin", zap.String("name", p.Name()))

			p.Execute(pctx)
		}

		// --- 4. Write final output ---
		copyResponse(w, pctx.Response()) //nolint:bodyclose // body closes in copyResponse
	})

	for i := len(rt.Middlewares) - 1; i >= 0; i-- {
		routeHandler = rt.Middlewares[i].Handler(routeHandler)
	}

	routeHandler.ServeHTTP(w, req)
}

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
