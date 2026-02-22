package app

import (
	"context"
	"fmt"
	"net/http"

	"github.com/VictoriaMetrics/metrics"
	"go.uber.org/zap"

	"github.com/starwalkn/kono"
)

type Server struct {
	http *http.Server
	log  *zap.Logger
}

func NewServer(cfg kono.Config, log *zap.Logger) *Server {
	routerConfigSet := kono.RouterConfigSet{
		Version:           cfg.Version,
		Router:            cfg.Router,
		GlobalMiddlewares: cfg.GlobalMiddlewares,
		Metrics:           cfg.Server.Metrics,
	}

	mainRouter := kono.NewRouter(routerConfigSet, log.Named("router"))

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
