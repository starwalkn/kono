package kono

import (
	"context"
	"crypto/tls"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"time"

	"github.com/go-chi/chi/v5"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/zap"

	"github.com/starwalkn/kono/internal/circuitbreaker"
)

var _ = Describe("httpUpstream", func() {
	Describe("resolveHeaders", func() {
		var (
			up     *httpUpstream
			orig   *http.Request
			target *http.Request
		)

		BeforeEach(func() {
			up = newTestUpstream("", withTrustedProxies(mustParseCIDR("10.0.0.0/8")))
		})

		Context("when proxy is untrusted", func() {
			It("sets X-Forwarded headers from RemoteAddr for HTTP", func() {
				orig, _ = http.NewRequest(http.MethodGet, "http://example.com/test", nil)
				orig.RemoteAddr = "1.2.3.4:12345"
				target, _ = http.NewRequest(orig.Method, orig.URL.String(), nil)

				Expect(up.resolveHeaders(target, orig)).To(Succeed())

				Expect(target.Header.Get("X-Forwarded-For")).To(Equal("1.2.3.4"))
				Expect(target.Header.Get("X-Forwarded-Proto")).To(Equal("http"))
				Expect(target.Header.Get("X-Forwarded-Host")).To(Equal("example.com"))
				Expect(target.Header.Get("X-Forwarded-Port")).To(Equal("80"))
				Expect(target.Header.Get("Forwarded")).To(Equal("for=1.2.3.4; proto=http; host=example.com"))
			})

			It("sets X-Forwarded headers from RemoteAddr for HTTPS", func() {
				orig, _ = http.NewRequest(http.MethodGet, "https://example.com:8443/test", nil)
				orig.RemoteAddr = "1.2.3.4:12345"
				orig.TLS = &tls.ConnectionState{}
				target, _ = http.NewRequest(orig.Method, orig.URL.String(), nil)

				Expect(up.resolveHeaders(target, orig)).To(Succeed())

				Expect(target.Header.Get("X-Forwarded-For")).To(Equal("1.2.3.4"))
				Expect(target.Header.Get("X-Forwarded-Proto")).To(Equal("https"))
				Expect(target.Header.Get("X-Forwarded-Host")).To(Equal("example.com:8443"))
				Expect(target.Header.Get("X-Forwarded-Port")).To(Equal("8443"))
			})

			It("ignores incoming X-Forwarded-For from spoofed clients", func() {
				orig = requestWithClientIP(http.MethodGet, "http://example.com/test", "1.2.3.4:12345", "1.2.3.4")
				orig.Header.Set("X-Forwarded-For", "192.168.99.1")
				target, _ = http.NewRequest(orig.Method, orig.URL.String(), nil)

				Expect(up.resolveHeaders(target, orig)).To(Succeed())
				Expect(target.Header.Get("X-Forwarded-For")).To(Equal("1.2.3.4"))
			})
		})

		Context("when proxy is trusted", func() {
			It("appends real client IP to existing X-Forwarded-For", func() {
				orig = requestWithClientIP(http.MethodGet, "http://example.com/test", "10.0.1.5:12345", "10.0.1.5")
				orig.Header.Set("X-Forwarded-For", "5.6.7.8")
				orig.Header.Set("X-Forwarded-Proto", "http")
				orig.Header.Set("X-Forwarded-Host", "example.com")
				orig.Header.Set("X-Forwarded-Port", "80")
				target, _ = http.NewRequest(orig.Method, orig.URL.String(), nil)

				Expect(up.resolveHeaders(target, orig)).To(Succeed())

				Expect(target.Header.Get("X-Forwarded-For")).To(Equal("5.6.7.8, 10.0.1.5"))
				Expect(target.Header.Get("X-Forwarded-Proto")).To(Equal("http"))
				Expect(target.Header.Get("X-Forwarded-Host")).To(Equal("example.com"))
				Expect(target.Header.Get("X-Forwarded-Port")).To(Equal("80"))
				Expect(target.Header.Get("Forwarded")).To(Equal("for=10.0.1.5; proto=http; host=example.com"))
			})

			It("falls back to derived proto when incoming proto is invalid", func() {
				orig, _ = http.NewRequest(http.MethodGet, "http://example.com/test", nil)
				orig.RemoteAddr = "10.0.1.6:12345"
				orig.Header.Set("X-Forwarded-Proto", "ftp")
				target, _ = http.NewRequest(orig.Method, orig.URL.String(), nil)

				Expect(up.resolveHeaders(target, orig)).To(Succeed())
				Expect(target.Header.Get("X-Forwarded-Proto")).To(Equal("http"))
			})

			It("falls back to default port when incoming port is invalid", func() {
				orig, _ = http.NewRequest(http.MethodGet, "http://example.com/test", nil)
				orig.RemoteAddr = "10.0.1.7:12345"
				orig.Header.Set("X-Forwarded-Port", "99999")
				target, _ = http.NewRequest(orig.Method, orig.URL.String(), nil)

				Expect(up.resolveHeaders(target, orig)).To(Succeed())
				Expect(target.Header.Get("X-Forwarded-Port")).To(Equal("80"))
			})
		})
	})

	Describe("resolveQueries", func() {
		It("forwards only configured query keys", func() {
			up := &httpUpstream{
				cfg: upstreamConfig{forwardQueries: []string{"foo", "bar"}},
			}

			orig, _ := http.NewRequest(http.MethodGet, "http://example.com?foo=1&bar=2&baz=3", nil)
			target, _ := http.NewRequest(http.MethodGet, "http://upstream.com", nil)

			up.resolveQueries(target, orig)

			q := target.URL.Query()
			Expect(q.Get("foo")).To(Equal("1"))
			Expect(q.Get("bar")).To(Equal("2"))
			Expect(q.Has("baz")).To(BeFalse())
		})

		It("forwards all query keys when wildcard is configured", func() {
			up := &httpUpstream{
				cfg: upstreamConfig{forwardQueries: []string{"*"}},
			}

			orig, _ := http.NewRequest(http.MethodGet, "http://example.com?a=1&b=2", nil)
			target, _ := http.NewRequest(http.MethodGet, "http://upstream.com", nil)

			up.resolveQueries(target, orig)

			q := target.URL.Query()
			Expect(q.Get("a")).To(Equal("1"))
			Expect(q.Get("b")).To(Equal("2"))
		})

		It("forwards only configured path params", func() {
			up := &httpUpstream{
				cfg: upstreamConfig{forwardParams: []string{"user_id"}},
			}

			rctx := chi.NewRouteContext()
			rctx.URLParams.Add("user_id", "42")
			rctx.URLParams.Add("order_id", "99")

			orig, _ := http.NewRequest(http.MethodGet, "http://example.com/users/42/orders/99", nil)
			orig = orig.WithContext(context.WithValue(orig.Context(), chi.RouteCtxKey, rctx))
			target, _ := http.NewRequest(http.MethodGet, "http://upstream.com", nil)

			up.resolveQueries(target, orig)

			q := target.URL.Query()
			Expect(q.Get("user_id")).To(Equal("42"))
			Expect(q.Has("order_id")).To(BeFalse())
		})

		It("forwards all path params when wildcard is configured", func() {
			up := &httpUpstream{
				cfg: upstreamConfig{forwardParams: []string{"*"}},
			}

			rctx := chi.NewRouteContext()
			rctx.URLParams.Add("user_id", "42")
			rctx.URLParams.Add("order_id", "99")

			orig, _ := http.NewRequest(http.MethodGet, "http://example.com/", nil)
			orig = orig.WithContext(context.WithValue(orig.Context(), chi.RouteCtxKey, rctx))
			target, _ := http.NewRequest(http.MethodGet, "http://upstream.com", nil)

			up.resolveQueries(target, orig)

			q := target.URL.Query()
			Expect(q.Get("user_id")).To(Equal("42"))
			Expect(q.Get("order_id")).To(Equal("99"))
		})
	})

	Describe("filterHeaders", func() {
		It("removes blacklisted headers and preserves the rest", func() {
			up := &httpUpstream{
				cfg: upstreamConfig{
					policy: upstreamPolicy{
						headerBlacklist: map[string]struct{}{"X-Secret": {}},
					},
				},
			}

			headers := http.Header{
				"X-Secret":  []string{"sensitive"},
				"X-Forward": []string{"ok"},
			}

			result := up.filterHeaders(headers)

			Expect(result.Get("X-Secret")).To(BeEmpty())
			Expect(result.Get("X-Forward")).To(Equal("ok"))
		})

		It("clones all headers when blacklist is empty", func() {
			up := &httpUpstream{cfg: upstreamConfig{policy: upstreamPolicy{}}}

			headers := http.Header{
				"X-One": []string{"1"},
				"X-Two": []string{"2"},
			}

			result := up.filterHeaders(headers)

			Expect(result.Get("X-One")).To(Equal("1"))
			Expect(result.Get("X-Two")).To(Equal("2"))
		})
	})

	Describe("expandPathParams", func() {
		It("replaces known params with their values", func() {
			rctx := chi.NewRouteContext()
			rctx.URLParams.Add("id", "123")

			req, _ := http.NewRequest(http.MethodGet, "/items/123", nil)
			req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

			Expect(expandPathParams("/items/{id}", req)).To(Equal("/items/123"))
		})

		It("preserves unknown params verbatim", func() {
			req, _ := http.NewRequest(http.MethodGet, "/items/123", nil)

			Expect(expandPathParams("/items/{unknown}", req)).To(Equal("/items/{unknown}"))
		})
	})

	DescribeTable("classifyDoError",
		func(err error, want upstreamErrorKind) {
			up := &httpUpstream{}
			Expect(up.classifyDoError(err)).To(Equal(want))
		},
		Entry("deadline exceeded → timeout", context.DeadlineExceeded, upstreamTimeout),
		Entry("canceled → canceled", context.Canceled, upstreamCanceled),
		Entry("EOF → connection error", io.EOF, upstreamConnection),
	)

	DescribeTable("isBreakerFailure",
		func(kind upstreamErrorKind, want bool) {
			up := &httpUpstream{}
			Expect(up.isBreakerFailure(&upstreamError{kind: kind, err: io.EOF})).To(Equal(want))
		},
		Entry("timeout counts as failure", upstreamTimeout, true),
		Entry("connection error counts as failure", upstreamConnection, true),
		Entry("bad status counts as failure", upstreamBadStatus, true),
		Entry("canceled does not count", upstreamCanceled, false),
		Entry("body too large does not count", upstreamBodyTooLarge, false),
		Entry("read error does not count", upstreamReadError, false),
		Entry("internal does not count", upstreamInternal, false),
		Entry("circuit open does not count", upstreamCircuitOpen, false),
	)

	It("treats nil error as non-failure for breaker", func() {
		up := &httpUpstream{}
		Expect(up.isBreakerFailure(nil)).To(BeFalse())
	})

	Describe("selectHost", func() {
		It("always returns 0 for a single host", func() {
			up := &httpUpstream{
				cfg:     upstreamConfig{hosts: []string{"http://only"}},
				metrics: testMetrics,
				log:     zap.NewNop(),
			}

			for range 5 {
				Expect(up.selectHost(zap.NewNop())).To(Equal(int64(0)))
			}
		})

		It("distributes evenly across hosts in round-robin mode", func() {
			up := &httpUpstream{
				cfg: upstreamConfig{
					hosts:  []string{"a", "b", "c"},
					lbMode: lbModeRoundRobin,
				},
				metrics: testMetrics,
				log:     zap.NewNop(),
			}

			seen := make(map[int64]int)
			for range 6 {
				seen[up.selectHost(zap.NewNop())]++
			}

			for _, count := range seen {
				Expect(count).To(Equal(2))
			}
		})

		It("prefers idle host in least-connections mode", func() {
			up := &httpUpstream{
				cfg: upstreamConfig{
					hosts:  []string{"busy", "idle"},
					lbMode: lbModeLeastConns,
				},
				state: upstreamState{
					activeConnections: []int64{5, 0},
				},
				metrics: testMetrics,
				log:     zap.NewNop(),
			}

			Expect(up.selectHost(zap.NewNop())).To(Equal(int64(1)))
		})
	})

	Describe("call", func() {
		It("returns successful response body", func() {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(`{"ok":true}`))
			}))
			defer server.Close()

			up := newTestUpstream(server.URL)
			resp := up.call(context.Background(), httptest.NewRequest(http.MethodGet, "/", nil), nil)

			Expect(resp.err).To(BeNil())
			Expect(string(resp.body)).To(Equal(`{"ok":true}`))
		})

		It("returns canceled error if context is already canceled", func() {
			up := newTestUpstream("http://localhost:1")

			ctx, cancel := context.WithCancel(context.Background())
			cancel()

			resp := up.call(ctx, httptest.NewRequest(http.MethodGet, "/", nil), nil)

			Expect(resp.err).ToNot(BeNil())
			Expect(resp.err.kind).To(Equal(upstreamCanceled))
		})

		It("aborts retry backoff when context is canceled", func() {
			var calls atomic.Int32

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.WriteHeader(http.StatusInternalServerError)
			}))
			defer server.Close()

			up := newTestUpstream(server.URL, withPolicy(upstreamPolicy{
				retry: retryPolicy{
					maxRetries:      5,
					retryOnStatuses: []int{http.StatusInternalServerError},
					backoffDelay:    500 * time.Millisecond,
				},
			}))

			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()

			resp := up.call(ctx, httptest.NewRequest(http.MethodGet, "/", nil), nil)

			Expect(resp.err).ToNot(BeNil())
			Expect(resp.err.kind).To(Equal(upstreamCanceled))
			Expect(calls.Load()).To(BeNumerically("<=", 2))
		})

		It("respects circuit breaker after consecutive failures", func() {
			var calls atomic.Int32

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.WriteHeader(http.StatusInternalServerError)
			}))
			defer server.Close()

			cb := circuitbreaker.New(2, 100*time.Millisecond)
			up := newTestUpstream(server.URL, withCircuitBreaker(cb))

			for range 4 {
				up.call(context.Background(), httptest.NewRequest(http.MethodGet, "/", nil), nil)
			}

			Expect(calls.Load()).To(Equal(int32(2)))
		})

		It("recovers after circuit breaker reset timeout", func() {
			resetTimeout := 50 * time.Millisecond
			cb := circuitbreaker.New(1, resetTimeout)

			var calls atomic.Int32

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if calls.Add(1) == 1 {
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
				w.WriteHeader(http.StatusOK)
			}))
			defer server.Close()

			up := newTestUpstream(server.URL, withCircuitBreaker(cb))
			req := func() *http.Request { return httptest.NewRequest(http.MethodGet, "/", nil) }

			r1 := up.call(context.Background(), req(), nil)
			Expect(r1.err).ToNot(BeNil())
			Expect(r1.err.kind).To(Equal(upstreamBadStatus))

			r2 := up.call(context.Background(), req(), nil)
			Expect(r2.err).ToNot(BeNil())
			Expect(r2.err.kind).To(Equal(upstreamCircuitOpen))

			time.Sleep(resetTimeout + 20*time.Millisecond)

			r3 := up.call(context.Background(), req(), nil)
			Expect(r3.err).To(BeNil())
		})
	})
})
