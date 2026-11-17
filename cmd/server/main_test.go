package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/aangwijaya/talent-scout-server/internal/domain"
	"github.com/aangwijaya/talent-scout-server/internal/session"
	"github.com/aangwijaya/talent-scout-server/internal/store"
)

type reconcileFailingStore struct{ store.Store }

func (reconcileFailingStore) ListGraphRunsByStatus(context.Context, domain.GraphRunStatus) ([]domain.GraphRun, error) {
	return nil, errors.New("listing runs failed")
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestBootStopsBeforeSweeperAndListenerWhenReconcileFails(t *testing.T) {
	sweeperStarted, listenerStarted := false, false
	err := boot(context.Background(), reconcileFailingStore{store.NewMemory()}, session.NewRegistry(), time.Minute, testLogger(),
		func(context.Context, *session.Sweeper) { sweeperStarted = true },
		func() { listenerStarted = true },
	)
	if err == nil {
		t.Fatal("boot error = nil, want the reconciliation failure")
	}
	if sweeperStarted {
		t.Error("sweeper started after a failed reconciliation, want boot to abort first")
	}
	if listenerStarted {
		t.Error("listener started after a failed reconciliation, want boot to abort first")
	}
}

func TestBootStartsSweeperAndListenerAfterReconcile(t *testing.T) {
	sweeperStarted, listenerStarted := false, false
	err := boot(context.Background(), store.NewMemory(), session.NewRegistry(), time.Minute, testLogger(),
		func(context.Context, *session.Sweeper) { sweeperStarted = true },
		func() { listenerStarted = true },
	)
	if err != nil {
		t.Fatalf("boot: %v", err)
	}
	if !sweeperStarted {
		t.Error("sweeper did not start after a successful reconciliation")
	}
	if !listenerStarted {
		t.Error("listener did not start after a successful reconciliation")
	}
}
