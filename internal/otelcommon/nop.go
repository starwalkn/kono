package otelcommon

import "context"

type nopProvider struct{}

func (nopProvider) Shutdown(ctx context.Context) error { return nil }

func NewNopProvider() Provider {
	return nopProvider{}
}
