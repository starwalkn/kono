package server

import (
	"context"
	"fmt"
	"net/http"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.uber.org/zap"

	"github.com/starwalkn/kono"
)

type Server struct {
	http   *http.Server
	router *kono.Router
	log    *zap.Logger
}

func New(cfg kono.GatewayConfig, log *zap.Logger) (*Server, *sdkmetric.MeterProvider, error) {
	routingConfigSet := kono.RoutingConfigSet{
		Routing: cfg.Routing,
		Metrics: cfg.Server.Metrics,
	}

	mainRouter, meterProvider, promReg, err := kono.NewRouter(routingConfigSet, log.Named("router"))
	if err != nil {
		return nil, nil, err
	}

	mux := http.NewServeMux()

	mux.Handle("GET /__health", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	}))

	// If metrics exporter is OTLP, promReg will be nil
	if cfg.Server.Metrics.Enabled && promReg != nil {
		mux.Handle("/metrics", promhttp.HandlerFor(promReg, promhttp.HandlerOpts{
			EnableOpenMetrics: true,
		}))
	}

	mux.Handle("/", mainRouter)

	return &Server{
		http: &http.Server{
			Addr:         fmt.Sprintf(":%d", cfg.Server.Port),
			Handler:      mux,
			ReadTimeout:  cfg.Server.Timeout,
			WriteTimeout: cfg.Server.Timeout,
		},
		router: mainRouter,
		log:    log,
	}, meterProvider, nil
}

func (s *Server) Start() error {
	return s.http.ListenAndServe()
}

func (s *Server) Stop(ctx context.Context) error {
	_ = s.router.Close()

	return s.http.Shutdown(ctx)
}
