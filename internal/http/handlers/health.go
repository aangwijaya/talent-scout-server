package handlers

import (
	"context"
	"net/http"
	"time"

	"github.com/aangwijaya/talent-scout-server/internal/config"
	"github.com/aangwijaya/talent-scout-server/internal/http/apierror"
)

type DBPinger interface {
	Ping(ctx context.Context) error
}

type Health struct {
	cfg config.Config
	db  DBPinger
}

type healthResponse struct {
	Status    string `json:"status"`
	Version   string `json:"version"`
	LLMMode   string `json:"llm_mode"`
	EmailMode string `json:"email_mode"`
	DB        string `json:"db,omitempty"`
}

func NewHealth(cfg config.Config, db DBPinger) *Health {
	return &Health{cfg: cfg, db: db}
}

func (h *Health) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	resp := healthResponse{
		Status:    "ok",
		Version:   h.cfg.Version,
		LLMMode:   h.cfg.LLMMode,
		EmailMode: h.cfg.EmailMode,
	}

	if r.URL.Query().Get("deep") == "1" {
		if h.db == nil {
			apierror.Write(w, http.StatusServiceUnavailable, apierror.CodeDBUnavailable, "database is unavailable", nil)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := h.db.Ping(ctx); err != nil {
			apierror.Write(w, http.StatusServiceUnavailable, apierror.CodeDBUnavailable, "database is unavailable", nil)
			return
		}
		resp.DB = "ok"
	}

	apierror.JSON(w, http.StatusOK, resp)
}
