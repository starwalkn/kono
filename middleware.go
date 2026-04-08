package kono

import (
	"go.uber.org/zap"

	"github.com/starwalkn/kono/sdk"
)

type Middleware = sdk.Middleware

func loadMiddleware(path string, cfg map[string]interface{}, log *zap.Logger) Middleware {
	factory := loadSymbol[func() Middleware](path, "NewMiddleware", log)
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
