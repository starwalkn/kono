package main

import (
	"bytes"
	"io"
	"net/http"
	"time"

	"go.uber.org/zap"

	"github.com/starwalkn/tokka"
	"github.com/starwalkn/tokka/internal/logger"
)

type Middleware struct {
	enabled bool
	logBody bool
	log     *zap.Logger
}

func NewMiddleware() tokka.Middleware {
	return &Middleware{}
}

func (m *Middleware) Name() string {
	return "logger"
}

func (m *Middleware) Init(cfg map[string]interface{}) error {
	if val, ok := cfg["enabled"].(bool); ok {
		m.enabled = val
	} else {
		m.enabled = true
	}

	if val, ok := cfg["log_body"].(bool); ok {
		m.logBody = val
	}

	m.log = logger.New(false)

	return nil
}

func (m *Middleware) Handler(next http.Handler) http.Handler {
	if !m.enabled {
		return next
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		var bodyCopy []byte
		if m.logBody && r.Body != nil {
			bodyCopy, _ = io.ReadAll(r.Body)
			r.Body = io.NopCloser(bytes.NewReader(bodyCopy))
		}

		m.log.Info("request started",
			zap.String("method", r.Method),
			zap.String("path", r.URL.Path),
			zap.String("remote_addr", r.RemoteAddr),
		)

		rec := &responseRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)

		duration := time.Since(start)

		fields := []zap.Field{
			zap.Int("status", rec.status),
			zap.Duration("duration", duration),
			zap.String("method", r.Method),
			zap.String("path", r.URL.Path),
		}

		if m.logBody && len(bodyCopy) > 0 {
			fields = append(fields, zap.ByteString("body", bodyCopy))
		}

		m.log.Info("request completed", fields...)
	})
}

type responseRecorder struct {
	http.ResponseWriter
	status int
}

func (r *responseRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}
