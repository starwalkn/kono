package app

import (
	"context"
	"fmt"
	"net/http"

	"github.com/VictoriaMetrics/metrics"
	"go.uber.org/zap"

	"github.com/starwalkn/tokka"
	"github.com/starwalkn/tokka/dashboard"
)

type Server struct {
	http *http.Server
	log  *zap.Logger
}

func NewServer(cfg tokka.Config, log *zap.Logger) *Server {
	if cfg.Dashboard.Enabled {
		dashboardServer := dashboard.NewServer(&cfg, log.Named("dashboard"))
		go dashboardServer.Start()
	}

	routerConfigSet := tokka.RouterConfigSet{
		Version:     cfg.Version,
		Routes:      cfg.Routes,
		Middlewares: cfg.Middlewares,
		Features:    cfg.Features,
		Metrics:     cfg.Server.Metrics,
	}

	mainRouter := tokka.NewRouter(routerConfigSet, log.Named("router"))

	mux := http.NewServeMux()

	if cfg.Server.Metrics.Enabled {
		mux.Handle("/metrics", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			metrics.WritePrometheus(w, true)
			// promhttp.Handler()
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
