package kono

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/starwalkn/kono/internal/circuitbreaker"
	"github.com/starwalkn/kono/internal/metric"
	"github.com/starwalkn/kono/internal/ratelimit"
)

const (
	sourceBuiltin = "builtin"
	sourceFile    = "file"
)
const (
	builtinPluginsPath     = "/usr/local/lib/kono/plugins/"
	builtinMiddlewaresPath = "/usr/local/lib/kono/middlewares/"
)

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
		flow, err := compileFlow(rcfg, trustedProxies, log)
		if err != nil {
			log.Fatal("failed to initialize flow", zap.Error(err))
		}

		router.Flows = append(router.Flows, flow)
	}

	return router
}

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

func compileFlow(cfg FlowConfig, trustedProxies []*net.IPNet, log *zap.Logger) (Flow, error) {
	upstreams := initUpstreams(cfg.Upstreams, trustedProxies)

	aggregation, err := initAggregation(cfg.Aggregation, upstreams)
	if err != nil {
		return Flow{}, err
	}

	return Flow{
		Path:                 cfg.Path,
		Method:               cfg.Method,
		Aggregation:          aggregation,
		MaxParallelUpstreams: cfg.MaxParallelUpstreams,
		Upstreams:            upstreams,
		Plugins:              initPlugins(cfg.Plugins, log),
		Middlewares:          initMiddlewares(cfg.Middlewares, log),
	}, nil
}

func initMiddlewares(cfgs []MiddlewareConfig, log *zap.Logger) []Middleware {
	middlewares := make([]Middleware, 0, len(cfgs))

	for _, cfg := range cfgs {
		cfn := func(middleware Middleware) bool {
			return middleware.Name() == cfg.Name
		}

		if slices.ContainsFunc(middlewares, cfn) {
			continue
		}

		var soPath string

		switch cfg.Source {
		case sourceBuiltin:
			soPath = builtinMiddlewaresPath + cfg.Name + ".so"
		case sourceFile:
			pathCopy := cfg.Path
			if !strings.HasSuffix(cfg.Path, "/") {
				pathCopy = cfg.Path + "/"
			}

			soPath = pathCopy + cfg.Name + ".so"
		default:
			panic(fmt.Sprintf("invalid source '%s'", cfg.Source))
		}

		soMiddleware := loadMiddleware(soPath, cfg.Config, log)
		if soMiddleware == nil {
			log.Error("cannot load middleware",
				zap.String("name", cfg.Name),
				zap.String("path", soPath),
			)

			panic(fmt.Sprintf("cannot load middleware from path '%s'", soPath))
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

		var soPath string

		switch cfg.Source {
		case sourceBuiltin:
			soPath = builtinPluginsPath + cfg.Name + ".so"
		case sourceFile:
			pathCopy := cfg.Path
			if !strings.HasSuffix(cfg.Path, "/") {
				pathCopy = cfg.Path + "/"
			}

			soPath = pathCopy + cfg.Name + ".so"
		default:
			panic(fmt.Sprintf("invalid source '%s'", cfg.Source))
		}

		soPlugin := loadPlugin(soPath, cfg.Config, log)
		if soPlugin == nil {
			log.Error(
				"cannot load plugin",
				zap.String("name", cfg.Name),
				zap.String("path", soPath),
			)

			panic(fmt.Sprintf("cannot load plugin from path '%s'", soPath))
		}

		log.Info("plugin initialized", zap.Any("name", soPlugin.Info()))

		plugins = append(plugins, soPlugin)
	}

	return plugins
}

func initUpstreams(cfgs []UpstreamConfig, trustedProxies []*net.IPNet) []Upstream {
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
			path:              cfg.Path,
			method:            cfg.Method,
			timeout:           cfg.Timeout,
			forwardHeaders:    cfg.ForwardHeaders,
			forwardQueries:    cfg.ForwardQueries,
			trustedProxies:    trustedProxies,
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

	sb.WriteString(strings.ToLower(method))
	sb.WriteString("-")

	for i, host := range hosts {
		sb.WriteString(strings.ToLower(host))

		if i != len(hosts)-1 {
			sb.WriteString("-")
		}
	}

	return sb.String()
}

func initAggregation(cfg AggregationConfig, upstreams []Upstream) (Aggregation, error) {
	strategy, err := compileStrategy(cfg.Strategy)
	if err != nil {
		return Aggregation{}, err
	}

	agg := Aggregation{
		BestEffort:        cfg.BestEffort,
		Strategy:          strategy,
		ConflictPolicy:    conflictPolicyOverwrite, // default, value used only for merge strategy
		PreferredUpstream: -1,                      // default, value used only for merge strategy
	}

	if strategy != strategyMerge {
		return agg, nil
	}

	conflict, err := compileConflictPolicy(cfg.OnConflict.Policy)
	if err != nil {
		return Aggregation{}, err
	}

	agg.ConflictPolicy = conflict

	if conflict == conflictPolicyPrefer {
		if cfg.OnConflict.Upstream == "" {
			return Aggregation{}, errors.New("no upstream specified for on_conflict prefer policy")
		}

		idx, found := searchUpstream(cfg.OnConflict.Upstream, upstreams)
		if !found {
			return Aggregation{}, errors.New("preferred upstream for on_conflict policy does not exist")
		}

		agg.PreferredUpstream = idx
	}

	return agg, nil
}

func searchUpstream(name string, upstreams []Upstream) (int, bool) {
	for i, u := range upstreams {
		if u.Name() == name {
			return i, true
		}
	}

	return -1, false
}

func compileStrategy(s string) (aggregationStrategy, error) {
	switch s {
	case "array":
		return strategyArray, nil
	case "merge":
		return strategyMerge, nil
	case "namespace":
		return strategyNamespace, nil
	default:
		return 0, fmt.Errorf("unknown aggregation strategy: '%q'", s)
	}
}

func compileConflictPolicy(p string) (conflictPolicy, error) {
	switch p {
	case "overwrite":
		return conflictPolicyOverwrite, nil
	case "error":
		return conflictPolicyError, nil
	case "first":
		return conflictPolicyFirst, nil
	case "prefer":
		return conflictPolicyPrefer, nil
	default:
		return 0, fmt.Errorf("unknown aggregation conflict policy: '%q'", p)
	}
}
