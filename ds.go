package kono

type Flow struct {
	Path                 string
	Method               string
	Aggregation          AggregationConfig
	MaxParallelUpstreams int64
	Upstreams            []Upstream
	Plugins              []Plugin
	Middlewares          []Middleware
}
