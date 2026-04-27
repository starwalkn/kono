package kono

import (
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/starwalkn/kono/internal/circuitbreaker"
)

//nolint:unparam // it is tests
func mustParseCIDR(cidr string) *net.IPNet {
	_, ipnet, err := net.ParseCIDR(cidr)
	if err != nil {
		panic(err)
	}

	return ipnet
}

func requestWithClientIP(method, url, remoteAddr, clientIP string) *http.Request {
	req, _ := http.NewRequest(method, url, nil)
	req.RemoteAddr = remoteAddr
	req = req.WithContext(withClientIP(req.Context(), clientIP))

	return req
}

func newResolveHeadersUpstream(trustedProxies ...*net.IPNet) *httpUpstream {
	return &httpUpstream{
		log: zap.NewNop(),
		cfg: upstreamConfig{
			trustedProxies: trustedProxies,
		},
	}
}

func TestResolveHeaders_UntrustedProxy_HTTP(t *testing.T) {
	up := newResolveHeadersUpstream(mustParseCIDR("10.0.0.0/8"))

	orig, _ := http.NewRequest(http.MethodGet, "http://example.com/test", nil)
	orig.RemoteAddr = "1.2.3.4:12345"
	target, _ := http.NewRequest(orig.Method, orig.URL.String(), nil)

	err := up.resolveHeaders(target, orig)

	require.NoError(t, err)
	assert.Equal(t, "1.2.3.4", target.Header.Get("X-Forwarded-For"))
	assert.Equal(t, "http", target.Header.Get("X-Forwarded-Proto"))
	assert.Equal(t, "example.com", target.Header.Get("X-Forwarded-Host"))
	assert.Equal(t, "80", target.Header.Get("X-Forwarded-Port"))
	assert.Equal(t, "for=1.2.3.4; proto=http; host=example.com", target.Header.Get("Forwarded"))
}

func TestResolveHeaders_UntrustedProxy_TLS(t *testing.T) {
	up := newResolveHeadersUpstream(mustParseCIDR("10.0.0.0/8"))

	orig, _ := http.NewRequest(http.MethodGet, "https://example.com:8443/test", nil)
	orig.RemoteAddr = "1.2.3.4:12345"
	orig.TLS = &tls.ConnectionState{}
	target, _ := http.NewRequest(orig.Method, orig.URL.String(), nil)

	err := up.resolveHeaders(target, orig)

	require.NoError(t, err)
	assert.Equal(t, "1.2.3.4", target.Header.Get("X-Forwarded-For"))
	assert.Equal(t, "https", target.Header.Get("X-Forwarded-Proto"))
	assert.Equal(t, "example.com:8443", target.Header.Get("X-Forwarded-Host"))
	assert.Equal(t, "8443", target.Header.Get("X-Forwarded-Port"))
	assert.Equal(t, "for=1.2.3.4; proto=https; host=example.com:8443", target.Header.Get("Forwarded"))
}

func TestResolveHeaders_UntrustedProxy_IgnoresIncomingXFF(t *testing.T) {
	up := newResolveHeadersUpstream(mustParseCIDR("10.0.0.0/8"))

	orig := requestWithClientIP(http.MethodGet, "http://example.com/test", "1.2.3.4:12345", "1.2.3.4")
	orig.Header.Set("X-Forwarded-For", "192.168.99.1")
	target, _ := http.NewRequest(orig.Method, orig.URL.String(), nil)

	err := up.resolveHeaders(target, orig)

	require.NoError(t, err)
	assert.Equal(t, "1.2.3.4", target.Header.Get("X-Forwarded-For"))
}

func TestResolveHeaders_TrustedProxy_AppendXFF(t *testing.T) {
	up := newResolveHeadersUpstream(mustParseCIDR("10.0.0.0/8"))

	orig := requestWithClientIP(http.MethodGet, "http://example.com/test", "10.0.1.5:12345", "10.0.1.5")
	orig.Header.Set("X-Forwarded-For", "5.6.7.8")
	orig.Header.Set("X-Forwarded-Proto", "http")
	orig.Header.Set("X-Forwarded-Host", "example.com")
	orig.Header.Set("X-Forwarded-Port", "80")
	target, _ := http.NewRequest(orig.Method, orig.URL.String(), nil)

	err := up.resolveHeaders(target, orig)

	require.NoError(t, err)
	assert.Equal(t, "5.6.7.8, 10.0.1.5", target.Header.Get("X-Forwarded-For"))
	assert.Equal(t, "http", target.Header.Get("X-Forwarded-Proto"))
	assert.Equal(t, "example.com", target.Header.Get("X-Forwarded-Host"))
	assert.Equal(t, "80", target.Header.Get("X-Forwarded-Port"))
	assert.Equal(t, "for=10.0.1.5; proto=http; host=example.com", target.Header.Get("Forwarded"))
}

func TestResolveHeaders_TrustedProxy_InvalidProtoFallback(t *testing.T) {
	up := newResolveHeadersUpstream(mustParseCIDR("10.0.0.0/8"))

	orig, _ := http.NewRequest(http.MethodGet, "http://example.com/test", nil)
	orig.RemoteAddr = "10.0.1.6:12345"
	orig.Header.Set("X-Forwarded-Proto", "ftp")
	target, _ := http.NewRequest(orig.Method, orig.URL.String(), nil)

	err := up.resolveHeaders(target, orig)

	require.NoError(t, err)
	assert.Equal(t, "http", target.Header.Get("X-Forwarded-Proto"))
}

func TestResolveHeaders_TrustedProxy_InvalidPort_UsesDefault(t *testing.T) {
	up := newResolveHeadersUpstream(mustParseCIDR("10.0.0.0/8"))

	orig, _ := http.NewRequest(http.MethodGet, "http://example.com/test", nil)
	orig.RemoteAddr = "10.0.1.7:12345"
	orig.Header.Set("X-Forwarded-Port", "99999")
	target, _ := http.NewRequest(orig.Method, orig.URL.String(), nil)

	err := up.resolveHeaders(target, orig)

	require.NoError(t, err)
	assert.Equal(t, "80", target.Header.Get("X-Forwarded-Port"))
}

func TestResolveQueries_ForwardSpecific(t *testing.T) {
	up := &httpUpstream{
		cfg: upstreamConfig{forwardQueries: []string{"foo", "bar"}},
	}

	original, _ := http.NewRequest(http.MethodGet, "http://example.com?foo=1&bar=2&baz=3", nil)
	target, _ := http.NewRequest(http.MethodGet, "http://upstream.com", nil)

	up.resolveQueries(target, original)

	q := target.URL.Query()
	assert.Equal(t, "1", q.Get("foo"))
	assert.Equal(t, "2", q.Get("bar"))
	assert.False(t, q.Has("baz"))
}

func TestResolveQueries_ForwardAll(t *testing.T) {
	up := &httpUpstream{
		cfg: upstreamConfig{forwardQueries: []string{"*"}},
	}

	original, _ := http.NewRequest(http.MethodGet, "http://example.com?a=1&b=2", nil)
	target, _ := http.NewRequest(http.MethodGet, "http://upstream.com", nil)

	up.resolveQueries(target, original)

	q := target.URL.Query()
	assert.Equal(t, "1", q.Get("a"))
	assert.Equal(t, "2", q.Get("b"))
}

func TestResolveQueries_ForwardParams_Specific(t *testing.T) {
	up := &httpUpstream{
		cfg: upstreamConfig{forwardParams: []string{"user_id"}},
	}

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("user_id", "42")
	rctx.URLParams.Add("order_id", "99")

	original, _ := http.NewRequest(http.MethodGet, "http://example.com/users/42/orders/99", nil)
	original = original.WithContext(context.WithValue(original.Context(), chi.RouteCtxKey, rctx))
	target, _ := http.NewRequest(http.MethodGet, "http://upstream.com", nil)

	up.resolveQueries(target, original)

	q := target.URL.Query()
	assert.Equal(t, "42", q.Get("user_id"))
	assert.False(t, q.Has("order_id"))
}

func TestResolveQueries_ForwardParams_All(t *testing.T) {
	up := &httpUpstream{
		cfg: upstreamConfig{forwardParams: []string{"*"}},
	}

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("user_id", "42")
	rctx.URLParams.Add("order_id", "99")

	original, _ := http.NewRequest(http.MethodGet, "http://example.com/", nil)
	original = original.WithContext(context.WithValue(original.Context(), chi.RouteCtxKey, rctx))
	target, _ := http.NewRequest(http.MethodGet, "http://upstream.com", nil)

	up.resolveQueries(target, original)

	q := target.URL.Query()
	assert.Equal(t, "42", q.Get("user_id"))
	assert.Equal(t, "99", q.Get("order_id"))
}

func TestFilterHeaders_Blacklist(t *testing.T) {
	up := &httpUpstream{
		cfg: upstreamConfig{
			policy: upstreamPolicy{
				headerBlacklist: map[string]struct{}{
					"X-Secret": {},
				},
			},
		},
	}

	headers := http.Header{
		"X-Secret":  []string{"sensitive"},
		"X-Forward": []string{"ok"},
	}

	filtered := up.filterHeaders(headers)

	assert.Empty(t, filtered.Get("X-Secret"))
	assert.Equal(t, "ok", filtered.Get("X-Forward"))
}

func TestFilterHeaders_EmptyBlacklist_ClonesAll(t *testing.T) {
	up := &httpUpstream{
		cfg: upstreamConfig{policy: upstreamPolicy{}},
	}

	headers := http.Header{
		"X-One": []string{"1"},
		"X-Two": []string{"2"},
	}

	filtered := up.filterHeaders(headers)

	assert.Equal(t, "1", filtered.Get("X-One"))
	assert.Equal(t, "2", filtered.Get("X-Two"))
}

func TestExpandPathParams_ReplacesKnownParam(t *testing.T) {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "123")

	req, _ := http.NewRequest(http.MethodGet, "/items/123", nil)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	got := expandPathParams("/items/{id}", req)

	assert.Equal(t, "/items/123", got)
}

func TestExpandPathParams_PreservesUnknownParam(t *testing.T) {
	req, _ := http.NewRequest(http.MethodGet, "/items/123", nil)

	got := expandPathParams("/items/{unknown}", req)

	assert.Equal(t, "/items/{unknown}", got)
}

func TestClassifyDoError(t *testing.T) {
	up := &httpUpstream{}

	cases := []struct {
		err  error
		want upstreamErrorKind
	}{
		{context.DeadlineExceeded, upstreamTimeout},
		{context.Canceled, upstreamCanceled},
		{io.EOF, upstreamConnection},
	}

	for _, c := range cases {
		got := up.classifyDoError(c.err)
		assert.Equal(t, c.want, got)
	}
}

func TestIsBreakerFailure(t *testing.T) {
	up := &httpUpstream{}

	cases := []struct {
		kind upstreamErrorKind
		want bool
	}{
		{upstreamTimeout, true},
		{upstreamConnection, true},
		{upstreamBadStatus, true},
		{upstreamCanceled, false},
		{upstreamBodyTooLarge, false},
		{upstreamReadError, false},
		{upstreamInternal, false},
		{upstreamCircuitOpen, false},
	}

	for _, c := range cases {
		got := up.isBreakerFailure(&upstreamError{kind: c.kind, err: io.EOF})
		assert.Equal(t, c.want, got)
	}

	assert.False(t, up.isBreakerFailure(nil))
}

func TestSelectHost_SingleHost_AlwaysZero(t *testing.T) {
	up := &httpUpstream{
		cfg:     upstreamConfig{hosts: []string{"http://only"}},
		metrics: testMetrics,
		log:     zap.NewNop(),
	}

	for range 5 {
		got := up.selectHost(zap.NewNop())
		assert.Equal(t, int64(0), got)
	}
}

func TestSelectHost_RoundRobin(t *testing.T) {
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
		assert.Equal(t, 2, count)
	}
}

func TestSelectHost_LeastConns_PrefersIdle(t *testing.T) {
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

	got := up.selectHost(zap.NewNop())

	assert.Equal(t, int64(1), got)
}

func TestCall_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	up := newTestUpstream(server.URL)

	resp := up.call(context.Background(), httptest.NewRequest(http.MethodGet, "/", nil), nil)

	require.Nil(t, resp.err)
	assert.Equal(t, `{"ok":true}`, string(resp.body))
}

func TestCall_ContextCanceledBeforeRequest(t *testing.T) {
	up := newTestUpstream("http://localhost:1")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	resp := up.call(ctx, httptest.NewRequest(http.MethodGet, "/", nil), nil)

	require.NotNil(t, resp.err)
	assert.Equal(t, upstreamCanceled, resp.err.kind)
}

func TestCall_ContextCanceledDuringBackoff(t *testing.T) {
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

	require.NotNil(t, resp.err)
	assert.Equal(t, upstreamCanceled, resp.err.kind)
	assert.LessOrEqual(t, calls.Load(), int32(2))
}

func TestCall_CircuitBreaker_Integration(t *testing.T) {
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

	assert.Equal(t, int32(2), calls.Load())
}

func TestCall_CircuitBreaker_RecoversAfterReset(t *testing.T) {
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
	require.NotNil(t, r1.err)
	require.Equal(t, upstreamBadStatus, r1.err.kind)

	r2 := up.call(context.Background(), req(), nil)
	require.NotNil(t, r2.err)
	require.Equal(t, upstreamCircuitOpen, r2.err.kind)

	time.Sleep(resetTimeout + 20*time.Millisecond)

	r3 := up.call(context.Background(), req(), nil)
	assert.Nil(t, r3.err)
}
