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
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"golang.org/x/sync/semaphore"

	"github.com/starwalkn/kono/internal/circuitbreaker"
)

const defaultParallelUpstreams = 10

func newTestUpstream(host string, opts ...func(*httpUpstream)) *httpUpstream {
	u := &httpUpstream{
		cfg: upstreamConfig{
			hosts:   []string{host},
			method:  http.MethodGet,
			timeout: 500 * time.Millisecond,
		},
		state:   upstreamState{},
		metrics: testMetrics,
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

func newTestFlow(upstreams []upstream, parallelUpstreams int64) *flow {
	return &flow{
		upstreams: upstreams,
		sem:       semaphore.NewWeighted(parallelUpstreams),
	}
}

func newTestScatter() *defaultScatter {
	return &defaultScatter{
		log:     zap.NewNop(),
		metrics: testMetrics,
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
	}, defaultParallelUpstreams)

	results := newTestScatter().scatter(flow, httptest.NewRequest(http.MethodGet, "/", nil))

	require.Len(t, results, 2)
	assert.Nil(t, results[0].err)
	assert.Nil(t, results[1].err)
	assert.Equal(t, "A", string(results[0].body))
	assert.Equal(t, "B", string(results[1].body))
}

func TestScatter_PostRequest_BodyForwarded(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Write(body)
	}))
	defer server.Close()

	flow := newTestFlow([]upstream{
		newTestUpstream(server.URL, withMethod(http.MethodPost)),
	}, defaultParallelUpstreams)

	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString("hello"))
	results := newTestScatter().scatter(flow, req)

	require.Len(t, results, 1)
	require.Nil(t, results[0].err)
	assert.Equal(t, "hello", string(results[0].body))
}

func TestScatter_BodyExceedsMaxSize_ReturnsNil(t *testing.T) {
	flow := newTestFlow([]upstream{
		newTestUpstream("http://localhost"),
	}, defaultParallelUpstreams)

	oversizedBody := bytes.Repeat([]byte("x"), maxBodySize+1)
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(oversizedBody))

	results := newTestScatter().scatter(flow, req)

	assert.Nil(t, results)
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
	}, defaultParallelUpstreams)

	req := httptest.NewRequest(http.MethodGet, "/?foo=bar", nil)
	req.Header.Set("X-Test", "baz")
	results := newTestScatter().scatter(flow, req)

	require.Len(t, results, 1)
	assert.Equal(t, "bar-baz", string(results[0].body))
}

func TestScatter_ForwardParams_AddedToQuery(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(r.URL.Query().Get("user_id")))
	}))
	defer server.Close()

	flow := newTestFlow([]upstream{
		newTestUpstream(server.URL, withForwardParams("user_id")),
	}, defaultParallelUpstreams)

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("user_id", "42")
	req := httptest.NewRequest(http.MethodGet, "/users/42", nil)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	results := newTestScatter().scatter(flow, req)

	require.Len(t, results, 1)
	assert.Equal(t, "42", string(results[0].body))
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
	}, defaultParallelUpstreams)

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("order_id", "99")
	req := httptest.NewRequest(http.MethodGet, "/orders/99", nil)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	newTestScatter().scatter(flow, req)

	assert.Equal(t, "/orders/99", receivedPath)
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
	}, defaultParallelUpstreams)

	results := newTestScatter().scatter(flow, httptest.NewRequest(http.MethodGet, "/", nil))

	require.Len(t, results, 2)
	assert.Nil(t, results[0].err)
	require.NotNil(t, results[1].err)
	require.Error(t, results[1].err.Unwrap())
	assert.Equal(t, "empty body not allowed by upstream policy", results[1].err.Unwrap().Error())
}

func TestScatter_Policy_AllowedStatuses_ViolatedOnUnexpectedStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	defer server.Close()

	flow := newTestFlow([]upstream{
		newTestUpstream(server.URL, withPolicy(upstreamPolicy{allowedStatuses: []int{http.StatusOK}})),
	}, defaultParallelUpstreams)

	results := newTestScatter().scatter(flow, httptest.NewRequest(http.MethodGet, "/", nil))

	require.Len(t, results, 1)
	assert.NotNil(t, results[0].err)
}

func TestScatter_Policy_MaxResponseBodySize_Exceeded(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("abcdefghijklmnopqrstuvwxyz"))
	}))
	defer server.Close()

	flow := newTestFlow([]upstream{
		newTestUpstream(server.URL, withPolicy(upstreamPolicy{maxResponseBodySize: 10})),
	}, defaultParallelUpstreams)

	results := newTestScatter().scatter(flow, httptest.NewRequest(http.MethodGet, "/", nil))

	require.Len(t, results, 1)
	require.NotNil(t, results[0].err)
	assert.Equal(t, upstreamBodyTooLarge, results[0].err.kind)
}

func TestScatter_UpstreamTimeout_ReturnsTimeoutKind(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(600 * time.Millisecond)
	}))
	defer server.Close()

	flow := newTestFlow([]upstream{
		newTestUpstream(server.URL, withTimeout(100*time.Millisecond)),
	}, defaultParallelUpstreams)

	results := newTestScatter().scatter(flow, httptest.NewRequest(http.MethodGet, "/", nil))

	require.Len(t, results, 1)
	require.NotNil(t, results[0].err)
	assert.Equal(t, upstreamTimeout, results[0].err.kind)
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
	}, defaultParallelUpstreams)

	results := newTestScatter().scatter(flow, httptest.NewRequest(http.MethodGet, "/", nil))

	require.Len(t, results, 1)
	assert.Nil(t, results[0].err)
	assert.Equal(t, int32(3), attempts.Load())
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
	}, defaultParallelUpstreams)

	results := newTestScatter().scatter(flow, httptest.NewRequest(http.MethodGet, "/", nil))

	require.Len(t, results, 1)
	require.NotNil(t, results[0].err)
	assert.Equal(t, upstreamBadStatus, results[0].err.kind)
	assert.Equal(t, int32(maxRetries+1), attempts.Load())
}

func TestScatter_CircuitBreaker_OpensAfterMaxFailures(t *testing.T) {
	var upstreamCalls atomic.Int32

	maxFailures := 3

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		upstreamCalls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	cb := circuitbreaker.New(maxFailures, 100*time.Millisecond)

	flow := newTestFlow([]upstream{
		newTestUpstream(server.URL, withCircuitBreaker(cb)),
	}, defaultParallelUpstreams)

	d := newTestScatter()

	results := make([]upstreamResponse, 5)
	for i := range results {
		responses := d.scatter(flow, httptest.NewRequest(http.MethodGet, "/", nil))
		results[i] = responses[0]
	}

	for i := range maxFailures {
		require.NotNil(t, results[i].err)
		assert.Equal(t, upstreamBadStatus, results[i].err.kind)
	}

	for i := maxFailures; i < 5; i++ {
		require.NotNil(t, results[i].err)
		assert.Equal(t, upstreamCircuitOpen, results[i].err.kind)
	}

	assert.Equal(t, int32(maxFailures), upstreamCalls.Load())
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
	}, defaultParallelUpstreams)

	d := newTestScatter()

	r1 := d.scatter(flow, httptest.NewRequest(http.MethodGet, "/", nil))
	require.NotNil(t, r1[0].err)
	require.Equal(t, upstreamBadStatus, r1[0].err.kind)

	r2 := d.scatter(flow, httptest.NewRequest(http.MethodGet, "/", nil))
	require.NotNil(t, r2[0].err)
	require.Equal(t, upstreamCircuitOpen, r2[0].err.kind)

	time.Sleep(resetTimeout + 20*time.Millisecond)

	r3 := d.scatter(flow, httptest.NewRequest(http.MethodGet, "/", nil))
	assert.Nil(t, r3[0].err)
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

	assert.Equal(t, int32(2), callsA.Load())
	assert.Equal(t, int32(2), callsB.Load())
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
	}, defaultParallelUpstreams)

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

	assert.Greater(t, callsB.Load(), callsA.Load())
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

	assert.LessOrEqual(t, maxConcurrent.Load(), int32(parallelUpstreams))
}
