package main

import (
	"compress/flate"
	"compress/gzip"
	"io"
	"net/http"
	"strings"

	"go.uber.org/zap"

	"github.com/starwalkn/tokka"
	"github.com/starwalkn/tokka/internal/logger"
)

type Middleware struct {
	enabled bool
	alg     string
	log     *zap.Logger
}

func NewMiddleware() tokka.Middleware {
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

		if alg == "gzip" || alg == "deflate" {
			m.alg = alg
		}
	} else {
		m.alg = "gzip"
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
		case "gzip":
			writer = gzip.NewWriter(w)
		case "deflate":
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
