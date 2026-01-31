package tokka

import (
	"net/http"

	"go.uber.org/zap"
)

type Middleware interface {
	Name() string
	Init(cfg map[string]interface{}) error
	Handler(next http.Handler) http.Handler
}

func loadMiddleware(path string, cfg map[string]interface{}, log *zap.Logger) Middleware {
	factory := loadSymbol[func() Middleware](path, "NewMiddleware", log)

	mw := factory()
	if err := mw.Init(cfg); err != nil {
		log.Error("cannot initialize middleware", zap.String("name", mw.Name()), zap.Error(err))
		return nil
	}

	return mw
}
