package main

import (
	"errors"
	"fmt"
	"net/http"
	"os"

	"github.com/VictoriaMetrics/metrics"
	"go.uber.org/zap"

	"github.com/starwalkn/tokka"
	"github.com/starwalkn/tokka/dashboard"
	"github.com/starwalkn/tokka/internal/logger"
)

func main() {
	cfgPath := os.Getenv("TOKKA_CONFIG")
	if cfgPath == "" {
		cfgPath = "./tokka.json"
	}

	cfg := tokka.LoadConfig(cfgPath)
	log := logger.New(cfg.Debug)

	if cfg.Dashboard.Enable {
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

	server := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.Server.Port),
		Handler:      mux,
		ReadTimeout:  cfg.Server.Timeout,
		WriteTimeout: cfg.Server.Timeout,
	}

	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal("server error", zap.Error(err))
	}

	log.Info("server is closed")

	if err := log.Sync(); err != nil {
		log.Warn("cannot sync log", zap.Error(err))
	}
}
