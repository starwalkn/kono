package kono

import (
	"fmt"
	"slices"
	"strings"

	"go.uber.org/zap"
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

func initPlugins(cfgs []PluginConfig, log *zap.Logger) []Plugin {
	plugins := make([]Plugin, 0, len(cfgs))

	for _, cfg := range cfgs {
		if slices.ContainsFunc(plugins, func(p Plugin) bool {
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

func initMiddlewares(cfgs []MiddlewareConfig, log *zap.Logger) []Middleware {
	middlewares := make([]Middleware, 0, len(cfgs))

	for _, cfg := range cfgs {
		if slices.ContainsFunc(middlewares, func(m Middleware) bool {
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
