package kono

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/starwalkn/kono/internal/circuitbreaker"
	"github.com/starwalkn/kono/internal/metric"

	"go.uber.org/zap"
)

const maxParallelUpstreams = 10

func TestDispatcher_Dispatch_Success(t *testing.T) {
	t.Log(runtime.NumCPU())

	upstreamA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		w.Write([]byte("A"))
	}))
	defer upstreamA.Close()

	upstreamB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		w.Write([]byte("B"))
	}))
	defer upstreamB.Close()

	d := &defaultDispatcher{
		log:     zap.NewNop(),
		metrics: metric.NewNop(),
	}

	route := &Route{
		Upstreams: []Upstream{
			&httpUpstream{hosts: []string{upstreamA.URL}, timeout: 1000 * time.Millisecond, log: zap.NewNop(), client: http.DefaultClient},
			&httpUpstream{hosts: []string{upstreamB.URL}, timeout: 1000 * time.Millisecond, log: zap.NewNop(), client: http.DefaultClient},
		},
		MaxParallelUpstreams: maxParallelUpstreams,
	}

	originalRequest := httptest.NewRequest(http.MethodGet, "http://example.com/test", nil)

	results := d.dispatch(route, originalRequest)
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	got := string(results[0].Body) + string(results[1].Body)
	want1 := "AB"
	if got != want1 {
		t.Errorf("unexpected results: %q", got)
	}
}

func TestDispatcher_Dispatch_ForwardQueryAndHeaders(t *testing.T) {
	upstreamA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("foo")
		h := r.Header.Get("X-Test")

		w.Write([]byte(q + "-" + h))
	}))
	defer upstreamA.Close()

	d := &defaultDispatcher{
		log:     zap.NewNop(),
		metrics: metric.NewNop(),
	}

	route := &Route{
		Upstreams: []Upstream{
			&httpUpstream{
				hosts:               []string{upstreamA.URL},
				forwardQueryStrings: []string{"foo"},
				forwardHeaders:      []string{"X-Test"},
				timeout:             500 * time.Millisecond,
				log:                 zap.NewNop(),
				client:              http.DefaultClient,
			},
		},
		MaxParallelUpstreams: maxParallelUpstreams,
	}

	originalRequest := httptest.NewRequest(http.MethodGet, "http://example.com/test?foo=bar", nil)
	originalRequest.Header.Set("X-Test", "baz")

	results := d.dispatch(route, originalRequest)

	if string(results[0].Body) != "bar-baz" {
		t.Errorf("unexpected result: %q", results[0].Body)
	}
}

func TestDispatcher_Dispatch_PostWithBody(t *testing.T) {
	upstreamA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Write(body)
	}))
	defer upstreamA.Close()

	d := &defaultDispatcher{
		log:     zap.NewNop(),
		metrics: metric.NewNop(),
	}

	route := &Route{
		Upstreams: []Upstream{
			&httpUpstream{
				hosts:   []string{upstreamA.URL},
				method:  http.MethodPost,
				timeout: 500 * time.Millisecond,
				log:     zap.NewNop(),
				client:  http.DefaultClient,
			},
		},
		MaxParallelUpstreams: maxParallelUpstreams,
	}

	originalRequest := httptest.NewRequest(http.MethodPost, "http://example.com/test", bytes.NewBufferString("hello"))

	results := d.dispatch(route, originalRequest)

	if string(results[0].Body) != "hello" {
		t.Errorf("expected 'hello', got %q", results[0].Body)
	}
}

func TestDispatcher_Dispatch_UpstreamTimeout(t *testing.T) {
	upstreamA := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		time.Sleep(600 * time.Millisecond)
	}))
	defer upstreamA.Close()

	d := &defaultDispatcher{
		log:     zap.NewNop(),
		metrics: metric.NewNop(),
	}

	route := &Route{
		Upstreams: []Upstream{
			&httpUpstream{
				hosts:   []string{upstreamA.URL},
				method:  http.MethodGet,
				timeout: 500 * time.Millisecond,
				log:     zap.NewNop(),
				client:  http.DefaultClient,
			},
		},
		MaxParallelUpstreams: maxParallelUpstreams,
	}

	originalRequest := httptest.NewRequest(http.MethodGet, "http://example.com/test", nil)

	results := d.dispatch(route, originalRequest)

	if len(results) != 1 {
		t.Errorf("expected 1 result, got %d", len(results))
	}

	if !errors.Is(results[0].Err, context.DeadlineExceeded) {
		t.Errorf("expected timeout error, got %v", results[0].Err)
	}
}

func TestDispatcher_Dispatch_MapStatusCodesPolicy(t *testing.T) {
	upstreamA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer upstreamA.Close()

	d := &defaultDispatcher{
		log:     zap.NewNop(),
		metrics: metric.NewNop(),
	}

	route := &Route{
		Upstreams: []Upstream{
			&httpUpstream{
				hosts:   []string{upstreamA.URL},
				method:  http.MethodGet,
				timeout: 500 * time.Millisecond,
				log:     zap.NewNop(),
				client:  http.DefaultClient,
				policy: Policy{
					MapStatusCodes: map[int]int{
						404: 502, // NotFound to InternalServerError
					},
				},
			},
		},
		MaxParallelUpstreams: maxParallelUpstreams,
	}

	originalRequest := httptest.NewRequest(http.MethodGet, "http://example.com/test", nil)

	results := d.dispatch(route, originalRequest)

	if len(results) != 1 {
		t.Errorf("expected 1 result, got %d", len(results))
	}

	if results[0].Err != nil {
		t.Errorf("expected no error, got %v", results[0].Err)
	}

	if results[0].Status != 502 {
		t.Errorf("expected status 502, got %d", results[0].Status)
	}
}

func TestDispatcher_Dispatch_MaxResponseBodySizePolicy(t *testing.T) {
	var (
		responseText        string = "abcdefghijklmnopqrstuvwxyz"
		maxResponseBodySize int64  = 10
	)

	upstreamA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(responseText))
	}))
	defer upstreamA.Close()

	d := &defaultDispatcher{
		log:     zap.NewNop(),
		metrics: metric.NewNop(),
	}

	route := &Route{
		Upstreams: []Upstream{
			&httpUpstream{
				hosts:   []string{upstreamA.URL},
				method:  http.MethodGet,
				timeout: 500 * time.Millisecond,
				log:     zap.NewNop(),
				client:  http.DefaultClient,
				policy: Policy{
					MaxResponseBodySize: maxResponseBodySize,
				},
			},
		},
		MaxParallelUpstreams: maxParallelUpstreams,
	}

	originalRequest := httptest.NewRequest(http.MethodGet, "http://example.com/test", nil)

	results := d.dispatch(route, originalRequest)

	if len(results) != 1 {
		t.Errorf("expected 1 result, got %d", len(results))
	}

	if results[0].Err == nil {
		t.Errorf("expected error, got nil")
	}

	if results[0].Err.Error() != string(UpstreamBodyTooLarge) {
		t.Errorf("expected error message 'response body larger than limit of %d bytes', got %v", maxResponseBodySize, results[0].Err)
	}
}

func TestDispatcher_Dispatch_RequireBodyPolicy(t *testing.T) {
	upstreamA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`Never gonna give you up
			Never gonna let you down
			Never gonna run around and desert you`))
	}))
	defer upstreamA.Close()

	upstreamB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstreamB.Close()

	d := &defaultDispatcher{
		log:     zap.NewNop(),
		metrics: metric.NewNop(),
	}

	route := &Route{
		Upstreams: []Upstream{
			&httpUpstream{
				hosts:   []string{upstreamA.URL},
				method:  http.MethodGet,
				timeout: 500 * time.Millisecond,
				log:     zap.NewNop(),
				client:  http.DefaultClient,
				policy: Policy{
					RequireBody: true,
				},
			},
			&httpUpstream{
				hosts:   []string{upstreamB.URL},
				method:  http.MethodGet,
				timeout: 500 * time.Millisecond,
				log:     zap.NewNop(),
				client:  http.DefaultClient,
				policy: Policy{
					RequireBody: true,
				},
			},
		},
		MaxParallelUpstreams: maxParallelUpstreams,
	}

	originalRequest := httptest.NewRequest(http.MethodGet, "http://example.com/test", nil)

	results := d.dispatch(route, originalRequest)

	if len(results) != 2 {
		t.Errorf("expected 2 results, got %d", len(results))
	}

	if results[0].Err != nil {
		t.Errorf("expected no error, got %v", results[0].Err)
	}

	if results[1].Err == nil || results[1].Err.Unwrap().Error() != "empty body not allowed by upstream policy" {
		t.Errorf("expected policy violation error, got %v", results[1].Err)
	}
}

func TestDispatcher_Dispatch_RetryPolicy(t *testing.T) {
	attemptsCount := 0

	upstreamA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attemptsCount++

		if attemptsCount <= 2 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusOK)
	}))
	defer upstreamA.Close()

	d := &defaultDispatcher{
		log:     zap.NewNop(),
		metrics: metric.NewNop(),
	}

	route := &Route{
		Upstreams: []Upstream{
			&httpUpstream{
				hosts:   []string{upstreamA.URL},
				method:  http.MethodGet,
				timeout: 500 * time.Millisecond,
				log:     zap.NewNop(),
				client:  http.DefaultClient,
				policy: Policy{
					RetryPolicy: RetryPolicy{
						MaxRetries:      3,
						RetryOnStatuses: []int{http.StatusInternalServerError},
						BackoffDelay:    10 * time.Millisecond,
					},
				},
			},
		},
		MaxParallelUpstreams: maxParallelUpstreams,
	}

	originalRequest := httptest.NewRequest(http.MethodGet, "http://example.com/test", nil)

	results := d.dispatch(route, originalRequest)

	if len(results) != 1 {
		t.Errorf("expected 1 result, got %d", len(results))
	}

	retriesCount := attemptsCount - 1

	if retriesCount > route.Upstreams[0].Policy().RetryPolicy.MaxRetries {
		t.Errorf("retries count %d exceeds max retries %d", retriesCount, route.Upstreams[0].Policy().RetryPolicy.MaxRetries)
	}
}

func TestDispatcher_Dispatch_CircuitBreakerPolicy(t *testing.T) {
	var (
		failures    = 0
		maxFailures = 3
	)

	upstreamA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		failures++
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer upstreamA.Close()

	d := &defaultDispatcher{
		log:     zap.NewNop(),
		metrics: metric.NewNop(),
	}

	cb := circuitbreaker.New(maxFailures, 100*time.Millisecond)

	route := &Route{
		Upstreams: []Upstream{
			&httpUpstream{
				hosts:   []string{upstreamA.URL},
				method:  http.MethodGet,
				timeout: 500 * time.Millisecond,
				log:     zap.NewNop(),
				client:  http.DefaultClient,
				policy: Policy{
					// The policy is added for clarity, but we are creating the
					// circuit breaker separately since the upstream builder is not involved here.
					CircuitBreaker: CircuitBreakerPolicy{
						Enabled:      true,
						MaxFailures:  maxFailures,
						ResetTimeout: 100 * time.Millisecond,
					},
				},
				circuitBreaker: cb,
			},
		},
		MaxParallelUpstreams: maxParallelUpstreams,
	}

	originalRequest := httptest.NewRequest(http.MethodGet, "http://example.com/test", nil)

	var results []UpstreamResponse

	for range 5 {
		responses := d.dispatch(route, originalRequest)
		results = append(results, responses[0])
	}

	for i := range maxFailures {
		if results[i].Err == nil || results[i].Err.Kind != UpstreamBadStatus {
			t.Errorf("expected %s error for attempt %d, got %v", UpstreamBadStatus, i, results[i].Err)
		}
	}

	for i := 3; i < 5; i++ {
		if results[i].Err == nil || results[i].Err.Kind != UpstreamCircuitOpen {
			t.Errorf("expected %s error for attempt %d, got %v", UpstreamCircuitOpen, i, results[i].Err)
		}
	}

	if failures != maxFailures {
		t.Errorf("expected %d upstream calls, got %d", maxFailures, failures)
	}
}

func TestDispatcher_Dispatch_LoadBalancerPolicy_RoundRobin(t *testing.T) {
	var callsA, callsB int

	upstreamA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		callsA++
		w.Write([]byte("A"))
	}))
	defer upstreamA.Close()

	upstreamB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		callsB++
		w.Write([]byte("B"))
	}))
	defer upstreamB.Close()

	d := &defaultDispatcher{
		log:     zap.NewNop(),
		metrics: metric.NewNop(),
	}

	route := &Route{
		Upstreams: []Upstream{
			&httpUpstream{
				hosts:   []string{upstreamA.URL, upstreamB.URL},
				method:  http.MethodGet,
				timeout: 500 * time.Millisecond,
				log:     zap.NewNop(),
				client:  http.DefaultClient,
				policy: Policy{
					LoadBalancer: LoadBalancerPolicy{Mode: LBModeRoundRobin},
				},
			},
		},
		MaxParallelUpstreams: 1,
	}

	for range 4 {
		req := httptest.NewRequest(http.MethodGet, "http://example.com/test", nil)
		_ = d.dispatch(route, req)
	}

	if callsA != 2 || callsB != 2 {
		t.Errorf("expected round robin distribution 2/2, got A=%d B=%d", callsA, callsB)
	}
}

func TestDispatcher_Dispatch_LoadBalancerPolicy_LeastConns(t *testing.T) {
	var callsA, callsB int

	// Slow server
	upstreamA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		callsA++
		time.Sleep(100 * time.Millisecond)
		w.Write([]byte("A"))
	}))
	defer upstreamA.Close()

	// Fast server
	upstreamB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		callsB++
		w.Write([]byte("B"))
	}))
	defer upstreamB.Close()

	d := &defaultDispatcher{
		log:     zap.NewNop(),
		metrics: metric.NewNop(),
	}

	route := &Route{
		Upstreams: []Upstream{
			&httpUpstream{
				hosts:   []string{upstreamA.URL, upstreamB.URL},
				method:  http.MethodGet,
				timeout: 500 * time.Millisecond,
				log:     zap.NewNop(),
				client:  http.DefaultClient,
				policy: Policy{
					LoadBalancer: LoadBalancerPolicy{Mode: LBModeLeastConns},
				},
				activeConnections: make([]int64, 2),
			},
		},
		MaxParallelUpstreams: maxParallelUpstreams,
	}

	var wg sync.WaitGroup

	for range 10 {
		wg.Add(1)

		go func() {
			defer wg.Done()

			req := httptest.NewRequest(http.MethodGet, "http://example.com/test", nil)
			_ = d.dispatch(route, req)
		}()

		time.Sleep(20 * time.Millisecond)
	}
	wg.Wait()

	if callsB <= callsA {
		t.Errorf("expected fast upstream (B) to receive more traffic, got A=%d B=%d", callsA, callsB)
	}
}
