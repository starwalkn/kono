package tokka

import "net/http"

type Context interface {
	Request() *http.Request
	Response() *http.Response
	Route() *Route
	Data() map[string]interface{}

	SetRequest(req *http.Request)
	SetResponse(resp *http.Response)
	SetData(key string, value interface{})
}

type defaultContext struct {
	req   *http.Request
	resp  *http.Response
	route *Route
	data  map[string]interface{}
}

func newContext(req *http.Request, route *Route) Context {
	return &defaultContext{
		req:   req,
		route: route,
		data:  make(map[string]interface{}),
	}
}

func (c *defaultContext) Request() *http.Request          { return c.req }
func (c *defaultContext) Response() *http.Response        { return c.resp }
func (c *defaultContext) Route() *Route                   { return c.route }
func (c *defaultContext) Data() map[string]interface{}    { return c.data }
func (c *defaultContext) SetRequest(r *http.Request)      { c.req = r }
func (c *defaultContext) SetResponse(r *http.Response)    { c.resp = r }
func (c *defaultContext) SetData(k string, v interface{}) { c.data[k] = v }
