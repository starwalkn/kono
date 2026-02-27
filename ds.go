package kono

import "net/http"

type Flow struct {
	Path                 string
	Method               string
	Aggregation          AggregationConfig
	MaxParallelUpstreams int64
	Upstreams            []Upstream
	Scripts              []Script
	Plugins              []Plugin
	Middlewares          []Middleware
}

type Script struct {
	Source string
	Path   string
}

const (
	luaActionContinue = "continue"
	luaActionAbort    = "abort"
)

const luaMsgExtraBufSize = 1024

type LuaJSONRequest struct {
	RequestID string      `json:"request_id"`
	Method    string      `json:"method"`
	Path      string      `json:"path"`
	Query     string      `json:"query"`
	Headers   http.Header `json:"headers"`
	Body      []byte      `json:"body"`
	ClientIP  string      `json:"client_ip"`
}

type LuaJSONResponse struct {
	Action string `json:"action"`
	Status int    `json:"status"`
	Error  string `json:"error"`

	LuaJSONRequest
}
