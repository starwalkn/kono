package kono

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/starwalkn/kono/sdk"
)

// mockProxyUpstream implements both upstream and proxyCapable.
type mockProxyUpstream struct {
	upstreamName string
	proxyFn      func(w http.ResponseWriter, req *http.Request) error
}

func (m *mockProxyUpstream) name() string { return m.upstreamName }
func (m *mockProxyUpstream) call(_ context.Context, _ *http.Request, _ []byte) *upstreamResponse {
	return &upstreamResponse{}
}
func (m *mockProxyUpstream) proxy(_ context.Context, w http.ResponseWriter, req *http.Request) error {
	return m.proxyFn(w, req)
}

// passthroughFlow builds a minimal passthrough flow.
func passthroughFlow(path string, u upstream, plugins ...sdk.Plugin) flow {
	return flow{
		path:        path,
		method:      http.MethodGet,
		passthrough: true,
		upstreams:   []upstream{u},
		plugins:     plugins,
	}
}

func TestPassthrough_Basic(t *testing.T) {
	u := &mockProxyUpstream{
		upstreamName: "sse",
		proxyFn: func(w http.ResponseWriter, _ *http.Request) error {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.WriteHeader(http.StatusOK)
			_, _ = fmt.Fprint(w, "data: hello\n\n")

			return nil
		},
	}

	r := newTestRouter([]flow{passthroughFlow("/stream", u)}, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/stream", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	res := rec.Result()
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		t.Errorf("status: got %d, want 200", res.StatusCode)
	}

	if ct := res.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("Content-Type: got %q, want %q", ct, "text/event-stream")
	}

	if cc := res.Header.Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("Cache-Control: got %q, want %q", cc, "no-cache")
	}

	if cl := res.Header.Get("Content-Length"); cl != "" {
		t.Errorf("Content-Length must not be set for streaming responses, got %q", cl)
	}

	body, _ := io.ReadAll(res.Body)
	if string(body) != "data: hello\n\n" {
		t.Errorf("body: got %q, want %q", string(body), "data: hello\n\n")
	}
}

func TestPassthrough_RequestPluginRuns(t *testing.T) {
	var pluginSaw string

	plugin := &mockPlugin{
		name: "auth",
		typ:  sdk.PluginTypeRequest,
		fn: func(ctx sdk.Context) {
			ctx.Request().Header.Set("X-Auth", "injected")
		},
	}

	u := &mockProxyUpstream{
		upstreamName: "backend",
		proxyFn: func(w http.ResponseWriter, req *http.Request) error {
			pluginSaw = req.Header.Get("X-Auth")
			w.WriteHeader(http.StatusOK)

			return nil
		},
	}

	f := passthroughFlow("/plugin", u, plugin)
	r := newTestRouter([]flow{f}, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/plugin", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status: got %d, want 200", rec.Code)
	}

	if pluginSaw != "injected" {
		t.Errorf("request plugin must run before upstream call: upstream saw X-Auth=%q", pluginSaw)
	}
}

func TestPassthrough_UpstreamError_BeforeWrite(t *testing.T) {
	u := &mockProxyUpstream{
		upstreamName: "broken",
		proxyFn: func(_ http.ResponseWriter, _ *http.Request) error {
			return errors.New("upstream connection refused")
		},
	}

	r := newTestRouter([]flow{passthroughFlow("/broken", u)}, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/broken", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Errorf("status: got %d, want 502", rec.Code)
	}
}

func TestPassthrough_UpstreamError_AfterWrite(t *testing.T) {
	u := &mockProxyUpstream{
		upstreamName: "partial",
		proxyFn: func(w http.ResponseWriter, _ *http.Request) error {
			w.WriteHeader(http.StatusOK)
			_, _ = fmt.Fprint(w, "data: partial\n\n")

			return errors.New("connection reset by peer")
		},
	}

	r := newTestRouter([]flow{passthroughFlow("/partial", u)}, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/partial", nil)
	rec := httptest.NewRecorder()

	panicked := func() (p bool) {
		defer func() {
			if rv := recover(); rv != nil {
				p = true
			}
		}()

		r.ServeHTTP(rec, req)

		return false
	}()

	if panicked {
		t.Fatal("handler must not panic on upstream error after partial write")
	}

	if rec.Code != http.StatusOK {
		t.Errorf("first WriteHeader must win: got %d, want 200", rec.Code)
	}
}

func TestPassthrough_NonProxyCapableUpstream(t *testing.T) {
	u := &stubUpstream{upstreamName: "plain"}

	f := flow{
		path:        "/noproxy",
		method:      http.MethodGet,
		passthrough: true,
		upstreams:   []upstream{u},
	}

	r := newTestRouter([]flow{f}, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/noproxy", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d, want 500", rec.Code)
	}
}

func TestPassthrough_MultipleUpstreams(t *testing.T) {
	makeU := func(n string) upstream {
		return &mockProxyUpstream{
			upstreamName: n,
			proxyFn: func(w http.ResponseWriter, _ *http.Request) error {
				w.WriteHeader(http.StatusOK)
				return nil
			},
		}
	}

	f := flow{
		path:        "/multi",
		method:      http.MethodGet,
		passthrough: true,
		upstreams:   []upstream{makeU("a"), makeU("b")},
	}

	r := newTestRouter([]flow{f}, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/multi", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d, want 500", rec.Code)
	}
}

func TestPassthrough_SSEMultipleEvents(t *testing.T) {
	events := "data: first\n\ndata: second\n\ndata: third\n\n"

	u := &mockProxyUpstream{
		upstreamName: "sse-multi",
		proxyFn: func(w http.ResponseWriter, _ *http.Request) error {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			_, _ = fmt.Fprint(w, events)

			return nil
		},
	}

	r := newTestRouter([]flow{passthroughFlow("/events", u)}, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/events", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	res := rec.Result()
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want 200", res.StatusCode)
	}

	body, _ := io.ReadAll(res.Body)
	if string(body) != events {
		t.Errorf("body: got %q, want %q", string(body), events)
	}

	if n := strings.Count(string(body), "data:"); n != 3 {
		t.Errorf("expected 3 SSE events, got %d", n)
	}
}

func TestPassthrough_StatusCodePreserved(t *testing.T) {
	u := &mockProxyUpstream{
		upstreamName: "created",
		proxyFn: func(w http.ResponseWriter, _ *http.Request) error {
			w.WriteHeader(http.StatusCreated)

			return nil
		},
	}

	r := newTestRouter([]flow{passthroughFlow("/created", u)}, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/created", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Errorf("status: got %d, want 201", rec.Code)
	}
}
