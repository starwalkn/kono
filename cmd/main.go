package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/starwalkn/tokka"
	"github.com/starwalkn/tokka/dashboard"

	_ "github.com/starwalkn/tokka/internal/plugin/ratelimit"
)

func main() {
	cfgPath := os.Getenv("TOKKA_CONFIG")
	if cfgPath == "" {
		cfgPath = "./tokka.json"
	}

	cfg := tokka.LoadConfig(cfgPath)

	if cfg.Dashboard.Enable {
		adminServer := dashboard.NewServer(&cfg)
		go adminServer.Start()
	}

	server := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.Server.Port),
		Handler:      tokka.NewRouter(cfg.Routes),
		ReadTimeout:  time.Duration(cfg.Server.Timeout) * time.Second,
		WriteTimeout: time.Duration(cfg.Server.Timeout) * time.Second,
	}

	if err := server.ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}
