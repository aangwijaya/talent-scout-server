package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/aangwijaya/talent-scout-server/internal/config"
	"github.com/aangwijaya/talent-scout-server/internal/cv"
	httpapi "github.com/aangwijaya/talent-scout-server/internal/http"
	"github.com/aangwijaya/talent-scout-server/internal/http/handlers"
	"github.com/aangwijaya/talent-scout-server/internal/storage"
	"github.com/aangwijaya/talent-scout-server/internal/store"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	cfg, err := config.Load()
	if err != nil {
		logger.Error("load config", "error", err)
		os.Exit(1)
	}

	var db handlers.DBPinger
	var st store.Store
	if cfg.DatabaseURL != "" {
		openCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		pool, err := store.OpenPool(openCtx, cfg.DatabaseURL)
		cancel()
		if err != nil {
			logger.Error("open database", "error", err)
			os.Exit(1)
		}
		defer pool.Close()
		db = pool
		st = store.NewPostgres(pool)
	}

	server := &http.Server{
		Addr: ":" + cfg.Port,
		Handler: httpapi.NewRouter(cfg, httpapi.Deps{
			DB:        db,
			Store:     st,
			Storage:   storage.NewSupabase(cfg.SupabaseURL, cfg.SupabaseStorageBucket, cfg.SupabaseServiceRoleKey),
			Extractor: cv.NewPDF(),
		}),
		ReadHeaderTimeout: 5 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		logger.Info("server listening", "addr", server.Addr, "env", cfg.AppEnv)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		logger.Error("server stopped", "error", err)
		os.Exit(1)
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error("graceful shutdown", "error", err)
		os.Exit(1)
	}
}
