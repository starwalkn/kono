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

	"github.com/starwalkn/tokka"
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

	HMACSecret          []byte         // For HS256.
	RSAPublicKey        *rsa.PublicKey // For static RS256.
	JWKSURL             string         // For JWKS.
	JWKSRefsreshTimeout time.Duration
}

const defaultLeeway = 5 * time.Second

func NewMiddleware() tokka.Middleware {
	return &Middleware{}
}

func (m *Middleware) Name() string {
	return "auth"
}

func (m *Middleware) Init(config map[string]any) error {
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

	jwtConfig := JWTConfig{
		Alg: config["alg"].(string),
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
			http.Error(w, "missing authorization header", http.StatusUnauthorized)
			return
		}

		parts := strings.SplitN(authHeader, " ", 2) //nolint:mnd // it is not magic, it is a fuckin auth header parts
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			http.Error(w, "invalid authorization header", http.StatusUnauthorized)
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
			http.Error(w, "invalid token", http.StatusUnauthorized)
			return
		}

		claims, ok := token.Claims.(*jwt.MapClaims)
		if !ok {
			http.Error(w, "invalid token claims", http.StatusUnauthorized)
			return
		}

		expirationTime, err := claims.GetExpirationTime()
		if err != nil {
			http.Error(w, "invalid token expiration time", http.StatusUnauthorized)
			return
		}

		if expirationTime.Before(time.Now()) {
			http.Error(w, "token expired", http.StatusUnauthorized)
			return
		}

		issuer, err := claims.GetIssuer()
		if err != nil {
			http.Error(w, "invalid token issuer", http.StatusUnauthorized)
			return
		}

		if issuer != m.Issuer {
			http.Error(w, "invalid token issuer", http.StatusUnauthorized)
			return
		}

		audience, err := claims.GetAudience()
		if err != nil {
			http.Error(w, "invalid token audience", http.StatusUnauthorized)
			return
		}

		if !slices.Contains(audience, m.Audience) {
			http.Error(w, "invalid token audience", http.StatusUnauthorized)
			return
		}

		ctx := context.WithValue(r.Context(), ctxKeyClaims{}, claims)

		next.ServeHTTP(w, r.WithContext(ctx))
	})
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
				refreshTimeout: cfg.JWKSRefsreshTimeout,
			}

			if err := resolver.refresh(cfg.JWKSRefsreshTimeout); err != nil {
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
