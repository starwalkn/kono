package server

import (
	"context"
	"fmt"
	"net/http"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap"

	"github.com/starwalkn/kono"
)

type Server struct {
	http *http.Server
	log  *zap.Logger
}

func New(cfg kono.GatewayConfig, log *zap.Logger) *Server {
	routingConfigSet := kono.RoutingConfigSet{
		Routing: cfg.Routing,
		Metrics: cfg.Server.Metrics,
	}

	mainRouter, promReg := kono.NewRouter(routingConfigSet, log.Named("router"))

	mux := http.NewServeMux()

	mux.Handle("GET /__health", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK")) //nolint:errcheck,gosec // not important
	}))

	if cfg.Server.Metrics.Enabled && promReg != nil {
		mux.Handle("/metrics", promhttp.HandlerFor(promReg, promhttp.HandlerOpts{
			EnableOpenMetrics: true,
		}))
	}

	mux.Handle("/", mainRouter)

	return &Server{
		log: log,
		http: &http.Server{
			Addr:         fmt.Sprintf(":%d", cfg.Server.Port),
			Handler:      mux,
			ReadTimeout:  cfg.Server.Timeout,
			WriteTimeout: cfg.Server.Timeout,
		},
	}
}

func (s *Server) Start() error {
	return s.http.ListenAndServe()
}

func (s *Server) Stop(ctx context.Context) error {
	return s.http.Shutdown(ctx)
}
