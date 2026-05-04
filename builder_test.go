package kono

import (
	"context"
	"net/http"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/zap"
)

var _ = Describe("builder", func() {
	Describe("NewRouter", func() {
		It("builds a complete bundle from a minimal config", func() {
			cfg := RoutingConfigSet{
				Service: ServiceConfig{Name: "kono-test"},
				Routing: RoutingConfig{
					Flows: []FlowConfig{{
						Path:        "/test",
						Method:      http.MethodGet,
						Passthrough: true,
						Upstreams:   []UpstreamConfig{testUpstreamConfig("7001")},
					}},
				},
			}

			bundle, err := NewRouter(context.Background(), cfg, zap.NewNop())
			Expect(err).NotTo(HaveOccurred())
			Expect(bundle.Router).NotTo(BeNil())
			Expect(bundle.MeterProvider).NotTo(BeNil())  // noop, но не nil
			Expect(bundle.TracerProvider).NotTo(BeNil()) // noop
			Expect(bundle.PromRegistry).To(BeNil())      // metrics не enabled
		})

		It("returns an error if a flow fails to compile", func() {
			cfg := RoutingConfigSet{
				Service: ServiceConfig{Name: "kono-test"},
				Routing: RoutingConfig{
					Flows: []FlowConfig{{
						Path:        "/bad",
						Passthrough: true,
						Upstreams:   []UpstreamConfig{testUpstreamConfig("7001"), testUpstreamConfig("7002")},
					}},
				},
			}

			_, err := NewRouter(context.Background(), cfg, zap.NewNop())
			Expect(err).To(MatchError(ContainSubstring("compile flow")))
		})
	})

	Describe("initRateLimiter", func() {
		It("returns nil if disabled", func() {
			cfg := RateLimiterConfig{
				Enabled: false,
				Config:  nil,
			}

			rl, err := initRateLimiter(cfg)
			Expect(err).NotTo(HaveOccurred())
			Expect(rl).To(BeNil())
		})

		It("returns a configured limiter when enabled", func() {
			cfg := RateLimiterConfig{
				Enabled: true,
				Config: map[string]interface{}{
					"window": "5s",
					"limit":  10,
				},
			}

			rl, err := initRateLimiter(cfg)
			Expect(err).NotTo(HaveOccurred())
			Expect(rl).NotTo(BeNil())
		})
	})

	Describe("parseTrustedProxies", func() {
		Context("when input is not in CIDR format", func() {
			It("returns an error", func() {
				proxies := []string{"127.0.0.1"}

				tp, err := parseTrustedProxies(proxies)
				Expect(err).To(HaveOccurred())
				Expect(tp).To(BeNil())
			})
		})

		Context("when input contains valid CIDRs", func() {
			It("parses all valid CIDRs", func() {
				proxies := []string{"127.0.0.1/8", "192.168.0.1/32"}

				tp, err := parseTrustedProxies(proxies)
				Expect(err).NotTo(HaveOccurred())
				Expect(tp).NotTo(BeNil())
				Expect(tp).To(HaveLen(2))
			})
		})
	})

	Describe("compileFlow", func() {
		It("rejects passthrough flows with multiple upstreams", func() {
			cfg := FlowConfig{
				Path:        "/builder/test",
				Method:      http.MethodGet,
				Passthrough: true,
				Upstreams: []UpstreamConfig{
					testUpstreamConfig("7001"),
					testUpstreamConfig("7002"),
				},
			}

			f, err := compileFlow(cfg, nil, nil, zap.NewNop())
			Expect(err).To(HaveOccurred())
			Expect(f).To(BeZero())
			Expect(err).To(MatchError(ContainSubstring("must have exactly one upstream")))
		})

		It("propagates aggregation initialization errors", func() {
			cfg := FlowConfig{
				Path:        "/builder/test",
				Method:      http.MethodGet,
				Passthrough: false,
				Upstreams: []UpstreamConfig{
					testUpstreamConfig("7001"),
				},
				Aggregation: &AggregationConfig{
					BestEffort: false,
					Strategy:   "unknown",
				},
			}

			f, err := compileFlow(cfg, nil, nil, zap.NewNop())
			Expect(err).To(HaveOccurred())
			Expect(f).To(BeZero())
			Expect(err).To(MatchError(ContainSubstring("init aggregation")))
		})

		It("compiles a passthrough flow", func() {
			cfg := FlowConfig{
				Path:        "/builder/test",
				Method:      http.MethodGet,
				Passthrough: true,
				Upstreams: []UpstreamConfig{
					testUpstreamConfig("7001"),
				},
				Aggregation: &AggregationConfig{
					BestEffort: true,
					Strategy:   strategyArray.String(),
				},
			}

			f, err := compileFlow(cfg, nil, nil, zap.NewNop())
			Expect(err).NotTo(HaveOccurred())
			Expect(f).NotTo(BeZero())
			Expect(f.upstreams).To(HaveLen(1))
			Expect(f.passthrough).To(BeTrue())
		})

		It("compiles a fan-out flow with aggregation", func() {
			cfg := FlowConfig{
				Path:        "/builder/test",
				Method:      http.MethodGet,
				Passthrough: false,
				Upstreams: []UpstreamConfig{
					testUpstreamConfig("7001"),
					testUpstreamConfig("7002"),
				},
				Aggregation: &AggregationConfig{
					BestEffort: false,
					Strategy:   strategyNamespace.String(),
				},
			}

			f, err := compileFlow(cfg, nil, nil, zap.NewNop())
			Expect(err).NotTo(HaveOccurred())
			Expect(f).NotTo(BeZero())
			Expect(f.upstreams).To(HaveLen(2))
			Expect(f.passthrough).To(BeFalse())
		})
	})

	Describe("initAggregation", func() {
		It("fails on unknown strategy", func() {
			cfg := AggregationConfig{
				BestEffort: false,
				Strategy:   "unknown",
				OnConflict: nil,
			}

			agg, err := initAggregation(cfg, nil)
			Expect(err).To(HaveOccurred())
			Expect(agg).To(BeZero())
			Expect(err).To(MatchError(ContainSubstring("unknown aggregation strategy")))
		})

		It("with merge strategy fails on unknown conflict policy", func() {
			cfg := AggregationConfig{
				BestEffort: true,
				Strategy:   strategyMerge.String(),
				OnConflict: &OnConflictConfig{
					Policy: "unknown",
				},
			}

			agg, err := initAggregation(cfg, nil)
			Expect(err).To(HaveOccurred())
			Expect(agg).To(BeZero())
			Expect(err).To(MatchError(ContainSubstring("unknown aggregation conflict policy")))
		})

		It("applies default conflict policy for non-merge strategies", func() {
			cfg := AggregationConfig{
				BestEffort: false,
				Strategy:   strategyArray.String(),
				OnConflict: nil,
			}

			agg, err := initAggregation(cfg, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(agg.strategy.String()).To(Equal(cfg.Strategy))
			Expect(agg.conflictPolicy).To(Equal(conflictPolicyOverwrite))
		})

		It("with merge strategy and prefer conflict policy fails on non-existent upstream", func() {
			up := newTestUpstream("test-service:7001")

			cfg := AggregationConfig{
				BestEffort: false,
				Strategy:   strategyMerge.String(),
				OnConflict: &OnConflictConfig{
					Policy:   conflictPolicyPrefer.String(),
					Upstream: "non-existent",
				},
			}

			agg, err := initAggregation(cfg, []upstream{up})
			Expect(err).To(HaveOccurred())
			Expect(agg).To(BeZero())
			Expect(err).To(MatchError(ContainSubstring("preferred upstream for on_conflict policy does not exist")))
		})

		It("succeeds with valid config", func() {
			up := newTestUpstream("test-service:7001")

			cfg := AggregationConfig{
				BestEffort: true,
				Strategy:   strategyMerge.String(),
				OnConflict: &OnConflictConfig{
					Policy: conflictPolicyFirst.String(),
				},
			}

			agg, err := initAggregation(cfg, []upstream{up})
			Expect(err).NotTo(HaveOccurred())
			Expect(agg.strategy.String()).To(Equal(cfg.Strategy))
			Expect(agg.conflictPolicy).To(Equal(conflictPolicyFirst))
			Expect(agg.bestEffort).To(BeTrue())
		})

		Describe("buildUpstream", func() {
			It("successfully builds upstream from valid config", func() {
				cfg := UpstreamConfig{
					Name:    "",
					Hosts:   AddrList{"test-service:7001", "test-service:7002"},
					Path:    "/builder/test",
					Method:  http.MethodGet,
					Timeout: 5 * time.Second,
				}

				u := buildUpstream(cfg, nil, nil, zap.NewNop())

				Expect(u).NotTo(BeNil())
				Expect(u.name()).To(Equal("get-test-service:7001-test-service:7002"))
			})
		})
	})
})
