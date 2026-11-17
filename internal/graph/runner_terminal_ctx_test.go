package graph

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/aangwijaya/talent-scout-server/internal/domain"
	"github.com/aangwijaya/talent-scout-server/internal/llm"
	"github.com/aangwijaya/talent-scout-server/internal/store"
)

// cancelAwareStore fails persistence on a cancelled context, like a real
// Postgres call would. Terminal events must therefore use a detached context.
type cancelAwareStore struct{ *store.Memory }

func (s *cancelAwareStore) AppendNodeEvent(ctx context.Context, event domain.NodeEvent) (domain.NodeEvent, error) {
	if err := ctx.Err(); err != nil {
		return domain.NodeEvent{}, err
	}
	return s.Memory.AppendNodeEvent(ctx, event)
}

func newCancelAwareRunner(t *testing.T, client llm.Client) (*Runner, *store.Memory) {
	t.Helper()
	mem := store.NewMemory()
	st := &cancelAwareStore{Memory: mem}
	runID := domain.NewID()
	return NewRunner(st, NewBus(st), client, testModels(), runID, "sess-ctx"), mem
}

func TestCancelledLLMNodePersistsTerminalFailureWithoutRetryPair(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	client := &stubClient{chat: func(req llm.Request) (llm.Response, error) {
		cancel()
		if req.OnRetry != nil {
			// A cancelled parent must not produce node.failed(will_retry=true)
			// nor a second node.started.
			req.OnRetry(1, errors.New("upstream status 503"))
		}
		return llm.Response{}, llm.ErrUnavailable
	}}
	runner, mem := newCancelAwareRunner(t, client)

	if _, err := runner.RunLLMNode(ctx, "planner", 1, llm.Request{
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "plan"}},
	}, "plan", nil); err == nil {
		t.Fatal("RunLLMNode succeeded, want the cancelled call to fail")
	}

	events, err := mem.ListNodeEvents(context.Background(), runner.RunID(), 0)
	if err != nil {
		t.Fatalf("ListNodeEvents: %v", err)
	}
	if len(events) != 2 || events[0].Type != EventNodeStarted || events[1].Type != EventNodeFailed {
		t.Fatalf("events = %v, want [node.started node.failed]", eventTypes(t, mem, runner.RunID()))
	}
	var failed NodeFailed
	if err := json.Unmarshal(events[1].Payload, &failed); err != nil {
		t.Fatalf("unmarshal node.failed: %v", err)
	}
	if failed.WillRetry {
		t.Error("node.failed will_retry = true after parent context cancellation, want false")
	}
}

func TestCancelledCodeNodePersistsTerminalFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	runner, mem := newCancelAwareRunner(t, &stubClient{})

	err := runner.RunCodeNode(ctx, "guard", 1, "guard input", func() (string, error) {
		cancel()
		return "", errors.New("guard stopped")
	})
	if err == nil {
		t.Fatal("RunCodeNode = nil, want the cancelled step to fail")
	}

	events, listErr := mem.ListNodeEvents(context.Background(), runner.RunID(), 0)
	if listErr != nil {
		t.Fatalf("ListNodeEvents: %v", listErr)
	}
	if len(events) != 2 || events[1].Type != EventNodeFailed {
		t.Fatalf("events = %v, want [node.started node.failed]", eventTypes(t, mem, runner.RunID()))
	}
}

func TestCancelledLLMNodeDoesNotRetryOnPlainStore(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	client := &stubClient{chat: func(req llm.Request) (llm.Response, error) {
		cancel()
		if req.OnRetry != nil {
			req.OnRetry(1, errors.New("upstream status 503"))
		}
		return llm.Response{}, llm.ErrUnavailable
	}}
	// A plain memory store accepts writes on a cancelled ctx, so a retry pair
	// emitted after cancellation would be persisted and visible here.
	mem := store.NewMemory()
	runner := NewRunner(mem, NewBus(mem), client, testModels(), domain.NewID(), "sess-ctx-plain")

	if _, err := runner.RunLLMNode(ctx, "planner", 1, llm.Request{
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "plan"}},
	}, "plan", nil); err == nil {
		t.Fatal("RunLLMNode succeeded, want the cancelled call to fail")
	}

	events, err := mem.ListNodeEvents(context.Background(), runner.RunID(), 0)
	if err != nil {
		t.Fatalf("ListNodeEvents: %v", err)
	}
	if len(events) != 2 || events[0].Type != EventNodeStarted || events[1].Type != EventNodeFailed {
		t.Fatalf("events = %v, want [node.started node.failed]", eventTypes(t, mem, runner.RunID()))
	}
	for i, event := range events {
		if event.Attempt == nil || *event.Attempt != 1 {
			t.Errorf("event %d (%s) attempt = %v, want 1", i, event.Type, event.Attempt)
		}
	}
	var failed NodeFailed
	if err := json.Unmarshal(events[1].Payload, &failed); err != nil {
		t.Fatalf("unmarshal node.failed: %v", err)
	}
	if failed.WillRetry {
		t.Error("node.failed will_retry = true after parent context cancellation, want false")
	}
}
