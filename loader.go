package kono

import (
	"fmt"
	"slices"
	"strings"

	"go.uber.org/zap"

	"github.com/starwalkn/kono/sdk"
)

const (
	sourceBuiltin = "builtin"
	sourceFile    = "file"
)

const (
	builtinPluginsPath     = "/usr/local/lib/kono/plugins/"
	builtinMiddlewaresPath = "/usr/local/lib/kono/middlewares/"
)

const extSo = ".so"

func initPlugins(cfgs []PluginConfig, log *zap.Logger) []sdk.Plugin {
	plugins := make([]sdk.Plugin, 0, len(cfgs))

	for _, cfg := range cfgs {
		if slices.ContainsFunc(plugins, func(p sdk.Plugin) bool {
			return p.Info().Name == cfg.Name
		}) {
			continue
		}

		soPath := resolveSoPath(cfg.Source, cfg.Name, cfg.Path, builtinPluginsPath)

		plugin := loadPlugin(soPath, cfg.Config, log)
		if plugin == nil {
			log.Fatal("failed to load plugin",
				zap.String("plugin", cfg.Name),
				zap.String("path", cfg.Path),
			)
		}

		log.Info("plugin initialized", zap.String("name", plugin.Info().Name))
		plugins = append(plugins, plugin)
	}

	return plugins
}

func initMiddlewares(cfgs []MiddlewareConfig, log *zap.Logger) []sdk.Middleware {
	middlewares := make([]sdk.Middleware, 0, len(cfgs))

	for _, cfg := range cfgs {
		if slices.ContainsFunc(middlewares, func(m sdk.Middleware) bool {
			return m.Name() == cfg.Name
		}) {
			continue
		}

		soPath := resolveSoPath(cfg.Source, cfg.Name, cfg.Path, builtinMiddlewaresPath)

		middleware := loadMiddleware(soPath, cfg.Config, log)
		if middleware == nil {
			log.Fatal("failed to load middleware",
				zap.String("name", cfg.Name),
				zap.String("path", cfg.Path),
			)
		}

		log.Info("middleware initialized", zap.String("name", middleware.Name()))
		middlewares = append(middlewares, middleware)
	}

	return middlewares
}

// resolveSoPath строит путь к .so файлу по source и имени.
func resolveSoPath(source, name, filePath, builtinPath string) string {
	switch source {
	case sourceBuiltin:
		return builtinPath + name + extSo
	case sourceFile:
		base := filePath
		if !strings.HasSuffix(base, "/") {
			base += "/"
		}

		return base + name + extSo
	default:
		panic(fmt.Sprintf("invalid source '%s'", source))
	}
}

func loadPlugin(path string, cfg map[string]interface{}, log *zap.Logger) sdk.Plugin {
	factory := loadSymbol[func() sdk.Plugin](path, "NewPlugin", log)
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

func loadMiddleware(path string, cfg map[string]interface{}, log *zap.Logger) sdk.Middleware {
	factory := loadSymbol[func() sdk.Middleware](path, "NewMiddleware", log)
	if factory == nil {
		return nil
	}

	mw := factory()
	if mw == nil {
		log.Error("middleware factory returned nil", zap.String("path", path))
		return nil
	}

	if err := mw.Init(cfg); err != nil {
		log.Error("cannot initialize middleware",
			zap.String("name", mw.Name()),
			zap.Error(err),
		)

		return nil
	}

	return mw
}
