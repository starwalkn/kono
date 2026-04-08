package sdk

import "net/http"

// Context is the gateway's per-request context passed to plugins.
// It provides access to the current request and the aggregated response.
type Context interface {
	Request() *http.Request
	Response() *http.Response
	SetResponse(resp *http.Response)
}
