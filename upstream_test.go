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
	"go.uber.org/zap"

	"github.com/starwalkn/kono/internal/circuitbreaker"
	"github.com/starwalkn/kono/internal/metric"
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

	if err := up.resolveHeaders(target, orig); err != nil {
		t.Fatalf("resolveHeaders error: %v", err)
	}

	cases := []struct{ header, want string }{
		{"X-Forwarded-For", "1.2.3.4"},
		{"X-Forwarded-Proto", "http"},
		{"X-Forwarded-Host", "example.com"},
		{"X-Forwarded-Port", "80"},
		{"Forwarded", "for=1.2.3.4; proto=http; host=example.com"},
	}
	for _, c := range cases {
		if got := target.Header.Get(c.header); got != c.want {
			t.Errorf("%s = %q; want %q", c.header, got, c.want)
		}
	}
}

func TestResolveHeaders_UntrustedProxy_TLS(t *testing.T) {
	up := newResolveHeadersUpstream(mustParseCIDR("10.0.0.0/8"))

	orig, _ := http.NewRequest(http.MethodGet, "https://example.com:8443/test", nil)
	orig.RemoteAddr = "1.2.3.4:12345"
	orig.TLS = &tls.ConnectionState{}
	target, _ := http.NewRequest(orig.Method, orig.URL.String(), nil)

	if err := up.resolveHeaders(target, orig); err != nil {
		t.Fatalf("resolveHeaders error: %v", err)
	}

	cases := []struct{ header, want string }{
		{"X-Forwarded-For", "1.2.3.4"},
		{"X-Forwarded-Proto", "https"},
		{"X-Forwarded-Host", "example.com:8443"},
		{"X-Forwarded-Port", "8443"},
		{"Forwarded", "for=1.2.3.4; proto=https; host=example.com:8443"},
	}
	for _, c := range cases {
		if got := target.Header.Get(c.header); got != c.want {
			t.Errorf("%s = %q; want %q", c.header, got, c.want)
		}
	}
}

// requestWithClientIP needed: incoming XFF must be ignored, real IP must come from context.
func TestResolveHeaders_UntrustedProxy_IgnoresIncomingXFF(t *testing.T) {
	up := newResolveHeadersUpstream(mustParseCIDR("10.0.0.0/8"))

	orig := requestWithClientIP(http.MethodGet, "http://example.com/test", "1.2.3.4:12345", "1.2.3.4")
	orig.Header.Set("X-Forwarded-For", "192.168.99.1")
	target, _ := http.NewRequest(orig.Method, orig.URL.String(), nil)

	if err := up.resolveHeaders(target, orig); err != nil {
		t.Fatalf("resolveHeaders error: %v", err)
	}

	if got := target.Header.Get("X-Forwarded-For"); got != "1.2.3.4" {
		t.Errorf("X-Forwarded-For = %q; want %q (spoofed XFF must be ignored for untrusted proxy)", got, "1.2.3.4")
	}
}

// requestWithClientIP needed: trusted proxy must append to XFF using real IP from context, not re-read XFF.
func TestResolveHeaders_TrustedProxy_AppendXFF(t *testing.T) {
	up := newResolveHeadersUpstream(mustParseCIDR("10.0.0.0/8"))

	orig := requestWithClientIP(http.MethodGet, "http://example.com/test", "10.0.1.5:12345", "10.0.1.5")
	orig.Header.Set("X-Forwarded-For", "5.6.7.8")
	orig.Header.Set("X-Forwarded-Proto", "http")
	orig.Header.Set("X-Forwarded-Host", "example.com")
	orig.Header.Set("X-Forwarded-Port", "80")
	target, _ := http.NewRequest(orig.Method, orig.URL.String(), nil)

	if err := up.resolveHeaders(target, orig); err != nil {
		t.Fatalf("resolveHeaders error: %v", err)
	}

	cases := []struct{ header, want string }{
		{"X-Forwarded-For", "5.6.7.8, 10.0.1.5"},
		{"X-Forwarded-Proto", "http"},
		{"X-Forwarded-Host", "example.com"},
		{"X-Forwarded-Port", "80"},
		{"Forwarded", "for=10.0.1.5; proto=http; host=example.com"},
	}
	for _, c := range cases {
		if got := target.Header.Get(c.header); got != c.want {
			t.Errorf("%s = %q; want %q", c.header, got, c.want)
		}
	}
}

func TestResolveHeaders_TrustedProxy_InvalidProtoFallback(t *testing.T) {
	up := newResolveHeadersUpstream(mustParseCIDR("10.0.0.0/8"))

	orig, _ := http.NewRequest(http.MethodGet, "http://example.com/test", nil)
	orig.RemoteAddr = "10.0.1.6:12345"
	orig.Header.Set("X-Forwarded-Proto", "ftp")
	target, _ := http.NewRequest(orig.Method, orig.URL.String(), nil)

	if err := up.resolveHeaders(target, orig); err != nil {
		t.Fatalf("resolveHeaders error: %v", err)
	}

	if got := target.Header.Get("X-Forwarded-Proto"); got != "http" {
		t.Errorf("X-Forwarded-Proto = %q; want %q (invalid proto must fall back to derived)", got, "http")
	}
}

func TestResolveHeaders_TrustedProxy_InvalidPort_UsesDefault(t *testing.T) {
	up := newResolveHeadersUpstream(mustParseCIDR("10.0.0.0/8"))

	orig, _ := http.NewRequest(http.MethodGet, "http://example.com/test", nil)
	orig.RemoteAddr = "10.0.1.7:12345"
	orig.Header.Set("X-Forwarded-Port", "99999")
	target, _ := http.NewRequest(orig.Method, orig.URL.String(), nil)

	if err := up.resolveHeaders(target, orig); err != nil {
		t.Fatalf("resolveHeaders error: %v", err)
	}

	if got := target.Header.Get("X-Forwarded-Port"); got != "80" {
		t.Errorf("X-Forwarded-Port = %q; want %q (invalid port must fall back to default)", got, "80")
	}
}

func TestResolveQueries_ForwardSpecific(t *testing.T) {
	up := &httpUpstream{
		cfg: upstreamConfig{forwardQueries: []string{"foo", "bar"}},
	}

	original, _ := http.NewRequest(http.MethodGet, "http://example.com?foo=1&bar=2&baz=3", nil)
	target, _ := http.NewRequest(http.MethodGet, "http://upstream.com", nil)

	up.resolveQueries(target, original)

	q := target.URL.Query()
	if q.Get("foo") != "1" {
		t.Errorf("foo = %q; want %q", q.Get("foo"), "1")
	}
	if q.Get("bar") != "2" {
		t.Errorf("bar = %q; want %q", q.Get("bar"), "2")
	}
	if q.Has("baz") {
		t.Errorf("baz should not be forwarded")
	}
}

func TestResolveQueries_ForwardAll(t *testing.T) {
	up := &httpUpstream{
		cfg: upstreamConfig{forwardQueries: []string{"*"}},
	}

	original, _ := http.NewRequest(http.MethodGet, "http://example.com?a=1&b=2", nil)
	target, _ := http.NewRequest(http.MethodGet, "http://upstream.com", nil)

	up.resolveQueries(target, original)

	q := target.URL.Query()
	if q.Get("a") != "1" || q.Get("b") != "2" {
		t.Errorf("all query params should be forwarded, got %v", q)
	}
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
	if q.Get("user_id") != "42" {
		t.Errorf("user_id = %q; want %q", q.Get("user_id"), "42")
	}
	if q.Has("order_id") {
		t.Errorf("order_id should not be forwarded")
	}
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
	if q.Get("user_id") != "42" || q.Get("order_id") != "99" {
		t.Errorf("all path params should be forwarded as query, got %v", q)
	}
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

	if filtered.Get("X-Secret") != "" {
		t.Errorf("X-Secret should be filtered out")
	}
	if filtered.Get("X-Forward") != "ok" {
		t.Errorf("X-Forward should be preserved")
	}
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

	if filtered.Get("X-One") != "1" || filtered.Get("X-Two") != "2" {
		t.Errorf("all headers should be preserved when blacklist is empty, got %v", filtered)
	}
}

func TestExpandPathParams_ReplacesKnownParam(t *testing.T) {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "123")

	req, _ := http.NewRequest(http.MethodGet, "/items/123", nil)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	got := expandPathParams("/items/{id}", req)
	if got != "/items/123" {
		t.Errorf("expandPathParams = %q; want %q", got, "/items/123")
	}
}

func TestExpandPathParams_PreservesUnknownParam(t *testing.T) {
	req, _ := http.NewRequest(http.MethodGet, "/items/123", nil)

	got := expandPathParams("/items/{unknown}", req)
	if got != "/items/{unknown}" {
		t.Errorf("expandPathParams = %q; want %q (unknown param must be preserved)", got, "/items/{unknown}")
	}
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
		if got != c.want {
			t.Errorf("classifyDoError(%v) = %q; want %q", c.err, got, c.want)
		}
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
		if got != c.want {
			t.Errorf("isBreakerFailure(%q) = %v; want %v", c.kind, got, c.want)
		}
	}

	if up.isBreakerFailure(nil) {
		t.Error("isBreakerFailure(nil) = true; want false")
	}
}

func TestSelectHost_SingleHost_AlwaysZero(t *testing.T) {
	up := &httpUpstream{
		cfg:     upstreamConfig{hosts: []string{"http://only"}},
		metrics: metric.NewNop(),
		log:     zap.NewNop(),
	}

	for range 5 {
		if got := up.selectHost(zap.NewNop()); got != 0 {
			t.Errorf("selectHost() = %d; want 0 for single host", got)
		}
	}
}

func TestSelectHost_RoundRobin(t *testing.T) {
	up := &httpUpstream{
		cfg: upstreamConfig{
			hosts:  []string{"a", "b", "c"},
			lbMode: lbModeRoundRobin,
		},
		metrics: metric.NewNop(),
		log:     zap.NewNop(),
	}

	seen := make(map[int64]int)
	for range 6 {
		seen[up.selectHost(zap.NewNop())]++
	}

	for idx, count := range seen {
		if count != 2 {
			t.Errorf("host %d selected %d times; want 2", idx, count)
		}
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
		metrics: metric.NewNop(),
		log:     zap.NewNop(),
	}

	if got := up.selectHost(zap.NewNop()); got != 1 {
		t.Errorf("selectHost() = %d; want 1 (idle host)", got)
	}
}

func TestCall_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	up := newTestUpstream(server.URL)
	resp := up.call(context.Background(), httptest.NewRequest(http.MethodGet, "/", nil), nil)

	if resp.err != nil {
		t.Fatalf("unexpected error: %v", resp.err)
	}
	if string(resp.body) != `{"ok":true}` {
		t.Errorf("body = %q; want %q", resp.body, `{"ok":true}`)
	}
}

func TestCall_ContextCanceledBeforeRequest(t *testing.T) {
	up := newTestUpstream("http://localhost:1")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	resp := up.call(ctx, httptest.NewRequest(http.MethodGet, "/", nil), nil)

	if resp.err == nil {
		t.Fatal("expected error, got nil")
	}
	if resp.err.kind != upstreamCanceled {
		t.Errorf("kind = %q; want %q", resp.err.kind, upstreamCanceled)
	}
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

	if resp.err == nil {
		t.Fatal("expected error, got nil")
	}
	if resp.err.kind != upstreamCanceled {
		t.Errorf("kind = %q; want %q", resp.err.kind, upstreamCanceled)
	}
	if calls.Load() > 2 {
		t.Errorf("too many upstream calls during backoff: %d", calls.Load())
	}
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

	if calls.Load() != 2 {
		t.Errorf("expected 2 upstream calls before breaker opened, got %d", calls.Load())
	}
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
	if r1.err == nil || r1.err.kind != upstreamBadStatus {
		t.Fatalf("first call: expected bad_status, got %v", r1.err)
	}

	r2 := up.call(context.Background(), req(), nil)
	if r2.err == nil || r2.err.kind != upstreamCircuitOpen {
		t.Fatalf("second call: expected circuit_open, got %v", r2.err)
	}

	time.Sleep(resetTimeout + 20*time.Millisecond)

	r3 := up.call(context.Background(), req(), nil)
	if r3.err != nil {
		t.Errorf("third call after reset: expected no error, got %v", r3.err)
	}
}
