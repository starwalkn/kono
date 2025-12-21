package tokka

import "net/http"

// Context is the internal interface that holds the request and response objects.
type Context interface {
	Request() *http.Request
	Response() *http.Response

	SetRequest(req *http.Request)
	SetResponse(resp *http.Response)
}

type defaultContext struct {
	req  *http.Request
	resp *http.Response
}

func newContext(req *http.Request) Context {
	return &defaultContext{
		req: req,
	}
}

func (c *defaultContext) Request() *http.Request       { return c.req }
func (c *defaultContext) Response() *http.Response     { return c.resp }
func (c *defaultContext) SetRequest(r *http.Request)   { c.req = r }
func (c *defaultContext) SetResponse(r *http.Response) { c.resp = r }
