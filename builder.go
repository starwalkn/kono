package kono

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel/sdk/resource"
	"go.uber.org/zap"
	"golang.org/x/sync/semaphore"

	"github.com/starwalkn/kono/internal/circuitbreaker"
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
			return flow{}, fmt.Errorf("init aggregation: %w", err)
		}
	}

	plugins, err := initPlugins(cfg.Plugins, log)
	if err != nil {
		return flow{}, fmt.Errorf("init plugins: %w", err)
	}

	middlewares, err := initMiddlewares(cfg.Middlewares, log)
	if err != nil {
		return flow{}, fmt.Errorf("init middlewares: %w", err)
	}

	return flow{
		path:              cfg.Path,
		method:            cfg.Method,
		aggregation:       aggregationParams,
		parallelUpstreams: cfg.ParallelUpstreams,
		upstreams:         upstreams,
		plugins:           plugins,
		middlewares:       middlewares,
		passthrough:       cfg.Passthrough,

		sem: semaphore.NewWeighted(cfg.ParallelUpstreams),
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

func initUpstreams(cfgs []UpstreamConfig, trustedProxies []*net.IPNet, metrics *metric.Metrics, log *zap.Logger) []upstream {
	upstreams := make([]upstream, 0, len(cfgs))

	for _, cfg := range cfgs {
		upstreams = append(upstreams, buildUpstream(cfg, trustedProxies, metrics, log))
	}

	return upstreams
}

func buildUpstream(cfg UpstreamConfig, trustedProxies []*net.IPNet, metrics *metric.Metrics, log *zap.Logger) upstream {
	return &httpUpstream{
		cfg:            buildUpstreamConfig(cfg, trustedProxies),
		state:          buildUpstreamState(cfg.Hosts),
		circuitBreaker: buildCircuitBreaker(cfg.Policy.CircuitBreakerConfig),
		metrics:        metrics,
		log:            log,
		client:         buildUpstreamHTTPClient(cfg),
		streamClient:   buildUpstreamStreamClient(cfg),
	}
}

func buildUpstreamConfig(cfg UpstreamConfig, trustedProxies []*net.IPNet) upstreamConfig {
	name := cfg.Name
	if name == "" {
		name = makeUpstreamName(cfg.Method, cfg.Hosts)
	}

	return upstreamConfig{
		id:             uuid.NewString(),
		name:           name,
		hosts:          cfg.Hosts,
		path:           cfg.Path,
		method:         cfg.Method,
		timeout:        cfg.Timeout,
		forwardHeaders: cfg.ForwardHeaders,
		forwardQueries: cfg.ForwardQueries,
		forwardParams:  cfg.ForwardParams,
		trustedProxies: trustedProxies,
		lbMode:         lbMode(cfg.Policy.LoadBalancingConfig.Mode),
		policy:         buildUpstreamPolicy(cfg.Policy),
	}
}

func buildUpstreamState(hosts []string) upstreamState {
	return upstreamState{
		currentHostIdx:    0,
		activeConnections: make([]int64, len(hosts)),
	}
}

func buildUpstreamPolicy(cfg PolicyConfig) upstreamPolicy {
	headerBlacklist := make(map[string]struct{}, len(cfg.HeaderBlacklist))
	for _, h := range cfg.HeaderBlacklist {
		headerBlacklist[h] = struct{}{}
	}

	return upstreamPolicy{
		headerBlacklist:     headerBlacklist,
		allowedStatuses:     cfg.AllowedStatuses,
		requireBody:         cfg.RequireBody,
		maxResponseBodySize: cfg.MaxResponseBodySize,
		retry: retryPolicy{
			maxRetries:      cfg.RetryConfig.MaxRetries,
			retryOnStatuses: cfg.RetryConfig.RetryOnStatuses,
			backoffDelay:    cfg.RetryConfig.BackoffDelay,
		},
	}
}

func buildCircuitBreaker(cfg CircuitBreakerConfig) *circuitbreaker.CircuitBreaker {
	if !cfg.Enabled {
		return nil
	}

	return circuitbreaker.New(cfg.MaxFailures, cfg.ResetTimeout)
}

func buildUpstreamHTTPClient(cfg UpstreamConfig) *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			MaxConnsPerHost:     0,
			MaxIdleConns:        cfg.Transport.MaxIdleConns,
			MaxIdleConnsPerHost: cfg.Transport.MaxIdleConnsPerHost,
			IdleConnTimeout:     cfg.Transport.IdleConnTimeout,
			ForceAttemptHTTP2:   true,
		},
		Timeout: cfg.Timeout,
	}
}

func buildUpstreamStreamClient(cfg UpstreamConfig) *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			MaxIdleConns:        cfg.Transport.MaxIdleConns,
			MaxIdleConnsPerHost: cfg.Transport.MaxIdleConnsPerHost,
			IdleConnTimeout:     cfg.Transport.IdleConnTimeout,
			ForceAttemptHTTP2:   true,
		},
	}
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
