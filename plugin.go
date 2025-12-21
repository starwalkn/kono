package tokka

import (
	"go.uber.org/zap"
)

type Plugin interface {
	Name() string
	Init(cfg map[string]any)
	Type() PluginType
	Execute(ctx Context)
}

type PluginType int

const (
	PluginTypeRequest = iota
	PluginTypeResponse
)

type BasePlugin struct {
	pluginType PluginType
}

func (bp *BasePlugin) SetType(t PluginType) { bp.pluginType = t }
func (bp *BasePlugin) Type() PluginType     { return bp.pluginType }

func loadPluginFromSO(path string, cfg map[string]any, log *zap.Logger) Plugin {
	factory := loadSymbol[func() Plugin](path, "NewPlugin", log)

	plugin := factory()
	plugin.Init(cfg)

	return plugin
}
