package httpapi

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/aangwijaya/talent-scout-server/internal/config"
	"github.com/aangwijaya/talent-scout-server/internal/http/handlers"
	"github.com/aangwijaya/talent-scout-server/internal/http/middleware"
)

func NewRouter(cfg config.Config, db handlers.DBPinger) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestLogger(slog.Default()))
	r.Use(middleware.CORS(cfg.FrontendOrigins))

	r.Route("/api/v1", func(r chi.Router) {
		r.Method(http.MethodGet, "/health", handlers.NewHealth(cfg, db))
	})

	return r
}
