package kono

type Route struct {
	Path                 string
	Method               string
	Upstreams            []Upstream
	Aggregation          AggregationConfig
	MaxParallelUpstreams int64
	Plugins              []Plugin
	Middlewares          []Middleware
}
