package kono

import (
	"net"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/starwalkn/kono/internal/circuitbreaker"
	"github.com/starwalkn/kono/internal/metric"
)

func initUpstreams(cfgs []UpstreamConfig, trustedProxies []*net.IPNet, metrics metric.Metrics, log *zap.Logger) []upstream {
	upstreams := make([]upstream, 0, len(cfgs))

	for _, cfg := range cfgs {
		upstreams = append(upstreams, buildUpstream(cfg, trustedProxies, metrics, log))
	}

	return upstreams
}

func buildUpstream(cfg UpstreamConfig, trustedProxies []*net.IPNet, metrics metric.Metrics, log *zap.Logger) upstream {
	return &httpUpstream{
		cfg:            buildUpstreamConfig(cfg, trustedProxies),
		state:          buildUpstreamState(cfg.Hosts),
		circuitBreaker: buildCircuitBreaker(cfg.Policy.CircuitBreakerConfig),
		metrics:        metrics,
		log:            log,
		client:         buildUpstreamHTTPClient(cfg),
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
