package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"go.uber.org/zap"
)

func TestRequestIDMiddleware_ExistingID(t *testing.T) {
	m := &Middleware{
		log:     zap.NewNop(),
		enabled: true,
	}

	handler := m.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(r.Header.Get("X-Request-ID")))
	}))

	requestID := newRequestID()

	req := httptest.NewRequest(http.MethodGet, "/request/id", nil)
	req.Header.Set("X-Request-ID", requestID)

	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	if rec.Header().Get("X-Request-ID") != requestID {
		t.Fatalf("expected %s, got %s", requestID, rec.Header().Get("X-Request-ID"))
	}

	if rec.Body.String() != requestID {
		t.Fatalf("expected %s, got %s", requestID, rec.Body.String())
	}
}

func TestRequestIDMiddleware_GeneratedID(t *testing.T) {
	m := &Middleware{
		log:     zap.NewNop(),
		enabled: true,
	}

	handler := m.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(r.Header.Get("X-Request-ID")))
	}))

	req := httptest.NewRequest(http.MethodGet, "/request/id", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	if rec.Header().Get("X-Request-ID") == "" {
		t.Fatalf("expected new request_id, got empty header")
	}

	if rec.Body.String() == "" {
		t.Fatalf("expected new request_id, got empty header")
	}
}

func TestRequestIDMiddleware_Disabled(t *testing.T) {
	m := &Middleware{
		log:     zap.NewNop(),
		enabled: false,
	}

	handler := m.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(r.Header.Get("X-Request-ID")))
	}))

	req := httptest.NewRequest(http.MethodGet, "/request/id", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	if rec.Header().Get("X-Request-ID") != "" {
		t.Fatalf("middleware disabled, expected empty, got %s", rec.Header().Get("X-Request-ID"))
	}

	if rec.Body.String() != "" {
		t.Fatalf("middleware disabled, expected empty, got %s", rec.Body.String())
	}
}
