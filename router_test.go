package kono

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/starwalkn/kono/internal/metric"
	"github.com/starwalkn/kono/sdk"
)

var testMetrics *metric.Metrics

func TestMain(m *testing.M) {
	tm, err := metric.New()
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "init test metrics: %v\n", err)
		os.Exit(1)
	}

	testMetrics = tm

	os.Exit(m.Run())
}

func decodeJSONResponse(t *testing.T, body []byte) ClientResponse {
	t.Helper()

	var resp ClientResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("invalid JSON response: %v\nbody=%s", err, body)
	}

	return resp
}

type mockScatter struct {
	results []upstreamResponse
}

func (m *mockScatter) scatter(_ *flow, _ *http.Request) []upstreamResponse {
	return m.results
}

type mockPlugin struct {
	name string
	typ  sdk.PluginType
	fn   func(sdk.Context)
}

func (m *mockPlugin) Init(_ map[string]interface{}) {}
func (m *mockPlugin) Info() sdk.PluginInfo {
	return sdk.PluginInfo{
		Name:        m.name,
		Description: "Mock plugin",
		Version:     "v1",
		Author:      "test",
	}
}
func (m *mockPlugin) Type() sdk.PluginType { return m.typ }
func (m *mockPlugin) Execute(ctx sdk.Context) error {
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

func newTestRouter(flows []flow, d scatter, a aggregator) *Router {
	r := &Router{
		chiRouter:  chi.NewMux(),
		scatter:    d,
		aggregator: a,
		flows:      flows,
		log:        zap.NewNop(),
		metrics:    testMetrics,
	}

	r.registerFlows()

	return r
}

func TestRouter_ServeHTTP_BasicFlow(t *testing.T) {
	d := &mockScatter{
		results: []upstreamResponse{
			{status: http.StatusOK, body: []byte(`"A"`), err: nil},
			{status: http.StatusOK, body: []byte(`"B"`), err: nil},
		},
	}

	flows := []flow{
		{
			path:   "/test/basic/flow",
			method: http.MethodGet,
			aggregation: aggregation{
				strategy:   strategyArray,
				bestEffort: false,
			},
		},
	}

	r := newTestRouter(flows, d, &defaultAggregator{})

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
	d := &mockScatter{
		results: []upstreamResponse{
			{status: http.StatusOK, body: []byte(`"A"`), err: nil},
			{status: http.StatusInternalServerError, body: nil, err: &upstreamError{
				kind: upstreamTimeout,
				err:  errors.New("upstream timeout"),
			}},
		},
	}

	flows := []flow{
		{
			path:   "/test/partial/response",
			method: http.MethodGet,
			aggregation: aggregation{
				strategy:   strategyArray,
				bestEffort: true,
			},
		},
	}

	r := newTestRouter(flows, d, &defaultAggregator{})

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

	if resp.Errors[0] != ClientErrUpstreamUnavailable {
		t.Fatalf("unexpected error code: %s", resp.Errors[0])
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
	d := &mockScatter{
		results: []upstreamResponse{
			{status: http.StatusOK, body: []byte(`"A"`), err: nil},
			{status: http.StatusInternalServerError, body: nil, err: &upstreamError{
				kind: upstreamTimeout,
				err:  errors.New("upstream timeout"),
			}},
		},
	}

	flows := []flow{
		{
			path:   "/test/upstream/error",
			method: http.MethodGet,
			aggregation: aggregation{
				strategy:   strategyArray,
				bestEffort: false,
			},
		},
	}

	r := newTestRouter(flows, d, &defaultAggregator{})

	req := httptest.NewRequest(http.MethodGet, "/test/upstream/error", nil)
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	res := rec.Result()
	defer res.Body.Close()

	if res.StatusCode != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d", res.StatusCode)
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

	if resp.Errors[0] != ClientErrUpstreamUnavailable {
		t.Fatalf("unexpected error code: %s", resp.Errors[0])
	}
}

func TestRouter_ServeHTTP_UpstreamErrorPriority(t *testing.T) {
	d := &mockScatter{
		results: []upstreamResponse{
			{
				status: http.StatusInternalServerError,
				body:   nil,
				err: &upstreamError{
					kind: "unknown_error_kind", // will be mapped to InternalError
					err:  errors.New("upstream unknown_error_kind"),
				},
			},
			{
				status: http.StatusInternalServerError,
				body:   nil,
				err: &upstreamError{
					kind: upstreamTimeout,
					err:  errors.New("upstream timeout"),
				},
			},
		},
	}

	flows := []flow{
		{
			path:   "/test/upstream/error/priority",
			method: http.MethodGet,
			aggregation: aggregation{
				strategy:   strategyArray,
				bestEffort: true,
			},
		},
	}

	r := newTestRouter(flows, d, &defaultAggregator{})

	req := httptest.NewRequest(http.MethodGet, "/test/upstream/error/priority", nil)
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	res := rec.Result()
	defer res.Body.Close()

	if res.StatusCode != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d", res.StatusCode)
	}

	if ct := res.Header.Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("unexpected Content-Type: %s", ct)
	}

	body, _ := io.ReadAll(res.Body)

	resp := decodeJSONResponse(t, body)

	if resp.Data != nil {
		t.Fatalf("unexpected data: %v", resp.Data)
	}

	if len(resp.Errors) != 2 {
		t.Fatalf("expected 2 error, got %d", len(resp.Errors))
	}
}

func TestRouter_ServeHTTP_NoRoute(t *testing.T) {
	r := newTestRouter(nil, nil, nil)

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
		typ:  sdk.PluginTypeRequest,
		fn: func(_ sdk.Context) {
			executed = append(executed, "req")
		},
	}

	responsePlugin := &mockPlugin{
		name: "resp",
		typ:  sdk.PluginTypeResponse,
		fn: func(ctx sdk.Context) {
			executed = append(executed, "resp")
			ctx.Response().Header.Set("X-Plugin", "done")
		},
	}

	d := &mockScatter{
		results: []upstreamResponse{
			{status: http.StatusOK, body: []byte(`"OK"`), err: nil},
		},
	}

	flows := []flow{
		{
			path:    "/test/with/plugins",
			method:  http.MethodGet,
			plugins: []sdk.Plugin{requestPlugin, responsePlugin},
			aggregation: aggregation{
				strategy:   strategyArray,
				bestEffort: false,
			},
		},
	}

	r := newTestRouter(flows, d, &defaultAggregator{})

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
	d := &mockScatter{
		results: []upstreamResponse{
			{status: http.StatusOK, body: []byte(`"OK"`), err: nil},
		},
	}

	flows := []flow{
		{
			path:        "/test/with/middleware",
			method:      http.MethodGet,
			middlewares: []sdk.Middleware{&mockMiddleware{}},
		},
	}

	r := newTestRouter(flows, d, &defaultAggregator{})

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
