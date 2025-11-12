package dashboard

import (
	"fmt"
	"net/http"
	"time"

	"go.uber.org/zap"

	"github.com/starwalkn/tokka"
	"github.com/starwalkn/tokka/internal/logger"
)

type Server struct {
	cfg *tokka.GatewayConfig
	log *zap.Logger
}

func NewServer(cfg *tokka.GatewayConfig) *Server {
	return &Server{
		cfg: cfg,
		log: logger.New(true).Named("dashboard"),
	}
}

func (s *Server) Start() {
	mux := http.NewServeMux()

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
