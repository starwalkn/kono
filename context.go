package kono

import (
	"net/http"

	"github.com/starwalkn/kono/sdk"
)

// konoContext is the internal per-request context passed to plugins.
// It implements sdk.Context and is created once per request in newFlowHandler.
type konoContext struct {
	req  *http.Request
	resp *http.Response
}

func newContext(req *http.Request) sdk.Context {
	return &konoContext{req: req}
}

func (c *konoContext) Request() *http.Request {
	return c.req
}

func (c *konoContext) Response() *http.Response {
	return c.resp
}

func (c *konoContext) SetRequest(req *http.Request) {
	c.req = req
}

func (c *konoContext) SetResponse(resp *http.Response) {
	c.resp = resp
}
