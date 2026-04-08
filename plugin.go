package kono

import (
	"go.uber.org/zap"

	"github.com/starwalkn/kono/sdk"
)

type Plugin = sdk.Plugin
type PluginInfo = sdk.PluginInfo
type PluginType = sdk.PluginType

func loadPlugin(path string, cfg map[string]interface{}, log *zap.Logger) Plugin {
	factory := loadSymbol[func() Plugin](path, "NewPlugin", log)
	if factory == nil {
		return nil
	}

	p := factory()
	if p == nil {
		log.Error("plugin factory returned nil", zap.String("path", path))
		return nil
	}

	p.Init(cfg)

	return p
}
