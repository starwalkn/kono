package main

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func makeHMACToken(t *testing.T, secret []byte, issuer, audience string, exp time.Time) string {
	claims := jwt.MapClaims{
		"iss": issuer,
		"aud": audience,
		"exp": exp.Unix(),
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)

	signed, err := token.SignedString(secret)
	if err != nil {
		t.Fatalf("failed to sign token: %v", err)
	}

	return signed
}

func TestAuthMiddleware_NoAuthHeader(t *testing.T) {
	m := &Middleware{
		Issuer:   "test-issuer",
		Audience: "test-aud",
		Resolver: &hmacResolver{HMACSecret: []byte("secret")},
		JWTConfig: JWTConfig{
			Alg:        "HS256",
			HMACSecret: []byte("secret"),
		},
	}

	handler := m.Handler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("ok"))
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestAuthMiddleware_InvalidToken(t *testing.T) {
	m := &Middleware{
		Issuer:   "test-issuer",
		Audience: "test-aud",
		Resolver: &hmacResolver{HMACSecret: []byte("secret")},
		JWTConfig: JWTConfig{
			Alg:        "HS256",
			HMACSecret: []byte("secret"),
		},
	}

	handler := m.Handler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("ok"))
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer invalid-token")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestAuthMiddleware_ExpiredToken(t *testing.T) {
	secret := []byte("secret")
	token := makeHMACToken(t, secret, "test-issuer", "test-aud", time.Now().Add(-time.Hour))

	m := &Middleware{
		Issuer:   "test-issuer",
		Audience: "test-aud",
		Resolver: &hmacResolver{HMACSecret: secret},
		JWTConfig: JWTConfig{
			Alg:        "HS256",
			HMACSecret: secret,
		},
	}

	handler := m.Handler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("ok"))
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestAuthMiddleware_ValidToken(t *testing.T) {
	secret := []byte("secret")
	token := makeHMACToken(t, secret, "test-issuer", "test-aud", time.Now().Add(time.Hour))

	m := &Middleware{
		Issuer:   "test-issuer",
		Audience: "test-aud",
		Resolver: &hmacResolver{HMACSecret: secret},
		JWTConfig: JWTConfig{
			Alg:        "HS256",
			HMACSecret: secret,
		},
	}

	var gotClaims *jwt.MapClaims
	handler := m.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims := r.Context().Value(ctxKeyClaims{}).(*jwt.MapClaims)
		gotClaims = claims

		w.Write([]byte("ok"))
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	if gotClaims == nil {
		t.Fatal("expected claims in context")
	}

	if iss, _ := gotClaims.GetIssuer(); iss != "test-issuer" {
		t.Fatalf("unexpected issuer: %s", iss)
	}

	aud, _ := gotClaims.GetAudience()
	if !slices.Contains(aud, "test-aud") {
		t.Fatalf("audience missing: %v", aud)
	}
}
