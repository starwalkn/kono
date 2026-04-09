package kono

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/prometheus/client_golang/prometheus"
	"go.uber.org/zap"
	"golang.org/x/sync/semaphore"

	"github.com/starwalkn/kono/internal/metric"
	"github.com/starwalkn/kono/internal/ratelimit"
)

type RoutingConfigSet struct {
	Lumos   LumosConfig
	Routing RoutingConfig
	Metrics MetricsConfig
}

func NewRouter(cfgSet RoutingConfigSet, log *zap.Logger) (*Router, *prometheus.Registry, error) {
	routing := cfgSet.Routing

	metrics, reg := initMetrics(cfgSet.Metrics)

	router := initMinimalRouter(len(routing.Flows), metrics, log)
	router.rateLimiter = initRateLimiter(routing.RateLimiter, log)

	lumos, err := initLumos(cfgSet.Lumos)
	if err != nil {
		return nil, nil, err
	}

	router.lumos = lumos

	trustedProxies := parseTrustedProxies(cfgSet.Routing.TrustedProxies, log)

	for _, fcfg := range routing.Flows {
		var flow Flow

		flow, err = compileFlow(fcfg, trustedProxies, metrics, log)
		if err != nil {
			log.Fatal("failed to compile flow", zap.Error(err))
		}

		router.Flows = append(router.Flows, flow)
	}

	router.registerFlows()

	return router, reg, nil
}

func (r *Router) registerFlows() {
	r.chiRouter.NotFound(func(w http.ResponseWriter, req *http.Request) {
		r.metrics.IncFailedRequestsTotal(metric.FailReasonNoMatchedFlow)
		r.log.Error("no flow matched", zap.String("request_uri", req.URL.RequestURI()))

		http.NotFound(w, req)
	})

	for i := range r.Flows {
		flow := &r.Flows[i]

		middlewares := make([]func(http.Handler) http.Handler, 0, len(flow.Middlewares))
		for _, m := range flow.Middlewares {
			middlewares = append(middlewares, m.Handler)
		}

		r.chiRouter.With(middlewares...).Method(
			flow.Method,
			flow.Path,
			r.newFlowHandler(flow),
		)
	}
}

func initMinimalRouter(routesCount int, metrics metric.Metrics, log *zap.Logger) *Router {
	return &Router{
		chiRouter: chi.NewMux(),
		dispatcher: &defaultDispatcher{
			log:     log.Named("dispatcher"),
			metrics: metrics,
		},
		aggregator:  &defaultAggregator{},
		Flows:       make([]Flow, 0, routesCount),
		log:         log,
		metrics:     metrics,
		rateLimiter: nil,
	}
}

func initMetrics(cfg MetricsConfig) (metric.Metrics, *prometheus.Registry) {
	if !cfg.Enabled {
		return metric.NewNop(), nil
	}

	switch cfg.Provider {
	case "prometheus":
		return metric.NewPrometheus()
	default:
		return metric.NewNop(), nil
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

//nolint:nilnil // nil pointer is allowed is lumos was not enabled
func initLumos(cfg LumosConfig) (*lumos, error) {
	if !cfg.Enabled {
		return nil, nil
	}

	rawMaxMsg := os.Getenv("LUMOS_MAX_MSG")
	if rawMaxMsg == "" {
		return nil, errors.New("LUMOS_MAX_MSG environment variable is not set")
	}

	maxMsg, err := strconv.Atoi(rawMaxMsg)
	if err != nil || maxMsg <= 0 {
		return nil, fmt.Errorf("invalid LUMOS_MAX_MSG value: %q", rawMaxMsg)
	}

	lumosCfg := lumosConfig{
		socketPath:          lumosSocketPath,
		socketReadDeadline:  cfg.ReadDeadline,
		socketWriteDeadline: cfg.WriteDeadline,
		maxMsg:              maxMsg,
	}

	return &lumos{cfg: lumosCfg}, nil
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

func compileFlow(cfg FlowConfig, trustedProxies []*net.IPNet, metrics metric.Metrics, log *zap.Logger) (Flow, error) {
	upstreams := initUpstreams(cfg.Upstreams, trustedProxies, metrics, log)

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

		sem: semaphore.NewWeighted(cfg.MaxParallelUpstreams),
	}, nil
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
