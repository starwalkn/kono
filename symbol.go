package tokka

import (
	"plugin"

	"go.uber.org/zap"
)

func loadSymbol[T any](path, symbol string, log *zap.Logger) T {
	var zero T

	log = log.With(zap.String("path", path))

	p, err := plugin.Open(path)
	if err != nil {
		log.Error("cannot open plugin", zap.Error(err))
		return zero
	}

	sym, err := p.Lookup(symbol)
	if err != nil {
		log.Error("symbol not found", zap.Error(err))
		return zero
	}

	factory, ok := sym.(T)
	if !ok {
		log.Error("symbol has wrong signature")
		return zero
	}

	pl := factory
	log.Info("symbol loaded successfully")

	return pl
}
