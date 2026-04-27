package kono

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel/sdk/resource"
	"go.uber.org/zap"
	"golang.org/x/sync/semaphore"

	"github.com/starwalkn/kono/internal/metric"
	"github.com/starwalkn/kono/internal/otelcommon"
	"github.com/starwalkn/kono/internal/ratelimit"
	"github.com/starwalkn/kono/internal/tracing"
)

type RoutingConfigSet struct {
	Routing        RoutingConfig
	Service        ServiceConfig
	ServiceVersion string // injected via ldflags
	Metrics        MetricsConfig
	Tracing        TracingConfig
}

type RouterBundle struct {
	Router         *Router
	MeterProvider  otelcommon.Provider
	TracerProvider otelcommon.Provider
	PromRegistry   *prometheus.Registry // nil unless metrics.exporter == "prometheus"
}

func NewRouter(ctx context.Context, cfgSet RoutingConfigSet, log *zap.Logger) (RouterBundle, error) {
	tracing.InstallPropagator() // Sets the propagator even if tracing is disabled

	res, err := otelcommon.NewResource(ctx, cfgSet.Service.Name, cfgSet.ServiceVersion)
	if err != nil {
		return RouterBundle{}, fmt.Errorf("build otel resource: %w", err)
	}

	meterProvider, promRegistry, err := initMetrics(ctx, cfgSet.Metrics, res)
	if err != nil {
		return RouterBundle{}, fmt.Errorf("init metrics: %w", err)
	}

	metrics, err := metric.New()
	if err != nil {
		return RouterBundle{}, fmt.Errorf("init metric instruments: %w", err)
	}

	tracerProvider, err := initTracing(ctx, cfgSet.Tracing, res)
	if err != nil {
		return RouterBundle{}, fmt.Errorf("init tracing: %w", err)
	}

	routing := cfgSet.Routing

	router := initMinimalRouter(len(routing.Flows), metrics, log)
	router.rateLimiter, err = initRateLimiter(routing.RateLimiter)
	if err != nil {
		return RouterBundle{}, fmt.Errorf("init rate limiter: %w", err)
	}

	trustedProxies, err := parseTrustedProxies(cfgSet.Routing.TrustedProxies)
	if err != nil {
		return RouterBundle{}, fmt.Errorf("parse trusted proxies: %w", err)
	}

	for _, fcfg := range routing.Flows {
		compiledFlow, compileErr := compileFlow(fcfg, trustedProxies, metrics, log)
		if compileErr != nil {
			return RouterBundle{}, fmt.Errorf("compile flow %q: %w", fcfg.Path, compileErr)
		}

		router.flows = append(router.flows, compiledFlow)
	}

	router.registerFlows()

	return RouterBundle{
		Router:         router,
		MeterProvider:  meterProvider,
		PromRegistry:   promRegistry,
		TracerProvider: tracerProvider,
	}, nil
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

func initMinimalRouter(routesCount int, metrics *metric.Metrics, log *zap.Logger) *Router {
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

func initMetrics(ctx context.Context, cfg MetricsConfig, res *resource.Resource) (otelcommon.Provider, *prometheus.Registry, error) {
	if !cfg.Enabled {
		return otelcommon.NewNopProvider(), nil, nil
	}

	switch cfg.Exporter {
	case "prometheus":
		provider, reg, err := metric.NewOtelPrometheus(res)
		if err != nil {
			return nil, nil, fmt.Errorf("init prometheus metrics: %w", err)
		}

		return provider, reg, nil

	case "otlp":
		provider, err := metric.NewOtelOTLP(ctx, cfg.OTLP.Endpoint, cfg.OTLP.Insecure, cfg.OTLP.Interval, res)
		if err != nil {
			return nil, nil, fmt.Errorf("init otlp metrics: %w", err)
		}

		return provider, nil, nil
	default:
		return nil, nil, fmt.Errorf("unsupported metrics exporter: %q", cfg.Exporter)
	}
}

func initTracing(ctx context.Context, cfg TracingConfig, res *resource.Resource) (otelcommon.Provider, error) {
	if !cfg.Enabled {
		return otelcommon.NewNopProvider(), nil
	}

	switch cfg.Exporter {
	case "otlp":
		tracerProvider, err := tracing.NewOtelOTLP(
			ctx,
			cfg.OTLP.Endpoint,
			cfg.OTLP.Insecure,
			cfg.OTLP.Interval,
			cfg.SamplingRatio,
			res,
		)
		if err != nil {
			return nil, fmt.Errorf("init otlp tracing: %w", err)
		}

		return tracerProvider, nil
	default:
		return nil, fmt.Errorf("unsupported tracing exporter: %q", cfg.Exporter)
	}
}

func initRateLimiter(cfg RateLimiterConfig) (*ratelimit.RateLimit, error) {
	if !cfg.Enabled {
		return nil, nil //nolint:nilnil // it is ok for optional modules
	}

	rl := ratelimit.New(cfg.Config)
	if err := rl.Start(); err != nil {
		return nil, fmt.Errorf("start rate limiter: %w", err)
	}

	return rl, nil
}

func parseTrustedProxies(proxies []string) ([]*net.IPNet, error) {
	result := make([]*net.IPNet, 0, len(proxies))

	for _, proxy := range proxies {
		_, ipnet, err := net.ParseCIDR(proxy)
		if err != nil {
			return nil, fmt.Errorf("parse trusted proxy CIDR: %w", err)
		}

		result = append(result, ipnet)
	}

	return result, nil
}

func compileFlow(cfg FlowConfig, trustedProxies []*net.IPNet, metrics *metric.Metrics, log *zap.Logger) (flow, error) {
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

		aggregationParams, err = initAggregation(*cfg.Aggregation, upstreams)
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
		return 0, fmt.Errorf("unknown aggregation strategy: %q", s)
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
		return 0, fmt.Errorf("unknown aggregation conflict policy: %q", p)
	}
}
