package main

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/starwalkn/kono/sdk"
)

type Middleware struct {
	allowedOrigins   map[string]struct{}
	allowedMethods   []string
	allowedHeaders   []string
	allowCredentials bool
	maxAge           int
	allowAll         bool
}

func NewMiddleware() sdk.Middleware {
	return &Middleware{}
}

func (m *Middleware) Name() string {
	return "cors"
}

func (m *Middleware) Init(cfg map[string]interface{}) error {
	m.allowedOrigins = make(map[string]struct{})

	if origins, ok := cfg["allowed_origins"].([]interface{}); ok {
		m.resolveAllowedOrigins(origins)
	}

	if methods, ok := cfg["allowed_methods"].([]interface{}); ok {
		for _, method := range methods {
			if s, mok := method.(string); mok {
				m.allowedMethods = append(m.allowedMethods, strings.ToUpper(s))
			}
		}
	}

	if headers, ok := cfg["allowed_headers"].([]interface{}); ok {
		for _, h := range headers {
			if s, hok := h.(string); hok {
				m.allowedHeaders = append(m.allowedHeaders, s)
			}
		}
	}

	if val, ok := cfg["allow_credentials"].(bool); ok {
		m.allowCredentials = val
	}

	if val, ok := cfg["max_age"].(int); ok {
		m.maxAge = val
	}

	if m.allowAll && m.allowCredentials {
		return errors.New("cors: allow_credentials cannot be used with wildcard origin")
	}

	return nil
}

func (m *Middleware) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")

		if origin == "" {
			next.ServeHTTP(w, r)
			return
		}

		if !m.isOriginAllowed(origin) {
			w.WriteHeader(http.StatusForbidden)
			return
		}

		m.setHeaders(w, origin)

		if m.isPreflight(r) {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (m *Middleware) resolveAllowedOrigins(origins []interface{}) {
	for _, o := range origins {
		s, ok := o.(string)
		if !ok {
			continue
		}

		if s == "*" {
			m.allowAll = true
		}

		m.allowedOrigins[s] = struct{}{}
	}
}

func (m *Middleware) isOriginAllowed(origin string) bool {
	if m.allowAll {
		return true
	}

	_, ok := m.allowedOrigins[origin]
	return ok
}

func (m *Middleware) setHeaders(w http.ResponseWriter, origin string) {
	h := w.Header()

	if m.allowAll {
		h.Set("Access-Control-Allow-Origin", "*")
	} else {
		h.Set("Access-Control-Allow-Origin", origin)
		h.Set("Vary", "Origin")
	}

	if len(m.allowedMethods) > 0 {
		h.Set("Access-Control-Allow-Methods", strings.Join(m.allowedMethods, ", "))
	}

	if len(m.allowedHeaders) > 0 {
		h.Set("Access-Control-Allow-Headers", strings.Join(m.allowedHeaders, ", "))
	}

	if m.allowCredentials {
		h.Set("Access-Control-Allow-Credentials", "true")
	}

	if m.maxAge > 0 {
		h.Set("Access-Control-Max-Age", strconv.Itoa(m.maxAge))
	}
}

func (m *Middleware) isPreflight(r *http.Request) bool {
	return r.Method == http.MethodOptions &&
		r.Header.Get("Origin") != "" &&
		r.Header.Get("Access-Control-Request-Method") != ""
}
