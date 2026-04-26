package kono

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"
	"golang.org/x/sync/semaphore"

	"github.com/starwalkn/kono/internal/circuitbreaker"
	"github.com/starwalkn/kono/internal/metric"
)

const defaultMaxParallel = 10

func newTestUpstream(host string, opts ...func(*httpUpstream)) *httpUpstream {
	u := &httpUpstream{
		cfg: upstreamConfig{
			hosts:   []string{host},
			method:  http.MethodGet,
			timeout: 500 * time.Millisecond,
		},
		state:   upstreamState{},
		metrics: metric.NewNop(),
		log:     zap.NewNop(),
		client:  http.DefaultClient,
	}

	for _, opt := range opts {
		opt(u)
	}

	return u
}

func withMethod(method string) func(*httpUpstream) {
	return func(u *httpUpstream) { u.cfg.method = method }
}

func withTimeout(d time.Duration) func(*httpUpstream) {
	return func(u *httpUpstream) { u.cfg.timeout = d }
}

func withPolicy(p upstreamPolicy) func(*httpUpstream) {
	return func(u *httpUpstream) { u.cfg.policy = p }
}

func withForwardQueries(queries ...string) func(*httpUpstream) {
	return func(u *httpUpstream) { u.cfg.forwardQueries = queries }
}

func withForwardHeaders(headers ...string) func(*httpUpstream) {
	return func(u *httpUpstream) { u.cfg.forwardHeaders = headers }
}

func withForwardParams(params ...string) func(*httpUpstream) {
	return func(u *httpUpstream) { u.cfg.forwardParams = params }
}

func withPath(path string) func(*httpUpstream) {
	return func(u *httpUpstream) { u.cfg.path = path }
}

func withHosts(hosts ...string) func(*httpUpstream) {
	return func(u *httpUpstream) { u.cfg.hosts = hosts }
}

func withLBMode(mode lbMode, hostCount int) func(*httpUpstream) {
	return func(u *httpUpstream) {
		u.cfg.lbMode = mode
		u.state.activeConnections = make([]int64, hostCount)
	}
}

func withCircuitBreaker(cb *circuitbreaker.CircuitBreaker) func(*httpUpstream) {
	return func(u *httpUpstream) { u.circuitBreaker = cb }
}

func newTestFlow(upstreams []upstream, maxParallel int64) *flow {
	return &flow{
		upstreams: upstreams,
		sem:       semaphore.NewWeighted(maxParallel),
	}
}

func newTestScatter() *defaultScatter {
	return &defaultScatter{
		log:     zap.NewNop(),
		metrics: metric.NewNop(),
	}
}

func TestScatter_TwoUpstreams_BothSucceed(t *testing.T) {
	serverA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		w.Write([]byte("A"))
	}))
	defer serverA.Close()

	serverB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		w.Write([]byte("B"))
	}))
	defer serverB.Close()

	flow := newTestFlow([]upstream{
		newTestUpstream(serverA.URL),
		newTestUpstream(serverB.URL),
	}, defaultMaxParallel)

	results := newTestScatter().scatter(flow, httptest.NewRequest(http.MethodGet, "/", nil))

	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].err != nil {
		t.Errorf("upstream A: unexpected error: %v", results[0].err)
	}
	if results[1].err != nil {
		t.Errorf("upstream B: unexpected error: %v", results[1].err)
	}
	if string(results[0].body) != "A" {
		t.Errorf("upstream A: expected body 'A', got %q", results[0].body)
	}
	if string(results[1].body) != "B" {
		t.Errorf("upstream B: expected body 'B', got %q", results[1].body)
	}
}

func TestScatter_PostRequest_BodyForwarded(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Write(body)
	}))
	defer server.Close()

	flow := newTestFlow([]upstream{
		newTestUpstream(server.URL, withMethod(http.MethodPost)),
	}, defaultMaxParallel)

	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString("hello"))
	results := newTestScatter().scatter(flow, req)

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].err != nil {
		t.Fatalf("unexpected error: %v", results[0].err)
	}
	if string(results[0].body) != "hello" {
		t.Errorf("expected body 'hello', got %q", results[0].body)
	}
}

func TestScatter_BodyExceedsMaxSize_ReturnsNil(t *testing.T) {
	flow := newTestFlow([]upstream{
		newTestUpstream("http://localhost"),
	}, defaultMaxParallel)

	oversizedBody := bytes.Repeat([]byte("x"), maxBodySize+1)
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(oversizedBody))

	if results := newTestScatter().scatter(flow, req); results != nil {
		t.Errorf("expected nil results for oversized body, got %d results", len(results))
	}
}

func TestScatter_ForwardQueryAndHeaders(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("foo")
		h := r.Header.Get("X-Test")
		w.Write([]byte(q + "-" + h))
	}))
	defer server.Close()

	flow := newTestFlow([]upstream{
		newTestUpstream(server.URL,
			withForwardQueries("foo"),
			withForwardHeaders("X-Test"),
		),
	}, defaultMaxParallel)

	req := httptest.NewRequest(http.MethodGet, "/?foo=bar", nil)
	req.Header.Set("X-Test", "baz")
	results := newTestScatter().scatter(flow, req)

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if string(results[0].body) != "bar-baz" {
		t.Errorf("expected 'bar-baz', got %q", results[0].body)
	}
}

func TestScatter_ForwardParams_AddedToQuery(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(r.URL.Query().Get("user_id")))
	}))
	defer server.Close()

	flow := newTestFlow([]upstream{
		newTestUpstream(server.URL, withForwardParams("user_id")),
	}, defaultMaxParallel)

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("user_id", "42")
	req := httptest.NewRequest(http.MethodGet, "/users/42", nil)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	results := newTestScatter().scatter(flow, req)

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if string(results[0].body) != "42" {
		t.Errorf("expected user_id '42' in query, got %q", results[0].body)
	}
}

func TestScatter_ExpandPathParams_InUpstreamPath(t *testing.T) {
	var receivedPath string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	flow := newTestFlow([]upstream{
		newTestUpstream(server.URL, withPath("/orders/{order_id}")),
	}, defaultMaxParallel)

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("order_id", "99")
	req := httptest.NewRequest(http.MethodGet, "/orders/99", nil)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	newTestScatter().scatter(flow, req)

	if receivedPath != "/orders/99" {
		t.Errorf("expected upstream path '/orders/99', got %q", receivedPath)
	}
}

func TestScatter_Policy_RequireBody_ViolatedOnEmptyResponse(t *testing.T) {
	serverWithBody := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"ok":true}`))
	}))
	defer serverWithBody.Close()

	serverNoBody := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer serverNoBody.Close()

	flow := newTestFlow([]upstream{
		newTestUpstream(serverWithBody.URL, withPolicy(upstreamPolicy{requireBody: true})),
		newTestUpstream(serverNoBody.URL, withPolicy(upstreamPolicy{requireBody: true})),
	}, defaultMaxParallel)

	results := newTestScatter().scatter(flow, httptest.NewRequest(http.MethodGet, "/", nil))

	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].err != nil {
		t.Errorf("upstream with body: unexpected error: %v", results[0].err)
	}
	if results[1].err == nil {
		t.Fatal("upstream without body: expected policy violation error, got nil")
	}
	if results[1].err.Unwrap() == nil || results[1].err.Unwrap().Error() != "empty body not allowed by upstream policy" {
		t.Errorf("unexpected error message: %v", results[1].err.Unwrap())
	}
}

func TestScatter_Policy_AllowedStatuses_ViolatedOnUnexpectedStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	defer server.Close()

	flow := newTestFlow([]upstream{
		newTestUpstream(server.URL, withPolicy(upstreamPolicy{allowedStatuses: []int{http.StatusOK}})),
	}, defaultMaxParallel)

	results := newTestScatter().scatter(flow, httptest.NewRequest(http.MethodGet, "/", nil))

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].err == nil {
		t.Fatal("expected policy violation error, got nil")
	}
}

func TestScatter_Policy_MaxResponseBodySize_Exceeded(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("abcdefghijklmnopqrstuvwxyz"))
	}))
	defer server.Close()

	flow := newTestFlow([]upstream{
		newTestUpstream(server.URL, withPolicy(upstreamPolicy{maxResponseBodySize: 10})),
	}, defaultMaxParallel)

	results := newTestScatter().scatter(flow, httptest.NewRequest(http.MethodGet, "/", nil))

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].err == nil {
		t.Fatal("expected body too large error, got nil")
	}
	if results[0].err.kind != upstreamBodyTooLarge {
		t.Errorf("expected kind %q, got %q", upstreamBodyTooLarge, results[0].err.kind)
	}
}

func TestScatter_UpstreamTimeout_ReturnsTimeoutKind(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(600 * time.Millisecond)
	}))
	defer server.Close()

	flow := newTestFlow([]upstream{
		newTestUpstream(server.URL, withTimeout(100*time.Millisecond)),
	}, defaultMaxParallel)

	results := newTestScatter().scatter(flow, httptest.NewRequest(http.MethodGet, "/", nil))

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if results[0].err.kind != upstreamTimeout {
		t.Errorf("expected kind %q, got %q", upstreamTimeout, results[0].err.kind)
	}
}

func TestScatter_Retry_SucceedsAfterTwoFailures(t *testing.T) {
	var attempts atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if attempts.Add(1) <= 2 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	flow := newTestFlow([]upstream{
		newTestUpstream(server.URL, withPolicy(upstreamPolicy{
			retry: retryPolicy{
				maxRetries:      3,
				retryOnStatuses: []int{http.StatusInternalServerError},
				backoffDelay:    10 * time.Millisecond,
			},
		})),
	}, defaultMaxParallel)

	results := newTestScatter().scatter(flow, httptest.NewRequest(http.MethodGet, "/", nil))

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].err != nil {
		t.Errorf("expected no error after retry, got %v", results[0].err)
	}
	if attempts.Load() != 3 {
		t.Errorf("expected exactly 3 attempts (2 failures + 1 success), got %d", attempts.Load())
	}
}

func TestScatter_Retry_ExhaustsMaxRetries(t *testing.T) {
	var attempts atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	maxRetries := 3

	flow := newTestFlow([]upstream{
		newTestUpstream(server.URL, withPolicy(upstreamPolicy{
			retry: retryPolicy{
				maxRetries:      maxRetries,
				retryOnStatuses: []int{http.StatusInternalServerError},
				backoffDelay:    10 * time.Millisecond,
			},
		})),
	}, defaultMaxParallel)

	results := newTestScatter().scatter(flow, httptest.NewRequest(http.MethodGet, "/", nil))

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].err == nil {
		t.Fatal("expected error after exhausting retries, got nil")
	}
	if results[0].err.kind != upstreamBadStatus {
		t.Errorf("expected kind %q, got %q", upstreamBadStatus, results[0].err.kind)
	}
	if attempts.Load() != int32(maxRetries+1) {
		t.Errorf("expected %d attempts, got %d", maxRetries+1, attempts.Load())
	}
}

func TestScatter_CircuitBreaker_OpensAfterMaxFailures(t *testing.T) {
	var upstreamCalls atomic.Int32

	maxFailures := 3

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		upstreamCalls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	// Circuit breaker is built separately — same as buildCircuitBreaker does at init time
	cb := circuitbreaker.New(maxFailures, 100*time.Millisecond)

	flow := newTestFlow([]upstream{
		newTestUpstream(server.URL, withCircuitBreaker(cb)),
	}, defaultMaxParallel)

	d := newTestScatter()

	results := make([]upstreamResponse, 5)
	for i := range results {
		responses := d.scatter(flow, httptest.NewRequest(http.MethodGet, "/", nil))
		results[i] = responses[0]
	}

	for i := range maxFailures {
		if results[i].err == nil || results[i].err.kind != upstreamBadStatus {
			t.Errorf("request %d: expected %q, got %v", i, upstreamBadStatus, results[i].err)
		}
	}

	for i := maxFailures; i < 5; i++ {
		if results[i].err == nil || results[i].err.kind != upstreamCircuitOpen {
			t.Errorf("request %d: expected %q, got %v", i, upstreamCircuitOpen, results[i].err)
		}
	}

	if upstreamCalls.Load() != int32(maxFailures) {
		t.Errorf("expected %d upstream calls, got %d", maxFailures, upstreamCalls.Load())
	}
}

func TestScatter_CircuitBreaker_ClosesAfterReset(t *testing.T) {
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

	flow := newTestFlow([]upstream{
		newTestUpstream(server.URL, withCircuitBreaker(cb)),
	}, defaultMaxParallel)

	d := newTestScatter()

	r1 := d.scatter(flow, httptest.NewRequest(http.MethodGet, "/", nil))
	if r1[0].err == nil || r1[0].err.kind != upstreamBadStatus {
		t.Fatalf("first request: expected %q, got %v", upstreamBadStatus, r1[0].err)
	}

	r2 := d.scatter(flow, httptest.NewRequest(http.MethodGet, "/", nil))
	if r2[0].err == nil || r2[0].err.kind != upstreamCircuitOpen {
		t.Fatalf("second request: expected %q (breaker open), got %v", upstreamCircuitOpen, r2[0].err)
	}

	time.Sleep(resetTimeout + 20*time.Millisecond)

	r3 := d.scatter(flow, httptest.NewRequest(http.MethodGet, "/", nil))
	if r3[0].err != nil {
		t.Errorf("third request after reset: expected no error, got %v", r3[0].err)
	}
}

func TestScatter_LoadBalancer_RoundRobin_EvenDistribution(t *testing.T) {
	var callsA, callsB atomic.Int32

	serverA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		callsA.Add(1)
	}))
	defer serverA.Close()

	serverB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		callsB.Add(1)
	}))
	defer serverB.Close()

	flow := newTestFlow([]upstream{
		newTestUpstream("",
			withHosts(serverA.URL, serverB.URL),
			withLBMode(lbModeRoundRobin, 2),
		),
	}, 1)

	d := newTestScatter()
	for range 4 {
		d.scatter(flow, httptest.NewRequest(http.MethodGet, "/", nil))
	}

	if callsA.Load() != 2 || callsB.Load() != 2 {
		t.Errorf("expected round-robin 2/2, got A=%d B=%d", callsA.Load(), callsB.Load())
	}
}

func TestScatter_LoadBalancer_LeastConns_PrefersFastServer(t *testing.T) {
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

	flow := newTestFlow([]upstream{
		newTestUpstream("",
			withHosts(serverA.URL, serverB.URL),
			withLBMode(lbModeLeastConns, 2),
		),
	}, defaultMaxParallel)

	d := newTestScatter()

	var wg sync.WaitGroup
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			d.scatter(flow, httptest.NewRequest(http.MethodGet, "/", nil))
		}()
		time.Sleep(20 * time.Millisecond)
	}
	wg.Wait()

	if callsB.Load() <= callsA.Load() {
		t.Errorf("fast upstream B should receive more requests: A=%d B=%d", callsA.Load(), callsB.Load())
	}
}

func TestScatter_Semaphore_LimitsParallelism(t *testing.T) {
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

	const maxParallel = 2
	const upstreamCount = 6

	upstreams := make([]upstream, upstreamCount)
	for i := range upstreamCount {
		upstreams[i] = newTestUpstream(server.URL)
	}

	newTestScatter().scatter(
		newTestFlow(upstreams, maxParallel),
		httptest.NewRequest(http.MethodGet, "/", nil),
	)

	if maxConcurrent.Load() > maxParallel {
		t.Errorf("parallelism exceeded limit: max allowed %d, observed %d", maxParallel, maxConcurrent.Load())
	}
}
