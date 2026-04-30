package kono

import (
	"errors"
	"fmt"
	"plugin"

	"go.uber.org/zap"
)

func loadSymbol[T any](path, symbol string, log *zap.Logger) (T, error) {
	var zero T

	log = log.With(zap.String("path", path))

	p, err := plugin.Open(path)
	if err != nil {
		return zero, fmt.Errorf("open plugin: %w", err)
	}

	sym, err := p.Lookup(symbol)
	if err != nil {
		return zero, fmt.Errorf("lookup symbol: %w", err)
	}

	factory, ok := sym.(T)
	if !ok {
		return zero, errors.New("symbol has wrong signature")
	}

	pl := factory

	log.Debug("symbol loaded successfully")

	return pl, nil
}
