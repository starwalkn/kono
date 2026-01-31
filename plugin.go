package tokka

import (
	"go.uber.org/zap"
)

type PluginType int

const (
	PluginTypeRequest = iota
	PluginTypeResponse
)

type Plugin interface {
	Info() PluginInfo
	Init(cfg map[string]interface{})
	Type() PluginType
	Execute(ctx Context) error
}

type PluginInfo struct {
	Name        string
	Description string
	Version     string
	Author      string
}

func loadPlugin(path string, cfg map[string]interface{}, log *zap.Logger) Plugin {
	factory := loadSymbol[func() Plugin](path, "NewPlugin", log)

	plugin := factory()
	plugin.Init(cfg)

	return plugin
}
