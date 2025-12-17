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
	results [][]byte
}

func (m *mockDispatcher) dispatch(_ *Route, _ *http.Request) [][]byte {
	return m.results
}

type mockAggregator struct{}

func (m *mockAggregator) aggregate(results [][]byte, _ string, _ bool) []byte {
	return bytes.Join(results, []byte(","))
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

func (m *mockMiddleware) Init(_ map[string]interface{}) error { return nil }
func (m *mockMiddleware) Name() string                        { return "mockmw" }
func (m *mockMiddleware) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("X-Middleware", "ok")
		next.ServeHTTP(w, r)
	})
}

func TestRouter_ServeHTTP_BasicFlow(t *testing.T) {
	r := &Router{
		dispatcher: &mockDispatcher{results: [][]byte{[]byte("A"), []byte("B")}},
		aggregator: &mockAggregator{},
		Routes: []Route{
			{
				Path:      "/test",
				Method:    http.MethodGet,
				Aggregate: "join",
				Backends:  []Backend{{URL: "mock1"}, {URL: "mock2"}},
			},
		},
		log: zap.NewNop(),
		metrics: metric.New(),
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

	if string(body) != "A,B" {
		t.Errorf("unexpected body: %q", string(body))
	}
}

func TestRouter_ServeHTTP_NoRoute(t *testing.T) {
	r := &Router{
		Routes: nil,
		log:    zap.NewNop(),
		metrics: metric.New(),
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
		dispatcher: &mockDispatcher{results: [][]byte{[]byte("OK")}},
		aggregator: &mockAggregator{},
		Routes: []Route{
			{
				Path:      "/plug",
				Method:    http.MethodGet,
				Plugins:   []Plugin{reqPlugin, respPlugin},
				Aggregate: "join",
			},
		},
		log: zap.NewNop(),
		metrics: metric.New(),
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
		dispatcher: &mockDispatcher{results: [][]byte{[]byte("body")}},
		aggregator: &mockAggregator{},
		Routes: []Route{
			{
				Path:        "/mw",
				Method:      http.MethodGet,
				Middlewares: []Middleware{&mockMiddleware{}},
			},
		},
		log: zap.NewNop(),
		metrics: metric.New(),
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
