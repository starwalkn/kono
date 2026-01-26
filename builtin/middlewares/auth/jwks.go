package main

import (
	"context"
	"crypto/rsa"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/lestrrat-go/jwx/jwk"
)

type jwksResolver struct {
	url            string
	keys           map[string]*rsa.PublicKey
	mu             sync.RWMutex
	refreshTimeout time.Duration
}

func (r *jwksResolver) KeyFunc(token *jwt.Token) (any, error) {
	if token.Method != jwt.SigningMethodRS256 {
		return nil, fmt.Errorf("invalid signing method: %T", token.Method)
	}

	if r.url == "" {
		return nil, errors.New("JWKS URL not configured")
	}

	kid, ok := token.Header["kid"].(string)
	if !ok {
		return nil, errors.New("missing kid in token header")
	}

	r.mu.RLock()
	key, ok := r.keys[kid]
	r.mu.RUnlock()

	if ok {
		return key, nil
	}

	// Refresh JWKS and try again.
	if err := r.refresh(r.refreshTimeout); err != nil {
		return nil, err
	}

	r.mu.RLock()
	key, ok = r.keys[kid]
	r.mu.RUnlock()

	if !ok {
		return nil, errors.New("key not found in JWKS")
	}

	return key, nil
}

func (r *jwksResolver) refresh(timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	set, err := jwk.Fetch(ctx, r.url)
	if err != nil {
		return fmt.Errorf("failed to fetch JWKS: %w", err)
	}

	newKeys := make(map[string]*rsa.PublicKey)

	for i := range set.Len() {
		key, ok := set.Get(i)
		if !ok {
			continue
		}

		var rsaKey *rsa.PublicKey
		if err = key.Raw(&rsaKey); err != nil {
			continue // Skip unsupported keys.
		}

		if kid := key.KeyID(); kid != "" {
			newKeys[kid] = rsaKey
		}
	}

	r.mu.Lock()
	r.keys = newKeys
	r.mu.Unlock()

	return nil
}
