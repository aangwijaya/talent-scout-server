package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/aangwijaya/talent-scout-server/internal/domain"
	"github.com/aangwijaya/talent-scout-server/internal/graph"
	"github.com/aangwijaya/talent-scout-server/internal/store"
)

// gatedEventStore pauses one matching node event until release is closed. It
// models the runner's turn tail after message.completed is already published.
type gatedEventStore struct {
	store.Store
	eventType  string
	nodeID     string
	occurrence int

	mu      sync.Mutex
	seen    int
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func newGatedEventStore(base store.Store, eventType, nodeID string, occurrence int) *gatedEventStore {
	return &gatedEventStore{
		Store:      base,
		eventType:  eventType,
		nodeID:     nodeID,
		occurrence: occurrence,
		entered:    make(chan struct{}),
		release:    make(chan struct{}),
	}
}

func (s *gatedEventStore) AppendNodeEvent(ctx context.Context, event domain.NodeEvent) (domain.NodeEvent, error) {
	if event.Type == s.eventType && event.NodeID != nil && *event.NodeID == s.nodeID {
		s.mu.Lock()
		s.seen++
		block := s.seen == s.occurrence
		s.mu.Unlock()
		if block {
			s.once.Do(func() { close(s.entered) })
			<-s.release
		}
	}
	return s.Store.AppendNodeEvent(ctx, event)
}

func waitAssistantCompleted(t *testing.T, events <-chan domain.NodeEvent) domain.Message {
	t.Helper()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for {
		select {
		case event := <-events:
			if event.Type != graph.EventMessageCompleted {
				continue
			}
			var payload graph.MessageCompleted
			if err := json.Unmarshal(event.Payload, &payload); err != nil {
				t.Fatalf("unmarshal message.completed: %v", err)
			}
			if payload.Message.Role == domain.MessageRoleAssistant {
				return payload.Message
			}
		case <-timer.C:
			t.Fatal("timed out waiting for an assistant message.completed")
		}
	}
}

func waitGate(t *testing.T, entered <-chan struct{}) {
	t.Helper()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the run to reach the gated node event")
	}
}

// sendGatedAnswer posts an answer while the previous turn is paused after
// message.completed. The correct server waits for that turn's tail and then
// accepts (202); the buggy one rejects immediately (409).
func sendGatedAnswer(t *testing.T, f *chatFixture, gate *gatedEventStore, content string) int {
	t.Helper()
	payload, _ := json.Marshal(map[string]string{"content": content})
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		f.handler.SendMessage(rec, chatRequest(http.MethodPost, f.raw, string(payload)))
		close(done)
	}()

	select {
	case <-done:
		// Rejected before the tail finished: reproduce the bug, then let the
		// run complete so the test can end cleanly.
		close(gate.release)
	case <-time.After(100 * time.Millisecond):
		// Accepted path: the handler is waiting for the in-flight turn.
		close(gate.release)
		<-done
	}
	return rec.Code
}

func TestSendMessageAcceptedImmediatelyAfterAssistantCompleted(t *testing.T) {
	t.Run("opening turn after start", func(t *testing.T) {
		var gate *gatedEventStore
		f := newChatFixtureWrapped(t, false, func(base store.Store) store.Store {
			gate = newGatedEventStore(base, graph.EventNodeCompleted, "interviewer", 1)
			return gate
		})
		events, cancel := f.bus.SubscribeSession(f.session.ID)
		defer cancel()

		rec := httptest.NewRecorder()
		f.handler.Start(rec, chatRequest(http.MethodPost, f.raw, ""))
		if rec.Code != http.StatusOK {
			t.Fatalf("start status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
		}

		waitAssistantCompleted(t, events)
		waitGate(t, gate.entered)

		code := sendGatedAnswer(t, f, gate, "I optimized rendering.")
		waitManager(t, f.manager, f.session.ID)
		if code != http.StatusAccepted {
			t.Fatalf("POST /messages right after message.completed = %d, want 202", code)
		}
	})

	t.Run("later turn", func(t *testing.T) {
		var gate *gatedEventStore
		f := newChatFixtureWrapped(t, false, func(base store.Store) store.Store {
			gate = newGatedEventStore(base, graph.EventNodeCompleted, "interviewer", 2)
			return gate
		})

		rec := httptest.NewRecorder()
		f.handler.Start(rec, chatRequest(http.MethodPost, f.raw, ""))
		if rec.Code != http.StatusOK {
			t.Fatalf("start status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
		}
		waitManager(t, f.manager, f.session.ID)

		events, cancel := f.bus.SubscribeSession(f.session.ID)
		defer cancel()

		payload, _ := json.Marshal(map[string]string{"content": "I optimized rendering."})
		rec = httptest.NewRecorder()
		f.handler.SendMessage(rec, chatRequest(http.MethodPost, f.raw, string(payload)))
		if rec.Code != http.StatusAccepted {
			t.Fatalf("first answer status = %d, want 202 (body: %s)", rec.Code, rec.Body.String())
		}

		waitAssistantCompleted(t, events)
		waitGate(t, gate.entered)

		code := sendGatedAnswer(t, f, gate, "I split the state into a store.")
		waitManager(t, f.manager, f.session.ID)
		if code != http.StatusAccepted {
			t.Fatalf("POST /messages right after message.completed = %d, want 202", code)
		}
	})
}
