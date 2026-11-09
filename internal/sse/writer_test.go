package sse

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aangwijaya/talent-scout-server/internal/domain"
	"github.com/aangwijaya/talent-scout-server/internal/graph"
)

func TestWriteEventIncludesIDForPersistedEvents(t *testing.T) {
	rec := httptest.NewRecorder()
	w := NewWriter(rec, time.Second)
	if err := w.Prepare(); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if err := w.WriteEvent(domain.NodeEvent{
		RunID: "run-1", Seq: 7, Type: graph.EventMessageCompleted,
		Payload: []byte(`{"message":"x"}`),
	}); err != nil {
		t.Fatalf("WriteEvent: %v", err)
	}
	body := rec.Body.String()
	for _, want := range []string{"id: 7\n", "event: message.completed\n", `data: {"run_id":"run-1","seq":7`} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q:\n%s", want, body)
		}
	}
}

func TestWriteEventOmitsIDForDelta(t *testing.T) {
	rec := httptest.NewRecorder()
	w := NewWriter(rec, time.Second)
	if err := w.Prepare(); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if err := w.WriteEvent(domain.NodeEvent{
		RunID: "run-1", Type: graph.EventMessageDelta,
		Payload: []byte(`{"delta":"hi"}`),
	}); err != nil {
		t.Fatalf("WriteEvent: %v", err)
	}
	if strings.Contains(rec.Body.String(), "id: ") {
		t.Errorf("delta frame must not set Last-Event-ID:\n%s", rec.Body.String())
	}
}

func TestSessionEventFilter(t *testing.T) {
	tests := map[string]bool{
		graph.EventSessionUpdated:   true,
		graph.EventSessionEnded:     true,
		graph.EventMessageCompleted: true,
		graph.EventError:            true,
		graph.EventMessageDelta:     false,
		graph.EventNodeStarted:      false,
		graph.EventRunCompleted:     false,
		graph.EventEdgeTraversed:    false,
	}
	for eventType, want := range tests {
		if got := IsSessionEvent(eventType); got != want {
			t.Errorf("IsSessionEvent(%q) = %v, want %v", eventType, got, want)
		}
	}
}

// deadlineWriter blocks on Write until the deadline set via ResponseController
// fires, mimicking a stalled client on a real net/http connection.
type deadlineWriter struct {
	header http.Header
	once   sync.Once
	done   chan struct{}
}

func newDeadlineWriter() *deadlineWriter {
	return &deadlineWriter{header: make(http.Header), done: make(chan struct{})}
}

func (w *deadlineWriter) Header() http.Header { return w.header }
func (w *deadlineWriter) WriteHeader(int)     {}
func (w *deadlineWriter) Flush()              {}
func (w *deadlineWriter) Write(p []byte) (int, error) {
	<-w.done
	return 0, errors.New("write timeout")
}
func (w *deadlineWriter) SetWriteDeadline(t time.Time) error {
	if !t.IsZero() {
		w.once.Do(func() {
			go func() {
				time.Sleep(time.Until(t))
				close(w.done)
			}()
		})
	}
	return nil
}

func TestStalledWriteReturnsAfterDeadline(t *testing.T) {
	w := NewWriter(newDeadlineWriter(), 30*time.Millisecond)
	start := time.Now()
	err := w.WriteEvent(domain.NodeEvent{RunID: "run-1", Seq: 1, Type: graph.EventSessionUpdated, Payload: []byte(`{}`)})
	if err == nil {
		t.Fatal("WriteEvent succeeded on a stalled writer, want a deadline error")
	}
	if elapsed := time.Since(start); elapsed < 20*time.Millisecond || elapsed > time.Second {
		t.Errorf("WriteEvent returned after %v, want about the 30ms deadline", elapsed)
	}
}
