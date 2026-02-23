package kono

import (
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/starwalkn/kono/internal/circuitbreaker"
	"github.com/starwalkn/kono/internal/metric"
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
		Flows:       make([]Flow, 0, routesCount),
		log:         log,
		metrics:     metrics,
		rateLimiter: nil,
	}
}

func initMiddlewares(cfgs []MiddlewareConfig, log *zap.Logger) []Middleware {
	middlewares := make([]Middleware, 0, len(cfgs))

	for _, cfg := range cfgs {
		soMiddleware := loadMiddleware(cfg.Path, cfg.Config, log)
		if soMiddleware == nil {
			log.Error("cannot load middleware from .so", zap.String("name", cfg.Name))

			if !cfg.CanFailOnLoad {
				panic("cannot load middleware from .so")
			}

			continue
		}

		log.Info("middleware initialized", zap.String("name", soMiddleware.Name()))

		middlewares = append(middlewares, soMiddleware)
	}

	return middlewares
}

func initPlugins(cfgs []PluginConfig, log *zap.Logger) []Plugin {
	plugins := make([]Plugin, 0, len(cfgs))

	for _, cfg := range cfgs {
		cfn := func(plugin Plugin) bool {
			return plugin.Info().Name == cfg.Name
		}

		if slices.ContainsFunc(plugins, cfn) {
			continue
		}

		soPlugin := loadPlugin(cfg.Path, cfg.Config, log)
		if soPlugin == nil {
			log.Error(
				"cannot load plugin from .so",
				zap.String("name", cfg.Name),
				zap.String("path", cfg.Path),
			)
			continue
		}

		log.Info("plugin initialized", zap.Any("name", soPlugin.Info()))

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

	// Build upstream policy
	for _, cfg := range cfgs {
		policy := Policy{
			AllowedStatuses:     cfg.Policy.AllowedStatuses,
			RequireBody:         cfg.Policy.RequireBody,
			MapStatusCodes:      cfg.Policy.MapStatusCodes,
			MaxResponseBodySize: cfg.Policy.MaxResponseBodySize,
			RetryPolicy: RetryPolicy{
				MaxRetries:      cfg.Policy.RetryConfig.MaxRetries,
				RetryOnStatuses: cfg.Policy.RetryConfig.RetryOnStatuses,
				BackoffDelay:    cfg.Policy.RetryConfig.BackoffDelay,
			},
			CircuitBreaker: CircuitBreakerPolicy{
				Enabled:      cfg.Policy.CircuitBreakerConfig.Enabled,
				MaxFailures:  cfg.Policy.CircuitBreakerConfig.MaxFailures,
				ResetTimeout: cfg.Policy.CircuitBreakerConfig.ResetTimeout,
			},
			LoadBalancing: LoadBalancingPolicy{
				Mode: cfg.Policy.LoadBalancingConfig.Mode,
			},
		}

		var circuitBreaker *circuitbreaker.CircuitBreaker
		if policy.CircuitBreaker.Enabled {
			circuitBreaker = circuitbreaker.New(policy.CircuitBreaker.MaxFailures, policy.CircuitBreaker.ResetTimeout)
		}

		name := cfg.Name
		if name == "" {
			makeUpstreamName(cfg.Method, cfg.Hosts)
		}

		upstream := &httpUpstream{
			id:                uuid.NewString(),
			name:              name,
			hosts:             cfg.Hosts,
			method:            cfg.Method,
			timeout:           cfg.Timeout,
			forwardHeaders:    cfg.ForwardHeaders,
			forwardQueries:    cfg.ForwardQueries,
			policy:            policy,
			activeConnections: make([]int64, len(cfg.Hosts)),
			client: &http.Client{
				Transport: transport,
			},
			circuitBreaker: circuitBreaker,
		}

		upstreams = append(upstreams, upstream)
	}

	return upstreams
}

// makeUpstreamName returns the upstream name made up of its method and hosts separated by a hyphen.
func makeUpstreamName(method string, hosts []string) string {
	sb := strings.Builder{}

	sb.WriteString(strings.ToUpper(method))
	sb.WriteString("-")

	for i, host := range hosts {
		sb.WriteString(host)

		if i != len(hosts)-1 {
			sb.WriteString("-")
		}
	}

	return sb.String()
}

func initRoute(cfg FlowConfig, log *zap.Logger) Flow {
	return Flow{
		Path:                 cfg.Path,
		Method:               cfg.Method,
		Aggregation:          cfg.Aggregation,
		MaxParallelUpstreams: cfg.MaxParallelUpstreams,
		Upstreams:            initUpstreams(cfg.Upstreams),
		Plugins:              initPlugins(cfg.Plugins, log),
		Middlewares:          initMiddlewares(cfg.Middlewares, log),
	}
}
