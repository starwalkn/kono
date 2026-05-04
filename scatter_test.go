package kono

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-chi/chi/v5"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/starwalkn/kono/internal/circuitbreaker"
)

const defaultParallelUpstreams = 10

var _ = Describe("Scatter", func() {
	Describe("dispatching to multiple upstreams", func() {
		It("returns responses from all of them", func() {
			serverA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				_, _ = w.Write([]byte("A"))
			}))
			defer serverA.Close()

			serverB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				_, _ = w.Write([]byte("B"))
			}))
			defer serverB.Close()

			f := newTestFlow([]upstream{
				newTestUpstream(serverA.URL),
				newTestUpstream(serverB.URL),
			}, defaultParallelUpstreams)

			results := newTestScatter().scatter(f, httptest.NewRequest(http.MethodGet, "/", nil))

			Expect(results).To(HaveLen(2))
			Expect(results[0].err).To(BeNil())
			Expect(results[1].err).To(BeNil())
			Expect(string(results[0].body)).To(Equal("A"))
			Expect(string(results[1].body)).To(Equal("B"))
		})
	})

	Describe("forwarding request data", func() {
		It("forwards POST body to upstream", func() {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				_, _ = w.Write(body)
			}))
			defer server.Close()

			f := newTestFlow([]upstream{
				newTestUpstream(server.URL, withMethod(http.MethodPost)),
			}, defaultParallelUpstreams)

			req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString("hello"))
			results := newTestScatter().scatter(f, req)

			Expect(results).To(HaveLen(1))
			Expect(results[0].err).To(BeNil())
			Expect(string(results[0].body)).To(Equal("hello"))
		})

		It("returns nil for an oversized request body", func() {
			f := newTestFlow([]upstream{
				newTestUpstream("http://localhost"),
			}, defaultParallelUpstreams)

			oversizedBody := bytes.Repeat([]byte("x"), maxBodySize+1)
			req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(oversizedBody))

			Expect(newTestScatter().scatter(f, req)).To(BeNil())
		})

		It("forwards configured query and header values", func() {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				q := r.URL.Query().Get("foo")
				h := r.Header.Get("X-Test")
				_, _ = w.Write([]byte(q + "-" + h))
			}))
			defer server.Close()

			f := newTestFlow([]upstream{
				newTestUpstream(server.URL,
					withForwardQueries("foo"),
					withForwardHeaders("X-Test"),
				),
			}, defaultParallelUpstreams)

			req := httptest.NewRequest(http.MethodGet, "/?foo=bar", nil)
			req.Header.Set("X-Test", "baz")
			results := newTestScatter().scatter(f, req)

			Expect(results).To(HaveLen(1))
			Expect(string(results[0].body)).To(Equal("bar-baz"))
		})

		It("forwards path params as query when configured", func() {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(r.URL.Query().Get("user_id")))
			}))
			defer server.Close()

			f := newTestFlow([]upstream{
				newTestUpstream(server.URL, withForwardParams("user_id")),
			}, defaultParallelUpstreams)

			rctx := chi.NewRouteContext()
			rctx.URLParams.Add("user_id", "42")
			req := httptest.NewRequest(http.MethodGet, "/users/42", nil)
			req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

			results := newTestScatter().scatter(f, req)

			Expect(results).To(HaveLen(1))
			Expect(string(results[0].body)).To(Equal("42"))
		})

		It("expands path params in upstream path template", func() {
			var receivedPath string

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				receivedPath = r.URL.Path
				w.WriteHeader(http.StatusOK)
			}))
			defer server.Close()

			f := newTestFlow([]upstream{
				newTestUpstream(server.URL, withPath("/orders/{order_id}")),
			}, defaultParallelUpstreams)

			rctx := chi.NewRouteContext()
			rctx.URLParams.Add("order_id", "99")
			req := httptest.NewRequest(http.MethodGet, "/orders/99", nil)
			req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

			newTestScatter().scatter(f, req)

			Expect(receivedPath).To(Equal("/orders/99"))
		})
	})

	Describe("upstream policies", func() {
		It("rejects empty body when require_body is set", func() {
			serverWithBody := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(`{"ok":true}`))
			}))
			defer serverWithBody.Close()

			serverNoBody := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusNoContent)
			}))
			defer serverNoBody.Close()

			f := newTestFlow([]upstream{
				newTestUpstream(serverWithBody.URL, withPolicy(upstreamPolicy{requireBody: true})),
				newTestUpstream(serverNoBody.URL, withPolicy(upstreamPolicy{requireBody: true})),
			}, defaultParallelUpstreams)

			results := newTestScatter().scatter(f, httptest.NewRequest(http.MethodGet, "/", nil))

			Expect(results).To(HaveLen(2))
			Expect(results[0].err).To(BeNil())
			Expect(results[1].err).ToNot(BeNil())
			Expect(results[1].err.Unwrap()).To(MatchError("empty body not allowed by upstream policy"))
		})

		It("rejects unexpected status codes", func() {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusTeapot)
			}))
			defer server.Close()

			f := newTestFlow([]upstream{
				newTestUpstream(server.URL, withPolicy(upstreamPolicy{allowedStatuses: []int{http.StatusOK}})),
			}, defaultParallelUpstreams)

			results := newTestScatter().scatter(f, httptest.NewRequest(http.MethodGet, "/", nil))

			Expect(results).To(HaveLen(1))
			Expect(results[0].err).ToNot(BeNil())
		})

		It("rejects responses larger than max_response_body_size", func() {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte("abcdefghijklmnopqrstuvwxyz"))
			}))
			defer server.Close()

			f := newTestFlow([]upstream{
				newTestUpstream(server.URL, withPolicy(upstreamPolicy{maxResponseBodySize: 10})),
			}, defaultParallelUpstreams)

			results := newTestScatter().scatter(f, httptest.NewRequest(http.MethodGet, "/", nil))

			Expect(results).To(HaveLen(1))
			Expect(results[0].err).ToNot(BeNil())
			Expect(results[0].err.kind).To(Equal(upstreamBodyTooLarge))
		})

		It("classifies upstream timeouts", func() {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				time.Sleep(600 * time.Millisecond)
			}))
			defer server.Close()

			f := newTestFlow([]upstream{
				newTestUpstream(server.URL, withTimeout(100*time.Millisecond)),
			}, defaultParallelUpstreams)

			results := newTestScatter().scatter(f, httptest.NewRequest(http.MethodGet, "/", nil))

			Expect(results).To(HaveLen(1))
			Expect(results[0].err).ToNot(BeNil())
			Expect(results[0].err.kind).To(Equal(upstreamTimeout))
		})
	})

	Describe("retry policy", func() {
		It("succeeds after a few transient failures", func() {
			var attempts atomic.Int32

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if attempts.Add(1) <= 2 {
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
				w.WriteHeader(http.StatusOK)
			}))
			defer server.Close()

			f := newTestFlow([]upstream{
				newTestUpstream(server.URL, withPolicy(upstreamPolicy{
					retry: retryPolicy{
						maxRetries:      3,
						retryOnStatuses: []int{http.StatusInternalServerError},
						backoffDelay:    10 * time.Millisecond,
					},
				})),
			}, defaultParallelUpstreams)

			results := newTestScatter().scatter(f, httptest.NewRequest(http.MethodGet, "/", nil))

			Expect(results).To(HaveLen(1))
			Expect(results[0].err).To(BeNil())
			Expect(attempts.Load()).To(Equal(int32(3)))
		})

		It("gives up after exhausting max retries", func() {
			var attempts atomic.Int32

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				attempts.Add(1)
				w.WriteHeader(http.StatusInternalServerError)
			}))
			defer server.Close()

			maxRetries := 3
			f := newTestFlow([]upstream{
				newTestUpstream(server.URL, withPolicy(upstreamPolicy{
					retry: retryPolicy{
						maxRetries:      maxRetries,
						retryOnStatuses: []int{http.StatusInternalServerError},
						backoffDelay:    10 * time.Millisecond,
					},
				})),
			}, defaultParallelUpstreams)

			results := newTestScatter().scatter(f, httptest.NewRequest(http.MethodGet, "/", nil))

			Expect(results).To(HaveLen(1))
			Expect(results[0].err).ToNot(BeNil())
			Expect(results[0].err.kind).To(Equal(upstreamBadStatus))
			Expect(attempts.Load()).To(Equal(int32(maxRetries + 1)))
		})
	})

	Describe("circuit breaker", func() {
		It("opens after configured failure count and rejects further requests", func() {
			var upstreamCalls atomic.Int32

			maxFailures := 3
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				upstreamCalls.Add(1)
				w.WriteHeader(http.StatusInternalServerError)
			}))
			defer server.Close()

			cb := circuitbreaker.New(maxFailures, 100*time.Millisecond)
			f := newTestFlow([]upstream{
				newTestUpstream(server.URL, withCircuitBreaker(cb)),
			}, defaultParallelUpstreams)

			d := newTestScatter()
			results := make([]upstreamResponse, 5)
			for i := range results {
				responses := d.scatter(f, httptest.NewRequest(http.MethodGet, "/", nil))
				results[i] = responses[0]
			}

			for i := range maxFailures {
				Expect(results[i].err).ToNot(BeNil())
				Expect(results[i].err.kind).To(Equal(upstreamBadStatus))
			}
			for i := maxFailures; i < 5; i++ {
				Expect(results[i].err).ToNot(BeNil())
				Expect(results[i].err.kind).To(Equal(upstreamCircuitOpen))
			}
			Expect(upstreamCalls.Load()).To(Equal(int32(maxFailures)))
		})

		It("closes after reset timeout and serves successful requests", func() {
			resetTimeout := 100 * time.Millisecond
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

			f := newTestFlow([]upstream{
				newTestUpstream(server.URL, withCircuitBreaker(cb)),
			}, defaultParallelUpstreams)

			d := newTestScatter()

			r1 := d.scatter(f, httptest.NewRequest(http.MethodGet, "/", nil))
			Expect(r1[0].err).ToNot(BeNil())
			Expect(r1[0].err.kind).To(Equal(upstreamBadStatus))

			r2 := d.scatter(f, httptest.NewRequest(http.MethodGet, "/", nil))
			Expect(r2[0].err).ToNot(BeNil())
			Expect(r2[0].err.kind).To(Equal(upstreamCircuitOpen))

			time.Sleep(resetTimeout + 20*time.Millisecond)

			r3 := d.scatter(f, httptest.NewRequest(http.MethodGet, "/", nil))
			Expect(r3[0].err).To(BeNil())
		})
	})

	Describe("load balancing", func() {
		It("distributes round-robin evenly across hosts", func() {
			var callsA, callsB atomic.Int32

			serverA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				callsA.Add(1)
			}))
			defer serverA.Close()

			serverB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				callsB.Add(1)
			}))
			defer serverB.Close()

			f := newTestFlow([]upstream{
				newTestUpstream("",
					withHosts(serverA.URL, serverB.URL),
					withLBMode(lbModeRoundRobin, 2),
				),
			}, 1)

			d := newTestScatter()
			for range 4 {
				d.scatter(f, httptest.NewRequest(http.MethodGet, "/", nil))
			}

			Expect(callsA.Load()).To(Equal(int32(2)))
			Expect(callsB.Load()).To(Equal(int32(2)))
		})

		It("least-connections sends more traffic to faster host", func() {
			var callsA, callsB atomic.Int32

			serverA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				callsA.Add(1)
				time.Sleep(100 * time.Millisecond)
			}))
			defer serverA.Close()

			serverB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				callsB.Add(1)
			}))
			defer serverB.Close()

			f := newTestFlow([]upstream{
				newTestUpstream("",
					withHosts(serverA.URL, serverB.URL),
					withLBMode(lbModeLeastConns, 2),
				),
			}, defaultParallelUpstreams)

			d := newTestScatter()
			var wg sync.WaitGroup
			for range 10 {
				wg.Add(1)
				go func() {
					defer wg.Done()
					d.scatter(f, httptest.NewRequest(http.MethodGet, "/", nil))
				}()
				time.Sleep(20 * time.Millisecond)
			}
			wg.Wait()

			Expect(callsB.Load()).To(BeNumerically(">", callsA.Load()))
		})
	})

	Describe("parallelism limit", func() {
		It("does not exceed configured semaphore weight", func() {
			var concurrent atomic.Int32
			var maxConcurrent atomic.Int32

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				cur := concurrent.Add(1)
				defer concurrent.Add(-1)
				for {
					old := maxConcurrent.Load()
					if cur <= old || maxConcurrent.CompareAndSwap(old, cur) {
						break
					}
				}
				time.Sleep(30 * time.Millisecond)
				w.WriteHeader(http.StatusOK)
			}))
			defer server.Close()

			const parallelUpstreams = 2
			const upstreamCount = 6

			upstreams := make([]upstream, upstreamCount)
			for i := range upstreamCount {
				upstreams[i] = newTestUpstream(server.URL)
			}

			newTestScatter().scatter(
				newTestFlow(upstreams, parallelUpstreams),
				httptest.NewRequest(http.MethodGet, "/", nil),
			)

			Expect(maxConcurrent.Load()).To(BeNumerically("<=", parallelUpstreams))
		})
	})
})
