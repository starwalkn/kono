package dashboard

import (
	"encoding/json"
	"fmt"
	"net/http"
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

	mux.HandleFunc("/config", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)

		cfgBytes, err := json.Marshal(s.cfg)
		if err != nil {
			s.log.Error("cannot marshal config", zap.Error(err))
			http.Error(w, "cannot marshal config", http.StatusInternalServerError)
		}

		w.Write(cfgBytes)
	})

	addr := fmt.Sprintf(":%d", s.cfg.Dashboard.Port)

	server := http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  time.Duration(s.cfg.Dashboard.Timeout) * time.Second,
		WriteTimeout: time.Duration(s.cfg.Dashboard.Timeout) * time.Second,
	}

	s.log.Info("📊 Dashboard server started\n", zap.String("addr", addr))

	if err := server.ListenAndServe(); err != nil {
		s.log.Error("dashboard server had errors, disabling", zap.Error(err))
		return
	}
}
