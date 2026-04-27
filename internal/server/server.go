package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap"

	"github.com/starwalkn/kono"
	"github.com/starwalkn/kono/internal/otelcommon"
)

type Server struct {
	http      *http.Server
	router    *kono.Router
	providers []otelcommon.Provider
	log       *zap.Logger
}

func New(ctx context.Context, cfg kono.GatewayConfig, version string, log *zap.Logger) (*Server, error) {
	bundle, err := bootstrapRouter(ctx, cfg, version, log)
	if err != nil {
		return nil, fmt.Errorf("bootstrap router: %w", err)
	}

	handler := buildHandler(bundle)

	return &Server{
		http: &http.Server{
			Addr:         fmt.Sprintf(":%d", cfg.Server.Port),
			Handler:      handler,
			ReadTimeout:  cfg.Server.Timeout,
			WriteTimeout: cfg.Server.Timeout,
		},
		router:    bundle.Router,
		providers: []otelcommon.Provider{bundle.MeterProvider, bundle.TracerProvider},
		log:       log,
	}, nil
}

func (s *Server) Start() error {
	return s.http.ListenAndServe()
}

// Stop drains the HTTP server, closes the router (middleware Closers), then
// flushes observability providers. Order matters: HTTP first so no in-flight
// request writes to a provider that is already shutting down.
func (s *Server) Stop(ctx context.Context) error {
	var errs []error

	if err := s.http.Shutdown(ctx); err != nil {
		errs = append(errs, fmt.Errorf("http shutdown: %w", err))
	}

	if err := s.router.Close(); err != nil {
		errs = append(errs, fmt.Errorf("router close: %w", err))
	}

	for _, p := range s.providers {
		if err := p.Shutdown(ctx); err != nil {
			errs = append(errs, fmt.Errorf("provider shutdown: %w", err))
		}
	}

	return errors.Join(errs...)
}

func bootstrapRouter(ctx context.Context, cfg kono.GatewayConfig, version string, log *zap.Logger) (kono.RouterBundle, error) {
	bundle, err := kono.NewRouter(ctx, kono.RoutingConfigSet{
		Routing:        cfg.Routing,
		Service:        cfg.Service,
		ServiceVersion: version,
		Metrics:        cfg.Server.Metrics,
		Tracing:        cfg.Server.Tracing,
	}, log.Named("router"))
	if err != nil {
		return kono.RouterBundle{}, err
	}

	return bundle, nil
}

func buildHandler(bundle kono.RouterBundle) http.Handler {
	mux := http.NewServeMux()

	mux.Handle("GET /__health", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	}))

	if bundle.PromRegistry != nil {
		mux.Handle("/metrics", promhttp.HandlerFor(bundle.PromRegistry, promhttp.HandlerOpts{}))
	}

	mux.Handle("/", bundle.Router)

	return mux
}
