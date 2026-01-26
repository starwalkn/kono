package main

import (
	"encoding/base64"
	"errors"
	"fmt"

	"github.com/golang-jwt/jwt/v5"
)

type hmacResolver struct {
	HMACSecret []byte
}

func (r *hmacResolver) KeyFunc(token *jwt.Token) (any, error) {
	if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
		return nil, fmt.Errorf("invalid signing method: %T", token.Method)
	}

	if len(r.HMACSecret) == 0 {
		return nil, errors.New("HMAC secret not configured")
	}

	return r.HMACSecret, nil
}

func parseHMACSecret(config map[string]any, key string) ([]byte, error) {
	raw, ok := config[key]
	if !ok {
		return nil, nil
	}

	str, ok := raw.(string)
	if !ok {
		return nil, fmt.Errorf("%s must be a string", key)
	}

	secret, err := base64.StdEncoding.DecodeString(str)
	if err != nil {
		return nil, fmt.Errorf("failed to decode %s: %w", key, err)
	}

	return secret, nil
}
