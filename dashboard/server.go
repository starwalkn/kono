package dashboard

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"time"

	"go.uber.org/zap"

	"github.com/starwalkn/tokka"
)

type Server struct {
	cfg *tokka.GatewayConfig
	log *zap.Logger
}

func NewServer(cfg *tokka.GatewayConfig, log *zap.Logger) *Server {
	return &Server{
		cfg: cfg,
		log: log,
	}
}

func (s *Server) Start() {
	mux := http.NewServeMux()

	staticDir := filepath.Join("/", "dashboard", "static")
	mux.Handle("/", http.FileServer(http.Dir(staticDir)))

	mux.HandleFunc("/config", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)

		//nolint:errcheck,gosec // its ok
		json.NewEncoder(w).Encode(s.cfg)
	})

	addr := fmt.Sprintf(":%d", s.cfg.Dashboard.Port)

	server := http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  time.Duration(s.cfg.Dashboard.Timeout) * time.Second,
		WriteTimeout: time.Duration(s.cfg.Dashboard.Timeout) * time.Second,
	}

	s.log.Info("dashboard server started", zap.String("addr", addr))

	if err := server.ListenAndServe(); err != nil {
		s.log.Error("dashboard server had errors, processed shutdown", zap.Error(err))
		return
	}
}
