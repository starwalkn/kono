package tokka

import (
	"fmt"
	"net/http"
	"slices"
	"time"

	"go.uber.org/zap"

	"github.com/starwalkn/tokka/internal/circuitbreaker"
	"github.com/starwalkn/tokka/internal/metric"
)

func initMinimalRouter(routesCount int, log *zap.Logger) *Router {
	metrics := metric.NewNop()

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
			CircuitBreaker: UpstreamCircuitBreaker{
				Enabled:      cfg.Policy.CircuitBreakerConfig.Enabled,
				MaxFailures:  cfg.Policy.CircuitBreakerConfig.MaxFailures,
				ResetTimeout: cfg.Policy.CircuitBreakerConfig.ResetTimeout,
			},
		}

		var circuitBreaker *circuitbreaker.CircuitBreaker
		if policy.CircuitBreaker.Enabled {
			circuitBreaker = circuitbreaker.New(policy.CircuitBreaker.MaxFailures, policy.CircuitBreaker.ResetTimeout)
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
			circuitBreaker: circuitBreaker,
		}

		upstreams = append(upstreams, upstream)
	}

	return upstreams
}

func initRoute(cfg RouteConfig, globalMiddlewares []Middleware, globalMiddlewareIndices map[string]int, log *zap.Logger) Route {
	var (
		globalMiddlewaresCopy = append([]Middleware(nil), globalMiddlewares...)
		localMiddlewares      = make([]Middleware, 0, len(cfg.Middlewares))
	)

	for _, mcfg := range cfg.Middlewares {
		soMiddleware := loadMiddlewareFromSO(mcfg.Path, mcfg.Config, log)
		if soMiddleware == nil {
			log.Error("cannot load middleware from .so", zap.String("name", mcfg.Name))

			if !mcfg.CanFailOnLoad {
				panic("cannot load middleware from .so")
			}

			continue
		}

		log.Info("middleware initialized", zap.String("name", soMiddleware.Name()), zap.String("route", cfg.Method+" "+cfg.Path))

		if mcfg.Override {
			if idx, ok := globalMiddlewareIndices[soMiddleware.Name()]; ok {
				globalMiddlewaresCopy[idx] = soMiddleware
				continue
			}
		}

		localMiddlewares = append(localMiddlewares, soMiddleware)
	}

	middlewares := append(globalMiddlewaresCopy, localMiddlewares...) //nolint:gocritic // because i am retard

	return Route{
		Path:                 cfg.Path,
		Method:               cfg.Method,
		Upstreams:            initUpstreams(cfg.Upstreams),
		Aggregation:          cfg.Aggregation,
		MaxParallelUpstreams: cfg.MaxParallelUpstreams,
		Plugins:              initPlugins(cfg.Plugins, log),
		Middlewares:          middlewares,
	}
}
