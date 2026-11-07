package httpapi

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/aangwijaya/talent-scout-server/internal/config"
	"github.com/aangwijaya/talent-scout-server/internal/cv"
	"github.com/aangwijaya/talent-scout-server/internal/http/handlers"
	"github.com/aangwijaya/talent-scout-server/internal/http/middleware"
	"github.com/aangwijaya/talent-scout-server/internal/storage"
	"github.com/aangwijaya/talent-scout-server/internal/store"
)

// Deps bundles the dependencies a router needs beyond config.
type Deps struct {
	DB        handlers.DBPinger
	Store     store.Store
	Storage   storage.Storage
	Extractor cv.Extractor
}

func NewRouter(cfg config.Config, deps Deps) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestLogger(slog.Default()))
	r.Use(middleware.CORS(cfg.FrontendOrigins))

	r.Route("/api/v1", func(r chi.Router) {
		r.Method(http.MethodGet, "/health", handlers.NewHealth(cfg, deps.DB))

		apply := handlers.NewApply(cfg, deps.Store, deps.Storage, deps.Extractor)
		limiter := middleware.NewRateLimiter(middleware.ApplyRateLimit, middleware.ApplyRateLimitWindow)
		r.With(middleware.RateLimit(limiter)).Method(http.MethodPost, "/jobs/{jobId}/applications", apply)
	})

	return r
}
