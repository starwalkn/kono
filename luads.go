package kono

import "net/http"

type Script struct {
	Source string
	Path   string
}

const luaWorkerSocketPath = "/tmp/kono-lua.sock"

const (
	luaActionContinue = "continue"
	luaActionAbort    = "abort"
)

const (
	luaMsgMaxSize      = 64 * 1024 * 1024 // 64 MB
	luaMsgExtraBufSize = 1024
)

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
