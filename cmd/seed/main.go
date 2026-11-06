package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/aangwijaya/talent-scout-server/internal/store"
)

func main() {
	configPath := flag.String("config", "config/seed-job.yaml", "path to the seed job YAML")
	flag.Parse()

	job, err := loadJob(*configPath)
	if err != nil {
		fail(err)
	}
	if err := validateJob(job); err != nil {
		fail(fmt.Errorf("invalid seed job: %w", err))
	}

	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		fail(errors.New("DATABASE_URL is required"))
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	openCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	pool, err := store.OpenPool(openCtx, databaseURL)
	cancel()
	if err != nil {
		fail(err)
	}
	defer pool.Close()

	s := store.NewPostgres(pool)

	existing, err := s.GetJob(ctx, job.ID)
	switch {
	case err == nil:
		fmt.Printf("job %s already exists (%s)\n", existing.ID, existing.Title)
		return
	case !errors.Is(err, store.ErrNotFound):
		fail(err)
	}

	if err := s.CreateJob(ctx, job); err != nil {
		if errors.Is(err, store.ErrConflict) {
			fmt.Printf("job %s already exists\n", job.ID)
			return
		}
		fail(err)
	}

	fmt.Printf("seeded job %s (%s)\n", job.ID, job.Title)
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
