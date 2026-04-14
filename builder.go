package kono

import (
	"errors"
	"fmt"
	"net"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/prometheus/client_golang/prometheus"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.uber.org/zap"
	"golang.org/x/sync/semaphore"

	"github.com/starwalkn/kono/internal/metric"
	"github.com/starwalkn/kono/internal/ratelimit"
)

type RoutingConfigSet struct {
	Routing RoutingConfig
	Metrics MetricsConfig
}

func NewRouter(cfgSet RoutingConfigSet, log *zap.Logger) (*Router, *sdkmetric.MeterProvider, *prometheus.Registry, error) {
	routing := cfgSet.Routing

	metrics, provider, reg := initMetrics(cfgSet.Metrics, log)

	router := initMinimalRouter(len(routing.Flows), metrics, log)
	router.rateLimiter = initRateLimiter(routing.RateLimiter, log)

	trustedProxies := parseTrustedProxies(cfgSet.Routing.TrustedProxies, log)

	for _, fcfg := range routing.Flows {
		compiledFlow, err := compileFlow(fcfg, trustedProxies, metrics, log)
		if err != nil {
			log.Fatal("failed to compile flow", zap.Error(err))
		}

		router.flows = append(router.flows, compiledFlow)
	}

	router.registerFlows()

	return router, provider, reg, nil
}

func (r *Router) registerFlows() {
	r.chiRouter.NotFound(func(w http.ResponseWriter, req *http.Request) {
		r.metrics.IncFailedRequestsTotal(metric.FailReasonNoMatchedFlow)
		r.log.Error("no flow matched", zap.String("request_uri", req.URL.RequestURI()))

		http.NotFound(w, req)
	})

	for i := range r.flows {
		f := &r.flows[i]

		middlewares := make([]func(http.Handler) http.Handler, 0, len(f.middlewares))
		for _, m := range f.middlewares {
			middlewares = append(middlewares, m.Handler)
		}

		r.chiRouter.With(middlewares...).Method(
			f.method,
			f.path,
			r.newFlowHandler(f),
		)
	}
}

func initMinimalRouter(routesCount int, metrics metric.Metrics, log *zap.Logger) *Router {
	return &Router{
		chiRouter: chi.NewMux(),
		scatter: &defaultScatter{
			log:     log.Named("scatter"),
			metrics: metrics,
		},
		aggregator:  &defaultAggregator{},
		flows:       make([]flow, 0, routesCount),
		log:         log,
		metrics:     metrics,
		rateLimiter: nil,
	}
}

func initMetrics(cfg MetricsConfig, log *zap.Logger) (metric.Metrics, *sdkmetric.MeterProvider, *prometheus.Registry) {
	if !cfg.Enabled {
		return metric.NewNop(), nil, nil
	}

	switch cfg.Exporter {
	case "prometheus":
		m, reg, err := metric.NewOtelPrometheus()
		if err != nil {
			log.Fatal("failed to init prometheus metrics", zap.Error(err))
		}

		return m, nil, reg
	case "otlp":
		m, provider, err := metric.NewOtelOTLP(cfg.OTLP.Endpoint, cfg.OTLP.Insecure, cfg.OTLP.Interval)
		if err != nil {
			log.Fatal("failed to init otlp metrics", zap.Error(err))
		}

		return m, provider, nil
	default:
		log.Fatal("unknown metrics exporter", zap.String("exporter", cfg.Exporter))
		return metric.NewNop(), nil, nil
	}
}

func initRateLimiter(cfg RateLimiterConfig, log *zap.Logger) *ratelimit.RateLimit {
	if !cfg.Enabled {
		return nil
	}

	rl := ratelimit.New(cfg.Config)
	if err := rl.Start(); err != nil {
		log.Fatal("failed to start rate limiter", zap.Error(err))
	}

	return rl
}

func parseTrustedProxies(proxies []string, log *zap.Logger) []*net.IPNet {
	result := make([]*net.IPNet, 0, len(proxies))

	for _, proxy := range proxies {
		_, ipnet, err := net.ParseCIDR(proxy)
		if err != nil {
			log.Fatal("failed to parse trusted proxy CIDR",
				zap.String("cidr", proxy),
				zap.Error(err),
			)
		}

		result = append(result, ipnet)
	}

	return result
}

func compileFlow(cfg FlowConfig, trustedProxies []*net.IPNet, metrics metric.Metrics, log *zap.Logger) (flow, error) {
	upstreams := initUpstreams(cfg.Upstreams, trustedProxies, metrics, log)

	if cfg.Passthrough && len(upstreams) != 1 {
		return flow{}, fmt.Errorf(
			"passthrough flow '%s' must have exactly one upstream, got %d",
			cfg.Path, len(upstreams),
		)
	}

	var aggregationParams aggregation

	if !cfg.Passthrough {
		var err error

		aggregationParams, err = initAggregation(cfg.Aggregation, upstreams)
		if err != nil {
			return flow{}, err
		}
	}

	return flow{
		path:                 cfg.Path,
		method:               cfg.Method,
		aggregation:          aggregationParams,
		maxParallelUpstreams: cfg.MaxParallelUpstreams,
		upstreams:            upstreams,
		plugins:              initPlugins(cfg.Plugins, log),
		middlewares:          initMiddlewares(cfg.Middlewares, log),
		passthrough:          cfg.Passthrough,

		sem: semaphore.NewWeighted(cfg.MaxParallelUpstreams),
	}, nil
}

func initAggregation(cfg AggregationConfig, upstreams []upstream) (aggregation, error) {
	strategy, err := compileStrategy(cfg.Strategy)
	if err != nil {
		return aggregation{}, err
	}

	agg := aggregation{
		bestEffort:        cfg.BestEffort,
		strategy:          strategy,
		conflictPolicy:    conflictPolicyOverwrite, // default, value used only for merge strategy
		preferredUpstream: -1,                      // default, value used only for merge strategy
	}

	if strategy != strategyMerge {
		return agg, nil
	}

	conflict, err := compileConflictPolicy(cfg.OnConflict.Policy)
	if err != nil {
		return aggregation{}, err
	}

	agg.conflictPolicy = conflict

	if conflict == conflictPolicyPrefer {
		if cfg.OnConflict.Upstream == "" {
			return aggregation{}, errors.New("no upstream specified for on_conflict prefer policy")
		}

		idx, found := searchUpstream(cfg.OnConflict.Upstream, upstreams)
		if !found {
			return aggregation{}, errors.New("preferred upstream for on_conflict policy does not exist")
		}

		agg.preferredUpstream = idx
	}

	return agg, nil
}

func searchUpstream(name string, upstreams []upstream) (int, bool) {
	for i, u := range upstreams {
		if u.name() == name {
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
