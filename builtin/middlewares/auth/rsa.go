package main

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"

	"github.com/golang-jwt/jwt/v5"
)

type rsaResolver struct {
	RSAPublic *rsa.PublicKey
}

func (r *rsaResolver) KeyFunc(token *jwt.Token) (any, error) {
	if _, ok := token.Method.(*jwt.SigningMethodRSA); !ok {
		return nil, fmt.Errorf("invalid signing method: %T", token.Method)
	}

	if r.RSAPublic == nil {
		return nil, errors.New("RSA public key not configured")
	}

	return r.RSAPublic, nil
}

func parseRSAPublicKey(config map[string]any, key string) (*rsa.PublicKey, error) {
	raw, ok := config[key]
	if !ok {
		return nil, nil //nolint:nilnil // its ok here
	}

	pemStr, ok := raw.(string)
	if !ok {
		return nil, fmt.Errorf("%s must be a string", key)
	}

	block, _ := pem.Decode([]byte(pemStr))
	if block == nil {
		return nil, fmt.Errorf("failed to decode PEM block for %s", key)
	}

	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse public key %s: %w", key, err)
	}

	rsaPub, ok := pub.(*rsa.PublicKey)
	if !ok {
		return nil, fmt.Errorf("%s is not RSA public key", key)
	}

	return rsaPub, nil
}
