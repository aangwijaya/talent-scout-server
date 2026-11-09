package httpapi

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/aangwijaya/talent-scout-server/internal/config"
	"github.com/aangwijaya/talent-scout-server/internal/cv"
	"github.com/aangwijaya/talent-scout-server/internal/email"
	"github.com/aangwijaya/talent-scout-server/internal/graph"
	"github.com/aangwijaya/talent-scout-server/internal/http/handlers"
	"github.com/aangwijaya/talent-scout-server/internal/http/middleware"
	"github.com/aangwijaya/talent-scout-server/internal/llm"
	"github.com/aangwijaya/talent-scout-server/internal/session"
	"github.com/aangwijaya/talent-scout-server/internal/storage"
	"github.com/aangwijaya/talent-scout-server/internal/store"
)

// Deps bundles the dependencies a router needs beyond config.
type Deps struct {
	DB        handlers.DBPinger
	Store     store.Store
	Storage   storage.Storage
	Extractor cv.Extractor
	Mailer    email.Mailer
	LLM       llm.Client
	Models    config.ModelsConfig
	Registry  *session.Registry
}

func NewRouter(cfg config.Config, deps Deps) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestLogger(slog.Default()))
	r.Use(middleware.CORS(cfg.FrontendOrigins))

	var bus *graph.Bus
	var manager *graph.SessionManager
	if deps.Store != nil && deps.LLM != nil {
		bus = graph.NewBus(deps.Store)
		manager = graph.NewSessionManager(cfg, deps.Store, bus, deps.LLM, deps.Models, deps.Registry, slog.Default())
	}

	r.Route("/api/v1", func(r chi.Router) {
		r.Method(http.MethodGet, "/health", handlers.NewHealth(cfg, deps.DB))

		apply := handlers.NewApply(cfg, deps.Store, deps.Storage, deps.Extractor, deps.Mailer)
		limiter := middleware.NewRateLimiter(middleware.ApplyRateLimit, middleware.ApplyRateLimitWindow)
		r.With(middleware.RateLimit(limiter, cfg.BFFSharedSecret)).Method(http.MethodPost, "/jobs/{jobId}/applications", apply)

		sessions := handlers.NewSessions(cfg, deps.Store)
		sessions.SetChat(manager, bus)
		r.Method(http.MethodGet, "/sessions/{token}", http.HandlerFunc(sessions.Get))
		r.Method(http.MethodPost, "/sessions/{token}/start", http.HandlerFunc(sessions.Start))
		r.Method(http.MethodGet, "/sessions/{token}/messages", http.HandlerFunc(sessions.Messages))
		r.Method(http.MethodPost, "/sessions/{token}/messages", http.HandlerFunc(sessions.SendMessage))
		r.Method(http.MethodPost, "/sessions/{token}/retry", http.HandlerFunc(sessions.Retry))
		r.Method(http.MethodGet, "/sessions/{token}/events", http.HandlerFunc(sessions.Events))
	})

	return r
}
