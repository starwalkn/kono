package kono

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/starwalkn/kono/sdk"
)

var _ = Describe("Passthrough", func() {
	Context("with a streaming SSE response", func() {
		It("forwards body and headers without buffering", func() {
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
			body, _ := io.ReadAll(res.Body)

			Expect(res.StatusCode).To(Equal(http.StatusOK))
			Expect(res.Header.Get("Content-Type")).To(Equal("text/event-stream"))
			Expect(res.Header.Get("Cache-Control")).To(Equal("no-cache"))
			Expect(res.Header.Get("Content-Length")).To(BeEmpty())
			Expect(string(body)).To(Equal("data: hello\n\n"))
		})
	})

	Context("with multiple SSE events", func() {
		It("forwards all events in order", func() {
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
			body, _ := io.ReadAll(res.Body)

			Expect(res.StatusCode).To(Equal(http.StatusOK))
			Expect(string(body)).To(Equal(events))
			Expect(strings.Count(string(body), "data:")).To(Equal(3))
		})
	})

	Context("with a request plugin", func() {
		It("runs the plugin before forwarding to upstream", func() {
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

			r := newTestRouter([]flow{passthroughFlow("/plugin", u, plugin)}, nil, nil)
			req := httptest.NewRequest(http.MethodGet, "/plugin", nil)
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)

			Expect(rec.Code).To(Equal(http.StatusOK))
			Expect(pluginSaw).To(Equal("injected"))
		})
	})

	Context("when upstream errors before any write", func() {
		It("returns 502", func() {
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

			Expect(rec.Code).To(Equal(http.StatusBadGateway))
		})
	})

	Context("when upstream errors after partial write", func() {
		It("does not panic and preserves the original status", func() {
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

			Expect(func() { r.ServeHTTP(rec, req) }).ToNot(Panic())
			Expect(rec.Code).To(Equal(http.StatusOK))
		})
	})

	Context("with a non-proxy-capable upstream", func() {
		It("returns 500", func() {
			u := &stubUpstream{upstreamName: "plain"}

			r := newTestRouter([]flow{{
				path:        "/noproxy",
				method:      http.MethodGet,
				passthrough: true,
				upstreams:   []upstream{u},
			}}, nil, nil)

			req := httptest.NewRequest(http.MethodGet, "/noproxy", nil)
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)

			Expect(rec.Code).To(Equal(http.StatusInternalServerError))
		})
	})

	Context("with multiple upstreams", func() {
		It("returns 500 because passthrough requires exactly one", func() {
			makeU := func(n string) upstream {
				return &mockProxyUpstream{
					upstreamName: n,
					proxyFn: func(w http.ResponseWriter, _ *http.Request) error {
						w.WriteHeader(http.StatusOK)
						return nil
					},
				}
			}

			r := newTestRouter([]flow{{
				path:        "/multi",
				method:      http.MethodGet,
				passthrough: true,
				upstreams:   []upstream{makeU("a"), makeU("b")},
			}}, nil, nil)

			req := httptest.NewRequest(http.MethodGet, "/multi", nil)
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)

			Expect(rec.Code).To(Equal(http.StatusInternalServerError))
		})
	})

	Context("with a non-200 upstream status", func() {
		It("preserves the status code", func() {
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

			Expect(rec.Code).To(Equal(http.StatusCreated))
		})
	})
})
