package main

import (
	"context"
	"crypto/rand"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"
	"go.uber.org/zap"

	"github.com/starwalkn/tokka"
	"github.com/starwalkn/tokka/internal/logger"
)

type contextKey string

type Middleware struct {
	enabled bool
	log     *zap.Logger
}

func NewMiddleware() tokka.Middleware {
	return &Middleware{}
}

func (m *Middleware) Name() string {
	return "requestid"
}

func (m *Middleware) Init(cfg map[string]interface{}) error {
	if val, ok := cfg["enabled"].(bool); ok {
		m.enabled = val
	} else {
		m.enabled = true
	}

	m.log = logger.New(false)

	return nil
}

func (m *Middleware) Handler(next http.Handler) http.Handler {
	if !m.enabled {
		return next
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := r.Header.Get("X-Request-ID")
		if requestID == "" {
			requestID = newRequestID()
			r.Header.Set("X-Request-ID", requestID)
		}

		w.Header().Set("X-Request-ID", requestID)
		r = r.WithContext(context.WithValue(r.Context(), contextKey("request_id"), requestID))

		next.ServeHTTP(w, r)
	})
}

func newRequestID() string {
	t := time.Now()
	entropy := ulid.Monotonic(rand.Reader, math.MaxInt64)

	return strings.ToLower(ulid.MustNew(ulid.Timestamp(t), entropy).String())
}
