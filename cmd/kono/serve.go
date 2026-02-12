package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"go.uber.org/zap"

	"github.com/xff16/kono"
	"github.com/xff16/kono/internal/app"
	"github.com/xff16/kono/internal/logger"
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Run HTTP server",
	Args:  cobra.NoArgs,
	RunE: func(_ *cobra.Command, _ []string) error {
		return runServe()
	},
}

func init() {
	rootCmd.AddCommand(serveCmd)
}

func runServe() error {
	if cfgPath == "" {
		cfgPath = os.Getenv("KONO_CONFIG")
	}
	if cfgPath == "" {
		cfgPath = "./kono.json"
	}

	cfg, err := kono.LoadConfig(cfgPath)
	if err != nil {
		return err
	}

	log := logger.New(cfg.Debug)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	server := app.NewServer(cfg, log)

	go func() {
		if err = server.Start(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal("server error", zap.Error(err))
		}
	}()

	log.Info("server started")

	<-ctx.Done()
	log.Info("shutdown signal received")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second) //nolint:mnd // internal timeout
	defer cancel()

	if err = server.Stop(shutdownCtx); err != nil {
		log.Error("graceful shutdown failed", zap.Error(err))
	}

	log.Info("server stopped")

	return nil
}
