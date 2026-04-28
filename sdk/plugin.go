package sdk

// PluginType defines when a plugin executes in the request lifecycle.
type PluginType int

const (
	// PluginTypeRequest runs before upstream dispatch. Can modify request context.
	PluginTypeRequest PluginType = iota
	// PluginTypeResponse runs after aggregation. Can modify response headers and body.
	PluginTypeResponse PluginType = iota
)

func (pt PluginType) String() string {
	switch pt {
	case PluginTypeRequest:
		return "request"
	case PluginTypeResponse:
		return "response"
	default:
		return "unknown"
	}
}

// PluginInfo contains metadata about a plugin.
type PluginInfo struct {
	Name        string
	Description string
	Version     string
	Author      string
}

// Plugin is the interface that all Kono plugins must implement.
// Plugins are loaded as Go shared objects (.so) and must be compiled
// with the exact same Go version as the gateway binary.
type Plugin interface {
	Info() PluginInfo
	Init(cfg map[string]interface{})
	Type() PluginType
	Execute(ctx Context) error
}
