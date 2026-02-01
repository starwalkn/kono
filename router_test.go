package tokka

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"

	"go.uber.org/zap"

	"github.com/starwalkn/tokka/internal/metric"
)

func decodeJSONResponse(t *testing.T, body []byte) JSONResponse {
	t.Helper()

	var resp JSONResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("invalid JSON response: %v\nbody=%s", err, body)
	}

	return resp
}

type mockDispatcher struct {
	results []UpstreamResponse
}

func (m *mockDispatcher) dispatch(_ *Route, _ *http.Request) []UpstreamResponse {
	return m.results
}

type mockPlugin struct {
	name string
	typ  PluginType
	fn   func(Context)
}

func (m *mockPlugin) Init(_ map[string]interface{}) {}
func (m *mockPlugin) Info() PluginInfo {
	return PluginInfo{
		Name:        m.name,
		Description: "Mock plugin",
		Version:     "v1",
		Author:      "test",
	}
}
func (m *mockPlugin) Type() PluginType { return m.typ }
func (m *mockPlugin) Execute(ctx Context) error {
	m.fn(ctx)

	return nil
}

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
		dispatcher: &mockDispatcher{
			results: []UpstreamResponse{
				{Status: http.StatusOK, Body: []byte(`"A"`), Err: nil},
				{Status: http.StatusOK, Body: []byte(`"B"`), Err: nil},
			},
		},
		aggregator: &defaultAggregator{log: zap.NewNop()},
		Routes: []Route{
			{
				Path:   "/test/basic/flow",
				Method: http.MethodGet,
				Aggregation: AggregationConfig{
					Strategy:            strategyArray,
					AllowPartialResults: false,
				},
			},
		},
		log:     zap.NewNop(),
		metrics: metric.NewNop(),
	}

	req := httptest.NewRequest(http.MethodGet, "/test/basic/flow", nil)
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	res := rec.Result()
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", res.StatusCode)
	}

	if ct := res.Header.Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("unexpected Content-Type: %s", ct)
	}

	body, _ := io.ReadAll(res.Body)

	resp := decodeJSONResponse(t, body)

	if len(resp.Errors) != 0 {
		t.Fatalf("expected no errors, got %d", len(resp.Errors))
	}

	var got []string
	if err := json.Unmarshal(resp.Data, &got); err != nil {
		t.Fatal(err)
	}

	if len(got) != 2 || !slices.Contains(got, "A") || !slices.Contains(got, "B") {
		t.Fatalf("unexpected data: %v", got)
	}
}

func TestRouter_ServeHTTP_PartialResponse(t *testing.T) {
	r := &Router{
		dispatcher: &mockDispatcher{
			results: []UpstreamResponse{
				{Status: http.StatusOK, Body: []byte(`"A"`), Err: nil},
				{Status: http.StatusInternalServerError, Body: nil, Err: &UpstreamError{
					Kind: UpstreamTimeout,
					Err:  errors.New("upstream timeout"),
				}},
			},
		},
		aggregator: &defaultAggregator{log: zap.NewNop()},
		Routes: []Route{
			{
				Path:   "/test/partial/response",
				Method: http.MethodGet,
				Aggregation: AggregationConfig{
					Strategy:            strategyArray,
					AllowPartialResults: true,
				},
			},
		},
		log:     zap.NewNop(),
		metrics: metric.NewNop(),
	}

	req := httptest.NewRequest(http.MethodGet, "/test/partial/response", nil)
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	res := rec.Result()
	defer res.Body.Close()

	if res.StatusCode != http.StatusPartialContent {
		t.Fatalf("expected 206, got %d", res.StatusCode)
	}

	if ct := res.Header.Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("unexpected Content-Type: %s", ct)
	}

	body, _ := io.ReadAll(res.Body)

	resp := decodeJSONResponse(t, body)

	if len(resp.Errors) != 1 {
		t.Fatalf("expected 1 error, got %d", len(resp.Errors))
	}

	if resp.Errors[0].Code != ErrorCodeUpstreamUnavailable && resp.Errors[0].Message != "service temporarily unavailable" {
		t.Fatalf("unexpected error code or message %s %s", resp.Errors[0].Code, resp.Errors[0].Message)
	}

	var got []string
	if err := json.Unmarshal(resp.Data, &got); err != nil {
		t.Fatal(err)
	}

	if len(got) != 1 || !slices.Contains(got, "A") {
		t.Fatalf("unexpected data: %v", got)
	}
}

func TestRouter_ServeHTTP_UpstreamError(t *testing.T) {
	r := &Router{
		dispatcher: &mockDispatcher{
			results: []UpstreamResponse{
				{Status: http.StatusOK, Body: []byte(`"A"`), Err: nil},
				{Status: http.StatusInternalServerError, Body: nil, Err: &UpstreamError{
					Kind: UpstreamTimeout,
					Err:  errors.New("upstream timeout"),
				}},
			},
		},
		aggregator: &defaultAggregator{log: zap.NewNop()},
		Routes: []Route{
			{
				Path:   "/test/upstream/error",
				Method: http.MethodGet,
				Aggregation: AggregationConfig{
					Strategy:            strategyArray,
					AllowPartialResults: false,
				},
			},
		},
		log:     zap.NewNop(),
		metrics: metric.NewNop(),
	}

	req := httptest.NewRequest(http.MethodGet, "/test/upstream/error", nil)
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	res := rec.Result()
	defer res.Body.Close()

	if res.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", res.StatusCode)
	}

	if ct := res.Header.Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("unexpected Content-Type: %s", ct)
	}

	body, _ := io.ReadAll(res.Body)

	resp := decodeJSONResponse(t, body)

	if resp.Data != nil {
		t.Fatalf("unexpected data: %v", resp.Data)
	}

	if len(resp.Errors) != 1 {
		t.Fatalf("expected 1 error, got %d", len(resp.Errors))
	}

	if resp.Errors[0].Code != ErrorCodeUpstreamUnavailable && resp.Errors[0].Message != "service temporarily unavailable" {
		t.Fatalf("unexpected error code or message %s %s", resp.Errors[0].Code, resp.Errors[0].Message)
	}
}

func TestRouter_ServeHTTP_NoRoute(t *testing.T) {
	r := &Router{
		Routes:  nil,
		log:     zap.NewNop(),
		metrics: metric.NewNop(),
	}

	req := httptest.NewRequest(http.MethodGet, "/test/not/found", nil)
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

	requestPlugin := &mockPlugin{
		name: "req",
		typ:  PluginTypeRequest,
		fn: func(_ Context) {
			executed = append(executed, "req")
		},
	}

	responsePlugin := &mockPlugin{
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
				{Status: http.StatusOK, Body: []byte(`"OK"`), Err: nil},
			},
		},
		aggregator: &defaultAggregator{log: zap.NewNop()},
		Routes: []Route{
			{
				Path:    "/test/with/plugins",
				Method:  http.MethodGet,
				Plugins: []Plugin{requestPlugin, responsePlugin},
				Aggregation: AggregationConfig{
					Strategy:            strategyArray,
					AllowPartialResults: false,
				},
			},
		},
		log:     zap.NewNop(),
		metrics: metric.NewNop(),
	}

	req := httptest.NewRequest(http.MethodGet, "/test/with/plugins", nil)
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	res := rec.Result()
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", res.StatusCode)
	}

	if ct := res.Header.Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("unexpected Content-Type: %s", ct)
	}

	body, _ := io.ReadAll(res.Body)

	resp := decodeJSONResponse(t, body)

	if len(resp.Errors) != 0 {
		t.Fatalf("expected no errors, got %d", len(resp.Errors))
	}

	if string(resp.Data) != `"OK"` {
		t.Errorf("expected body OK, got %q", resp.Data)
	}

	if res.Header.Get("X-Plugin") != "done" {
		t.Errorf("response plugin not executed")
	}

	if !reflect.DeepEqual(executed, []string{"req", "resp"}) {
		t.Errorf("unexpected plugin order: %v", executed)
	}
}

func TestRouter_ServeHTTP_WithMiddleware(t *testing.T) {
	r := &Router{
		dispatcher: &mockDispatcher{
			results: []UpstreamResponse{
				{Status: http.StatusOK, Body: []byte(`"OK"`), Err: nil},
			},
		},
		aggregator: &defaultAggregator{log: zap.NewNop()},
		Routes: []Route{
			{
				Path:        "/test/with/middleware",
				Method:      http.MethodGet,
				Middlewares: []Middleware{&mockMiddleware{}},
			},
		},
		log:     zap.NewNop(),
		metrics: metric.NewNop(),
	}

	req := httptest.NewRequest(http.MethodGet, "/test/with/middleware", nil)
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	res := rec.Result()
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", res.StatusCode)
	}

	if ct := res.Header.Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("unexpected Content-Type: %s", ct)
	}

	if got := res.Header.Get("X-Middleware"); got != "ok" {
		t.Errorf("middleware not executed, header=%q", got)
	}
}
