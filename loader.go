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

func initPlugins(cfgs []PluginConfig, log *zap.Logger) ([]sdk.Plugin, error) {
	plugins := make([]sdk.Plugin, 0, len(cfgs))

	for _, cfg := range cfgs {
		if slices.ContainsFunc(plugins, func(p sdk.Plugin) bool {
			return p.Info().Name == cfg.Name
		}) {
			continue
		}

		soPath, err := resolveSoPath(cfg.Source, cfg.Name, cfg.Path, builtinPluginsPath)
		if err != nil {
			return nil, fmt.Errorf("resolve .so path: %w", err)
		}

		plugin, err := loadPlugin(soPath, cfg.Config, log)
		if err != nil {
			if cfg.Source == sourceBuiltin {
				return nil, fmt.Errorf("cannot load builtin plugin %q: %w", cfg.Name, err)
			}

			return nil, fmt.Errorf("cannot load plugin %q from path %q: %w", cfg.Name, cfg.Path, err)
		}

		log.Info("plugin initialized", zap.String("name", plugin.Info().Name))

		plugins = append(plugins, plugin)
	}

	return plugins, nil
}

func initMiddlewares(cfgs []MiddlewareConfig, log *zap.Logger) ([]sdk.Middleware, error) {
	middlewares := make([]sdk.Middleware, 0, len(cfgs))

	for _, cfg := range cfgs {
		if slices.ContainsFunc(middlewares, func(m sdk.Middleware) bool {
			return m.Name() == cfg.Name
		}) {
			continue
		}

		soPath, err := resolveSoPath(cfg.Source, cfg.Name, cfg.Path, builtinMiddlewaresPath)
		if err != nil {
			return nil, fmt.Errorf("resolve .so path: %w", err)
		}

		middleware, err := loadMiddleware(soPath, cfg.Config, log)
		if err != nil {
			if cfg.Source == sourceBuiltin {
				return nil, fmt.Errorf("cannot load builtin middleware %q: %w", cfg.Name, err)
			}

			return nil, fmt.Errorf("cannot load middleware %q from path %q: %w", cfg.Name, cfg.Path, err)
		}

		log.Info("middleware initialized", zap.String("name", middleware.Name()))

		middlewares = append(middlewares, middleware)
	}

	return middlewares, nil
}

func resolveSoPath(source, name, filePath, builtinPath string) (string, error) {
	switch source {
	case sourceBuiltin:
		return builtinPath + name + extSo, nil
	case sourceFile:
		base := filePath
		if !strings.HasSuffix(base, "/") {
			base += "/"
		}

		return base + name + extSo, nil
	default:
		return "", fmt.Errorf("invalid plugin source %q", source)
	}
}

func loadPlugin(path string, cfg map[string]interface{}, log *zap.Logger) (sdk.Plugin, error) {
	factory, err := loadSymbol[func() sdk.Plugin](path, "NewPlugin", log)
	if err != nil {
		return nil, fmt.Errorf("load plugin symbol: %w", err)
	}

	p := factory()
	if p == nil {
		return nil, fmt.Errorf("plugin factory for path %q returned nil", path)
	}

	if err = p.Init(cfg); err != nil {
		return nil, fmt.Errorf("init plugin %s: %w", p.Info().Name, err)
	}

	return p, nil
}

func loadMiddleware(path string, cfg map[string]interface{}, log *zap.Logger) (sdk.Middleware, error) {
	factory, err := loadSymbol[func() sdk.Middleware](path, "NewMiddleware", log)
	if err != nil {
		return nil, fmt.Errorf("load middleware symbol: %w", err)
	}

	mw := factory()
	if mw == nil {
		return nil, fmt.Errorf("middleware factory for path %q returned nil", path)
	}

	if err = mw.Init(cfg); err != nil {
		return nil, fmt.Errorf("init middleware %s: %w", mw.Name(), err)
	}

	return mw, nil
}
