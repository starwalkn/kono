package kono

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/starwalkn/kono/sdk"
)

var _ = Describe("Router", func() {
	Describe("ServeHTTP", func() {
		Context("with a successful flow", func() {
			It("aggregates upstream responses into an array", func() {
				d := &mockScatter{
					results: []upstreamResponse{
						{status: http.StatusOK, body: []byte(`"A"`), err: nil},
						{status: http.StatusOK, body: []byte(`"B"`), err: nil},
					},
				}

				r := newTestRouter([]flow{{
					path:   "/test/basic",
					method: http.MethodGet,
					aggregation: aggregation{
						strategy:   strategyArray,
						bestEffort: false,
					},
				}}, d, &defaultAggregator{})

				req := httptest.NewRequest(http.MethodGet, "/test/basic", nil)
				rec := httptest.NewRecorder()
				r.ServeHTTP(rec, req)

				res := rec.Result()
				defer res.Body.Close()
				body, _ := io.ReadAll(res.Body)
				resp := decodeJSONResponse(body)

				Expect(res.StatusCode).To(Equal(http.StatusOK))
				Expect(res.Header.Get("Content-Type")).To(ContainSubstring("application/json"))
				Expect(resp.Errors).To(BeEmpty())
				jsonEqual(`["A","B"]`, resp.Data)
			})
		})

		Context("with a partial response", func() {
			It("returns 206 and includes errors", func() {
				d := &mockScatter{
					results: []upstreamResponse{
						{status: http.StatusOK, body: []byte(`"A"`), err: nil},
						{status: http.StatusInternalServerError, body: nil, err: &upstreamError{
							kind: upstreamTimeout,
							err:  errors.New("upstream timeout"),
						}},
					},
				}

				r := newTestRouter([]flow{{
					path:   "/test/partial",
					method: http.MethodGet,
					aggregation: aggregation{
						strategy:   strategyArray,
						bestEffort: true,
					},
				}}, d, &defaultAggregator{})

				req := httptest.NewRequest(http.MethodGet, "/test/partial", nil)
				rec := httptest.NewRecorder()
				r.ServeHTTP(rec, req)

				res := rec.Result()
				defer res.Body.Close()
				body, _ := io.ReadAll(res.Body)
				resp := decodeJSONResponse(body)

				Expect(res.StatusCode).To(Equal(http.StatusPartialContent))
				Expect(resp.Errors).To(ConsistOf(ClientErrUpstreamUnavailable))
				jsonEqual(`["A"]`, resp.Data)
			})
		})

		Context("with all upstreams failing", func() {
			It("returns 502", func() {
				d := &mockScatter{
					results: []upstreamResponse{
						{status: http.StatusOK, body: []byte(`"A"`), err: nil},
						{status: http.StatusInternalServerError, err: &upstreamError{
							kind: upstreamTimeout,
							err:  errors.New("upstream timeout"),
						}},
					},
				}

				r := newTestRouter([]flow{{
					path:   "/test/error",
					method: http.MethodGet,
					aggregation: aggregation{
						strategy:   strategyArray,
						bestEffort: false,
					},
				}}, d, &defaultAggregator{})

				req := httptest.NewRequest(http.MethodGet, "/test/error", nil)
				rec := httptest.NewRecorder()
				r.ServeHTTP(rec, req)

				res := rec.Result()
				defer res.Body.Close()
				body, _ := io.ReadAll(res.Body)
				resp := decodeJSONResponse(body)

				Expect(res.StatusCode).To(Equal(http.StatusBadGateway))
				Expect(resp.Data).To(BeNil())
				Expect(resp.Errors).To(ConsistOf(ClientErrUpstreamUnavailable))
			})
		})

		Context("with multiple distinct upstream errors", func() {
			It("returns all errors and 502", func() {
				d := &mockScatter{
					results: []upstreamResponse{
						{err: &upstreamError{kind: "unknown_error_kind", err: errors.New("unknown")}},
						{err: &upstreamError{kind: upstreamTimeout, err: errors.New("timeout")}},
					},
				}

				r := newTestRouter([]flow{{
					path:   "/test/priority",
					method: http.MethodGet,
					aggregation: aggregation{
						strategy:   strategyArray,
						bestEffort: true,
					},
				}}, d, &defaultAggregator{})

				req := httptest.NewRequest(http.MethodGet, "/test/priority", nil)
				rec := httptest.NewRecorder()
				r.ServeHTTP(rec, req)

				res := rec.Result()
				defer res.Body.Close()
				body, _ := io.ReadAll(res.Body)
				resp := decodeJSONResponse(body)

				Expect(res.StatusCode).To(Equal(http.StatusBadGateway))
				Expect(resp.Errors).To(HaveLen(2))
			})
		})

		Context("when no flow matches", func() {
			It("returns 404", func() {
				r := newTestRouter(nil, nil, nil)

				req := httptest.NewRequest(http.MethodGet, "/missing", nil)
				rec := httptest.NewRecorder()
				r.ServeHTTP(rec, req)

				Expect(rec.Code).To(Equal(http.StatusNotFound))
			})
		})

		Context("with plugins", func() {
			It("runs request and response plugins in order", func() {
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
						{status: http.StatusOK, body: []byte(`"OK"`)},
					},
				}

				r := newTestRouter([]flow{{
					path:    "/test/plugins",
					method:  http.MethodGet,
					plugins: []sdk.Plugin{requestPlugin, responsePlugin},
					aggregation: aggregation{
						strategy:   strategyArray,
						bestEffort: false,
					},
				}}, d, &defaultAggregator{})

				req := httptest.NewRequest(http.MethodGet, "/test/plugins", nil)
				rec := httptest.NewRecorder()
				r.ServeHTTP(rec, req)

				res := rec.Result()
				defer res.Body.Close()
				body, _ := io.ReadAll(res.Body)
				resp := decodeJSONResponse(body)

				Expect(res.StatusCode).To(Equal(http.StatusOK))
				Expect(resp.Errors).To(BeEmpty())
				Expect(string(resp.Data)).To(Equal(`"OK"`))
				Expect(res.Header.Get("X-Plugin")).To(Equal("done"))
				Expect(executed).To(Equal([]string{"req", "resp"}))
			})
		})

		Context("with middleware", func() {
			It("runs middleware before the handler", func() {
				d := &mockScatter{
					results: []upstreamResponse{
						{status: http.StatusOK, body: []byte(`"OK"`)},
					},
				}

				r := newTestRouter([]flow{{
					path:        "/test/mw",
					method:      http.MethodGet,
					middlewares: []sdk.Middleware{&mockMiddleware{}},
				}}, d, &defaultAggregator{})

				req := httptest.NewRequest(http.MethodGet, "/test/mw", nil)
				rec := httptest.NewRecorder()
				r.ServeHTTP(rec, req)

				res := rec.Result()
				defer res.Body.Close()

				Expect(res.StatusCode).To(Equal(http.StatusOK))
				Expect(res.Header.Get("X-Middleware")).To(Equal("ok"))
			})
		})
	})

	Describe("computeFingerprint", func() {
		It("distinguishes header from query key with same name", func() {
			r1 := httptest.NewRequest(http.MethodGet, "/u/{id}?id=1", nil)
			r1.Header.Set("Accept", "*/*")

			r2 := httptest.NewRequest(http.MethodGet, "/u/{id}", nil)
			r2.Header.Set("Accept", "*/*")
			r2.Header.Set("Id", "anything")

			Expect(computeFingerprint(r1, "/u/{id}")).
				ToNot(Equal(computeFingerprint(r2, "/u/{id}")))
		})
	})
})
