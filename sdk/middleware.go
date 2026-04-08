package sdk

import "net/http"

// Middleware wraps an HTTP handler and executes within the request lifecycle.
// Middlewares are applied per-flow in the order they are defined.
type Middleware interface {
	Name() string
	Init(cfg map[string]interface{}) error
	Handler(next http.Handler) http.Handler
}
