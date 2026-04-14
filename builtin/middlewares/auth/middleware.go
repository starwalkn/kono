package main

import (
	"context"
	"crypto/rsa"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/starwalkn/kono/sdk"
)

type ctxKeyClaims struct{}

type keyResolver interface {
	KeyFunc(token *jwt.Token) (any, error)
}

type Middleware struct {
	Issuer   string
	Audience string
	Resolver keyResolver

	JWTConfig JWTConfig
}

type JWTConfig struct {
	Alg string

	HMACSecret         []byte         // For HS256.
	RSAPublicKey       *rsa.PublicKey // For static RS256.
	JWKSURL            string         // For JWKS.
	JWKSRefreshTimeout time.Duration
}

const (
	defaultLeeway        = 5 * time.Second
	authHeaderPartsCount = 2
)

func NewMiddleware() sdk.Middleware {
	return &Middleware{}
}

func (m *Middleware) Name() string {
	return "auth"
}

func (m *Middleware) Init(config map[string]interface{}) error {
	issuer, ok := config["issuer"].(string)
	if !ok {
		return errors.New("missing issuer")
	}
	m.Issuer = issuer

	audience, ok := config["audience"].(string)
	if !ok {
		return errors.New("missing audience")
	}
	m.Audience = audience

	alg, ok := config["alg"].(string)
	if !ok || alg == "" {
		return errors.New("missing or invalid alg")
	}

	jwtConfig := JWTConfig{
		Alg: alg,
	}

	hmacSecret, err := parseHMACSecret(config, "hmac_secret")
	if err != nil {
		return err
	}
	jwtConfig.HMACSecret = hmacSecret

	rsaPub, err := parseRSAPublicKey(config, "rsa_public_key")
	if err != nil {
		return err
	}
	jwtConfig.RSAPublicKey = rsaPub

	m.JWTConfig = jwtConfig

	resolver, err := m.newKeyResolver(m.JWTConfig)
	if err != nil {
		return err
	}

	m.Resolver = resolver

	return nil
}

func (m *Middleware) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			unauthorized(w)
			return
		}

		parts := strings.SplitN(authHeader, " ", authHeaderPartsCount)
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			unauthorized(w)
			return
		}

		tokenString := parts[1]

		token, err := jwt.ParseWithClaims(
			tokenString,
			&jwt.MapClaims{},
			m.Resolver.KeyFunc,
			jwt.WithValidMethods([]string{m.JWTConfig.Alg}),
			jwt.WithLeeway(defaultLeeway),
		)
		if err != nil || !token.Valid {
			unauthorized(w)
			return
		}

		claims, ok := token.Claims.(*jwt.MapClaims)
		if !ok {
			unauthorized(w)
			return
		}

		issuer, err := claims.GetIssuer()
		if err != nil {
			unauthorized(w)
			return
		}

		if issuer != m.Issuer {
			unauthorized(w)
			return
		}

		audience, err := claims.GetAudience()
		if err != nil {
			unauthorized(w)
			return
		}

		if !slices.Contains(audience, m.Audience) {
			unauthorized(w)
			return
		}

		ctx := context.WithValue(r.Context(), ctxKeyClaims{}, claims)

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func unauthorized(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write([]byte(`{"errors":[{"code":"UNAUTHORIZED"}]}`))
}

func (m *Middleware) newKeyResolver(cfg JWTConfig) (keyResolver, error) {
	switch cfg.Alg {
	case jwt.SigningMethodHS256.Alg():
		if len(cfg.HMACSecret) == 0 {
			return nil, errors.New("HMAC secret not configured")
		}

		return &hmacResolver{HMACSecret: cfg.HMACSecret}, nil
	case jwt.SigningMethodRS256.Alg():
		if cfg.JWKSURL != "" {
			resolver := &jwksResolver{
				url:            cfg.JWKSURL,
				keys:           make(map[string]*rsa.PublicKey, 0),
				refreshTimeout: cfg.JWKSRefreshTimeout,
			}

			if err := resolver.refresh(cfg.JWKSRefreshTimeout); err != nil {
				return nil, fmt.Errorf("cannot refresh JWKS: %w", err)
			}

			return resolver, nil
		}

		if cfg.RSAPublicKey != nil {
			return &rsaResolver{RSAPublic: cfg.RSAPublicKey}, nil
		}

		return nil, errors.New("RSA public key not configured")
	default:
		return nil, fmt.Errorf("unsupported signing method: %s", cfg.Alg)
	}
}
