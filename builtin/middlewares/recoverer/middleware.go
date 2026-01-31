package main

import (
	"fmt"
	"net/http"
	"runtime/debug"
	"time"

	"go.uber.org/zap"

	"github.com/starwalkn/tokka"
	"github.com/starwalkn/tokka/internal/logger"
)

type Middleware struct {
	enabled      bool
	includeStack bool
	log          *zap.Logger
}

func NewMiddleware() tokka.Middleware {
	return &Middleware{}
}

func (m *Middleware) Name() string {
	return "recoverer"
}

func (m *Middleware) Init(cfg map[string]interface{}) error {
	if val, ok := cfg["enabled"].(bool); ok {
		m.enabled = val
	}

	if val, ok := cfg["include_stack"].(bool); ok {
		m.includeStack = val
	}

	m.log = logger.New(false)

	return nil
}

func (m *Middleware) Handler(next http.Handler) http.Handler {
	if !m.enabled {
		return next
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				now := time.Now()
				msg := fmt.Sprintf("panic recovered: %v", rec)

				log := m.log.With(
					zap.String("method", r.Method),
					zap.String("path", r.URL.Path),
					zap.Time("time", now),
				)

				if m.includeStack {
					log.Error(msg, zap.ByteString("stack", debug.Stack()))
				} else {
					log.Error(msg)
				}

				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"error": "internal server error"}`))
			}
		}()

		next.ServeHTTP(w, r)
	})
}
