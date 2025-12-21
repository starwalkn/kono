package tokka

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/starwalkn/tokka/internal/metric"
	"go.uber.org/zap"
)

type mockDispatcher struct {
	results []UpstreamResponse
}

func (m *mockDispatcher) dispatch(_ *Route, _ *http.Request) []UpstreamResponse {
	return m.results
}

type mockAggregator struct{}

func (m *mockAggregator) aggregate(responses []UpstreamResponse, _ string, _ bool) []byte {
	var out [][]byte
	for _, r := range responses {
		out = append(out, r.Body)
	}
	return bytes.Join(out, []byte(","))
}

type mockPlugin struct {
	name string
	typ  PluginType
	fn   func(Context)
}

func (m *mockPlugin) Init(_ map[string]any) {}
func (m *mockPlugin) Name() string          { return m.name }
func (m *mockPlugin) Type() PluginType      { return m.typ }
func (m *mockPlugin) Execute(ctx Context)   { m.fn(ctx) }

type mockMiddleware struct{}

func (m *mockMiddleware) Init(_ map[string]any) error { return nil }
func (m *mockMiddleware) Name() string                { return "mockmw" }
func (m *mockMiddleware) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("X-Middleware", "ok")
		next.ServeHTTP(w, r)
	})
}

func TestRouter_ServeHTTP_BasicFlow(t *testing.T) {
	r := &Router{
		dispatcher: &mockDispatcher{
			results: []UpstreamResponse{
				{Body: []byte("A"), Status: 200},
				{Body: []byte("B"), Status: 200},
			},
		},
		aggregator: &mockAggregator{},
		Routes: []Route{
			{
				Path:      "/test",
				Method:    http.MethodGet,
				Aggregate: "array",
			},
		},
		log:     zap.NewNop(),
		metrics: metric.NewNop(),
	}

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	res := rec.Result()
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)

	if res.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", res.StatusCode)
	}

	got := string(body)
	if got != "A,B" && got != "B,A" {
		t.Errorf("unexpected body: %q", got)
	}
}

func TestRouter_ServeHTTP_NoRoute(t *testing.T) {
	r := &Router{
		Routes:  nil,
		log:     zap.NewNop(),
		metrics: metric.NewNop(),
	}

	req := httptest.NewRequest(http.MethodGet, "/not-found", nil)
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	res := rec.Result()
	defer res.Body.Close()

	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", res.StatusCode)
	}
}

func TestRouter_ServeHTTP_WithPlugins(t *testing.T) {
	var executed []string

	reqPlugin := &mockPlugin{
		name: "req",
		typ:  PluginTypeRequest,
		fn: func(_ Context) {
			executed = append(executed, "req")
		},
	}
	respPlugin := &mockPlugin{
		name: "resp",
		typ:  PluginTypeResponse,
		fn: func(ctx Context) {
			executed = append(executed, "resp")
			ctx.Response().Header.Set("X-Plugin", "done")
		},
	}

	r := &Router{
		dispatcher: &mockDispatcher{
			results: []UpstreamResponse{
				{Body: []byte("OK"), Status: 200},
			},
		},
		aggregator: &mockAggregator{},
		Routes: []Route{
			{
				Path:      "/plug",
				Method:    http.MethodGet,
				Plugins:   []Plugin{reqPlugin, respPlugin},
				Aggregate: "array",
			},
		},
		log:     zap.NewNop(),
		metrics: metric.NewNop(),
	}

	req := httptest.NewRequest(http.MethodGet, "/plug", nil)
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	res := rec.Result()
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)

	if string(body) != "OK" {
		t.Errorf("expected body OK, got %q", string(body))
	}

	if res.Header.Get("X-Plugin") != "done" {
		t.Errorf("response plugin not executed")
	}

	if len(executed) != 2 {
		t.Errorf("expected 2 plugins executed, got %v", executed)
	}
}

func TestRouter_ServeHTTP_WithMiddleware(t *testing.T) {
	r := &Router{
		dispatcher: &mockDispatcher{
			results: []UpstreamResponse{
				{Body: []byte("body"), Status: 200},
			},
		},
		aggregator: &mockAggregator{},
		Routes: []Route{
			{
				Path:        "/mw",
				Method:      http.MethodGet,
				Middlewares: []Middleware{&mockMiddleware{}},
			},
		},
		log:     zap.NewNop(),
		metrics: metric.NewNop(),
	}

	req := httptest.NewRequest(http.MethodGet, "/mw", nil)
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	res := rec.Result()
	defer res.Body.Close()

	if got := res.Header.Get("X-Middleware"); got != "ok" {
		t.Errorf("middleware not executed, header=%q", got)
	}
}
