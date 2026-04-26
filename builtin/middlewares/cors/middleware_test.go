package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func newCORSMiddleware(cfg map[string]interface{}) *Middleware {
	m := &Middleware{}
	_ = m.Init(cfg)
	return m
}

func newHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
}

func TestCORSMiddleware_NoOrigin(t *testing.T) {
	m := newCORSMiddleware(map[string]interface{}{
		"allowed_origins": []interface{}{"https://myapp.com"},
	})

	handler := m.Handler(newHandler())

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	if rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("expected no CORS headers for request without Origin")
	}
}

func TestCORSMiddleware_AllowedOrigin(t *testing.T) {
	m := newCORSMiddleware(map[string]interface{}{
		"allowed_origins": []interface{}{"https://myapp.com"},
	})

	handler := m.Handler(newHandler())

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Origin", "https://myapp.com")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://myapp.com" {
		t.Fatalf("expected Access-Control-Allow-Origin: https://myapp.com, got %q", got)
	}
}

func TestCORSMiddleware_DisallowedOrigin(t *testing.T) {
	m := newCORSMiddleware(map[string]interface{}{
		"allowed_origins": []interface{}{"https://myapp.com"},
	})

	handler := m.Handler(newHandler())

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Origin", "https://evil.com")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rec.Code)
	}
}

func TestCORSMiddleware_WildcardOrigin(t *testing.T) {
	m := newCORSMiddleware(map[string]interface{}{
		"allowed_origins": []interface{}{"*"},
	})

	handler := m.Handler(newHandler())

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Origin", "https://anyone.com")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("expected Access-Control-Allow-Origin: *, got %q", got)
	}
}

func TestCORSMiddleware_Preflight(t *testing.T) {
	m := newCORSMiddleware(map[string]interface{}{
		"allowed_origins": []interface{}{"https://myapp.com"},
		"allowed_methods": []interface{}{"GET", "POST"},
		"allowed_headers": []interface{}{"Content-Type", "Authorization"},
	})

	handler := m.Handler(newHandler())

	req := httptest.NewRequest(http.MethodOptions, "/", nil)
	req.Header.Set("Origin", "https://myapp.com")
	req.Header.Set("Access-Control-Request-Method", "POST")

	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", rec.Code)
	}

	if got := rec.Header().Get("Access-Control-Allow-Methods"); got == "" {
		t.Fatal("expected Access-Control-Allow-Methods header")
	}

	if got := rec.Header().Get("Access-Control-Allow-Headers"); got == "" {
		t.Fatal("expected Access-Control-Allow-Headers header")
	}
}

func TestCORSMiddleware_OptionsPassthrough(t *testing.T) {
	m := newCORSMiddleware(map[string]interface{}{
		"allowed_origins": []interface{}{"https://myapp.com"},
	})

	var reached bool
	handler := m.Handler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodOptions, "/", nil)
	req.Header.Set("Origin", "https://myapp.com")

	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if !reached {
		t.Fatal("expected OPTIONS request to pass through to handler")
	}
}

func TestCORSMiddleware_AllowCredentials(t *testing.T) {
	m := newCORSMiddleware(map[string]interface{}{
		"allowed_origins":   []interface{}{"https://myapp.com"},
		"allow_credentials": true,
	})

	handler := m.Handler(newHandler())

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Origin", "https://myapp.com")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Fatalf("expected Access-Control-Allow-Credentials: true, got %q", got)
	}
}

func TestCORSMiddleware_VaryHeader(t *testing.T) {
	m := newCORSMiddleware(map[string]interface{}{
		"allowed_origins": []interface{}{"https://myapp.com"},
	})

	handler := m.Handler(newHandler())

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Origin", "https://myapp.com")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if got := rec.Header().Get("Vary"); got != "Origin" {
		t.Fatalf("expected Vary: Origin, got %q", got)
	}
}

func TestCORSMiddleware_WildcardNoVary(t *testing.T) {
	m := newCORSMiddleware(map[string]interface{}{
		"allowed_origins": []interface{}{"*"},
	})

	handler := m.Handler(newHandler())

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Origin", "https://anyone.com")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if got := rec.Header().Get("Vary"); got != "" {
		t.Fatalf("expected no Vary header for wildcard origin, got %q", got)
	}
}

func TestCORSMiddleware_CredentialsWithWildcard_Error(t *testing.T) {
	m := &Middleware{}
	err := m.Init(map[string]interface{}{
		"allowed_origins":   []interface{}{"*"},
		"allow_credentials": true,
	})

	if err == nil {
		t.Fatal("expected error for credentials with wildcard origin")
	}
}
