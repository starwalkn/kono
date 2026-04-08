package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func newTestLogger(buf *bytes.Buffer) *zap.Logger {
	core := zapcore.NewCore(
		zapcore.NewJSONEncoder(zap.NewProductionEncoderConfig()),
		zapcore.AddSync(buf),
		zapcore.DebugLevel,
	)
	return zap.New(core)
}

func TestRecovererMiddleware_PanicRecovered(t *testing.T) {
	buf := new(bytes.Buffer)
	m := &Middleware{
		enabled: true,
		log:     newTestLogger(buf),
	}
	handler := m.Handler(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rec.Code)
	}

	body := rec.Body.String()
	if body != `{"errors":[{"code":"INTERNAL"}]}` {
		t.Fatalf("unexpected body: %s", body)
	}

	logOutput := buf.String()
	if logOutput == "" || !strings.Contains(logOutput, "panic recovered") {
		t.Errorf("expected panic log, got: %s", logOutput)
	}
}

func TestRecovererMiddleware_NoPanic(t *testing.T) {
	buf := new(bytes.Buffer)
	m := &Middleware{
		enabled: true,
		log:     newTestLogger(buf),
	}
	handler := m.Handler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	if rec.Body.String() != "ok" {
		t.Fatalf("unexpected body: %s", rec.Body.String())
	}

	if buf.Len() > 0 {
		t.Errorf("expected no logs, got: %s", buf.String())
	}
}

func TestRecovererMiddleware_IncludeStack(t *testing.T) {
	buf := new(bytes.Buffer)
	m := &Middleware{
		enabled:      true,
		log:          newTestLogger(buf),
		includeStack: true,
	}

	handler := m.Handler(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("test panic")
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rec.Code)
	}

	logOutput := buf.String()
	if !strings.Contains(logOutput, "panic recovered") {
		t.Errorf("expected panic log, got: %s", logOutput)
	}

	if !strings.Contains(logOutput, "stack") {
		t.Errorf("expected stack trace in log, got: %s", logOutput)
	}
}
