package main

import (
	"compress/flate"
	"compress/gzip"
	"io"
	"net/http"
	"strings"

	"go.uber.org/zap"

	"github.com/starwalkn/kono"
	"github.com/starwalkn/kono/internal/logger"
)

const (
	algGzip    = "gzip"
	algDeflate = "deflate"
)

type Middleware struct {
	enabled bool
	alg     string
	log     *zap.Logger
}

func NewMiddleware() kono.Middleware {
	return &Middleware{}
}

func (m *Middleware) Name() string {
	return "compressor"
}

func (m *Middleware) Init(cfg map[string]interface{}) error {
	if val, ok := cfg["enabled"].(bool); ok {
		m.enabled = val
	}

	if alg, ok := cfg["alg"].(string); ok {
		alg = strings.ToLower(alg)

		if alg == algGzip || alg == algDeflate {
			m.alg = alg
		}
	} else {
		m.alg = algGzip
	}

	m.log = logger.New(false)

	return nil
}

func (m *Middleware) Handler(next http.Handler) http.Handler {
	if !m.enabled {
		return next
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("Accept-Encoding"), m.alg) {
			next.ServeHTTP(w, r)
			return
		}

		w.Header().Set("Content-Encoding", m.alg)

		var writer io.WriteCloser
		var err error

		switch m.alg {
		case algGzip:
			writer = gzip.NewWriter(w)
		case algDeflate:
			writer, err = flate.NewWriter(w, flate.DefaultCompression)
			if err != nil {
				m.log.Error("cannot create deflate writer", zap.Error(err))
				next.ServeHTTP(w, r)

				return
			}
		}

		defer func() {
			if err = writer.Close(); err != nil {
				m.log.Warn("cannot close compression writer", zap.Error(err))
			}
		}()

		cw := &compressorResponseWriter{
			ResponseWriter: w,
			Writer:         writer,
		}

		next.ServeHTTP(cw, r)
	})
}

type compressorResponseWriter struct {
	http.ResponseWriter
	Writer io.Writer
}

func (w *compressorResponseWriter) Write(b []byte) (int, error) {
	return w.Writer.Write(b)
}
