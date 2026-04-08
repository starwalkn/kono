package kono

import (
	"net/http"

	"github.com/starwalkn/kono/sdk"
)

// Context is a type alias for sdk.Context — the public contract for plugin developers.
type Context = sdk.Context

// konoContext is the internal per-request context passed to plugins.
// It implements sdk.Context and is created once per request in newFlowHandler.
type konoContext struct {
	req  *http.Request
	resp *http.Response
}

func newContext(req *http.Request) Context {
	return &konoContext{req: req}
}

func (c *konoContext) Request() *http.Request {
	return c.req
}

func (c *konoContext) Response() *http.Response {
	return c.resp
}

func (c *konoContext) SetResponse(resp *http.Response) {
	c.resp = resp
}
