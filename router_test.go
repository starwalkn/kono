package kono

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	require.NoError(t, json.Unmarshal(body, &resp), "invalid JSON response: %s", body)

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

func (m *mockPlugin) Init(_ map[string]interface{}) error { return nil }
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

	body, _ := io.ReadAll(res.Body)
	resp := decodeJSONResponse(t, body)

	require.Equal(t, http.StatusOK, res.StatusCode)
	assert.Contains(t, res.Header.Get("Content-Type"), "application/json")
	assert.Empty(t, resp.Errors)

	var got []string
	require.NoError(t, json.Unmarshal(resp.Data, &got))
	assert.ElementsMatch(t, []string{"A", "B"}, got)
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

	body, _ := io.ReadAll(res.Body)
	resp := decodeJSONResponse(t, body)

	require.Equal(t, http.StatusPartialContent, res.StatusCode)
	assert.Contains(t, res.Header.Get("Content-Type"), "application/json")
	require.Len(t, resp.Errors, 1)
	assert.Equal(t, ClientErrUpstreamUnavailable, resp.Errors[0])

	var got []string
	require.NoError(t, json.Unmarshal(resp.Data, &got))
	assert.Equal(t, []string{"A"}, got)
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

	body, _ := io.ReadAll(res.Body)
	resp := decodeJSONResponse(t, body)

	require.Equal(t, http.StatusBadGateway, res.StatusCode)
	assert.Contains(t, res.Header.Get("Content-Type"), "application/json")
	assert.Nil(t, resp.Data)
	require.Len(t, resp.Errors, 1)
	assert.Equal(t, ClientErrUpstreamUnavailable, resp.Errors[0])
}

func TestRouter_ServeHTTP_UpstreamErrorPriority(t *testing.T) {
	d := &mockScatter{
		results: []upstreamResponse{
			{
				status: http.StatusInternalServerError,
				body:   nil,
				err: &upstreamError{
					kind: "unknown_error_kind",
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

	body, _ := io.ReadAll(res.Body)
	resp := decodeJSONResponse(t, body)

	require.Equal(t, http.StatusBadGateway, res.StatusCode)
	assert.Contains(t, res.Header.Get("Content-Type"), "application/json")
	assert.Nil(t, resp.Data)
	assert.Len(t, resp.Errors, 2)
}

func TestRouter_ServeHTTP_NoRoute(t *testing.T) {
	r := newTestRouter(nil, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/test/not/found", nil)
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	res := rec.Result()
	defer res.Body.Close()

	assert.Equal(t, http.StatusNotFound, res.StatusCode)
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

	body, _ := io.ReadAll(res.Body)
	resp := decodeJSONResponse(t, body)

	require.Equal(t, http.StatusOK, res.StatusCode)
	assert.Contains(t, res.Header.Get("Content-Type"), "application/json")
	assert.Empty(t, resp.Errors)
	assert.Equal(t, `"OK"`, string(resp.Data))
	assert.Equal(t, "done", res.Header.Get("X-Plugin"))
	assert.Equal(t, []string{"req", "resp"}, executed)
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

	require.Equal(t, http.StatusOK, res.StatusCode)
	assert.Contains(t, res.Header.Get("Content-Type"), "application/json")
	assert.Equal(t, "ok", res.Header.Get("X-Middleware"))
}

func TestComputeFingerprint_HeaderQueryAmbiguity(t *testing.T) {
	r1 := httptest.NewRequest(http.MethodGet, "/u/{id}?id=1", nil)
	r1.Header.Set("Accept", "*/*")

	r2 := httptest.NewRequest(http.MethodGet, "/u/{id}", nil)
	r2.Header.Set("Accept", "*/*")
	r2.Header.Set("Id", "anything")

	f1 := computeFingerprint(r1, "/u/{id}")
	f2 := computeFingerprint(r2, "/u/{id}")

	assert.NotEqual(t, f1, f2, "fingerprint must distinguish header from query key with same name")
}
