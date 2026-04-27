package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/pprof"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"go.uber.org/zap"

	"github.com/starwalkn/kono"
	"github.com/starwalkn/kono/internal/logger"
	"github.com/starwalkn/kono/internal/server"
)

const (
	shutdownTimeout  = 10 * time.Second
	bootstrapTimeout = 30 * time.Second

	pprofReadTimeout  = 10 * time.Second
	pprofWriteTimeout = 30 * time.Second
	pprofIdleTimeout  = 60 * time.Second
)

// version is populated via -ldflags "-X main.version=…" at build time.
// Empty when built without ldflags; tracing then omits service.version.
var version string

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
		cfgPath = fallbackConfigPath
	}

	cfg, err := kono.LoadConfig(cfgPath)
	if err != nil {
		return err
	}

	log := logger.New(cfg.Debug)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	bootstrapCtx, cancelBootstrap := context.WithTimeout(ctx, bootstrapTimeout)

	srv, err := server.New(bootstrapCtx, cfg.Gateway, version, log)
	cancelBootstrap()
	if err != nil {
		return fmt.Errorf("server init: %w", err)
	}

	serverErrCh := make(chan error, 1)
	go func() {
		if err = srv.Start(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErrCh <- err
			stop()

			return
		}

		serverErrCh <- nil
	}()

	log.Info("server started")

	stopPprof := startPprofServer(cfg.Gateway.Server.Pprof, log)

	<-ctx.Done()
	log.Info("shutdown signal received")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	if err = srv.Stop(shutdownCtx); err != nil {
		log.Error("graceful shutdown failed", zap.Error(err))
	}

	stopPprof(shutdownCtx)

	// Drain Start's exit so we don't lose a listener error on the floor
	if err = <-serverErrCh; err != nil {
		log.Error("server error", zap.Error(err))
	}

	log.Info("server stopped")

	return nil
}

func startPprofServer(cfg kono.PprofConfig, log *zap.Logger) func(ctx context.Context) {
	if !cfg.Enabled {
		return func(_ context.Context) {}
	}

	srv := buildPprofServer(cfg.Port)

	go func() {
		log.Info("pprof listener started", zap.Int("port", cfg.Port))

		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("pprof server error", zap.Error(err))
		}
	}()

	return func(ctx context.Context) {
		if err := srv.Shutdown(ctx); err != nil {
			log.Error("pprof server shutdown error", zap.Error(err))
		}
	}
}

func buildPprofServer(port int) *http.Server {
	pprofMux := http.NewServeMux()
	pprofMux.HandleFunc("/debug/pprof/", pprof.Index)
	pprofMux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	pprofMux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	pprofMux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	pprofMux.HandleFunc("/debug/pprof/trace", pprof.Trace)

	return &http.Server{
		Addr:         fmt.Sprintf("localhost:%d", port),
		Handler:      pprofMux,
		ReadTimeout:  pprofReadTimeout,
		WriteTimeout: pprofWriteTimeout,
		IdleTimeout:  pprofIdleTimeout,
	}
}
