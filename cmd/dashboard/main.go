package main

import (
	"os"

	"go.uber.org/zap"

	"github.com/starwalkn/tokka"
	"github.com/starwalkn/tokka/dashboard"
	"github.com/starwalkn/tokka/internal/logger"
	_ "github.com/starwalkn/tokka/internal/plugin/ratelimit"
)

func main() {
	cfgPath := os.Getenv("TOKKA_CONFIG")
	if cfgPath == "" {
		cfgPath = "./tokka.json"
	}

	cfg := tokka.LoadConfig(cfgPath)
	log := logger.New(cfg.Debug)

	dashboardServer := dashboard.NewServer(&cfg, log.Named("dashboard"))
	dashboardServer.Start()

	if err := log.Sync(); err != nil {
		log.Warn("cannot sync log", zap.Error(err))
	}
}
