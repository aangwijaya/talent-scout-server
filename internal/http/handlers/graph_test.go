package handlers

import (
	"bufio"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/aangwijaya/talent-scout-server/internal/domain"
	"github.com/aangwijaya/talent-scout-server/internal/graph"
	"github.com/aangwijaya/talent-scout-server/internal/store"
)

const graphHandlersRunID = "7c9e6679-0000-4000-8000-000000000401"

func graphHandlersFixture(t *testing.T) (*store.Memory, *graph.Bus, *GraphRuns, string) {
	t.Helper()
	st := store.NewMemory()
	runID := graphHandlersRunID
	if err := st.CreateGraphRun(context.Background(), domain.GraphRun{
		ID:        runID,
		SessionID: "7c9e6679-0000-4000-8000-000000000402",
		Status:    domain.GraphRunStatusRunning,
	}); err != nil {
		t.Fatalf("CreateGraphRun: %v", err)
	}
	bus := graph.NewBus(st)
	handler := NewGraphRuns(st)
	handler.SetBus(bus)
	return st, bus, handler, runID
}

func runStreamRouter(handler *GraphRuns) http.Handler {
	r := chi.NewRouter()
	r.Get("/graph-runs/{runId}/events", handler.Events)
	return r
}

func emitRunEvent(t *testing.T, bus *graph.Bus, runID, eventType string, nodeID string) {
	t.Helper()
	var node *string
	if nodeID != "" {
		node = domain.StrPtr(nodeID)
	}
	if _, err := bus.Emit(context.Background(), runID, graph.Event{
		NodeID:  node,
		Type:    eventType,
		Payload: map[string]any{},
	}); err != nil {
		t.Fatalf("Emit(%s): %v", eventType, err)
	}
}

func TestRunStreamReplaysThenStreamsLive(t *testing.T) {
	st, bus, handler, runID := graphHandlersFixture(t)
	if _, err := st.AppendNodeEvent(context.Background(), domain.NodeEvent{
		RunID: runID, Type: graph.EventRunStarted, Payload: []byte(`{}`),
	}); err != nil {
		t.Fatalf("AppendNodeEvent: %v", err)
	}
	if _, err := st.AppendNodeEvent(context.Background(), domain.NodeEvent{
		RunID: runID, NodeID: domain.StrPtr("interviewer"), Type: graph.EventNodeSkipped, Payload: []byte(`{"reason":"run_failed"}`),
	}); err != nil {
		t.Fatalf("AppendNodeEvent: %v", err)
	}

	server := httptest.NewServer(runStreamRouter(handler))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/graph-runs/"+runID+"/events", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Last-Event-ID", "1")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	reader := bufio.NewReader(resp.Body)
	replayed := readFrame(t, reader)
	if !strings.Contains(replayed, "event: node.skipped") || !strings.Contains(replayed, "id: 2") {
		t.Fatalf("replayed frame = %q, want node.skipped with id 2", replayed)
	}

	emitRunEvent(t, bus, runID, graph.EventNodeStarted, "guard")
	live := readFrame(t, reader)
	if !strings.Contains(live, "event: node.started") || !strings.Contains(live, "id: 3") {
		t.Fatalf("live frame = %q, want node.started with id 3", live)
	}

	emitRunEvent(t, bus, runID, graph.EventRunCompleted, "")
	completed := readFrame(t, reader)
	if !strings.Contains(completed, "event: run.completed") {
		t.Fatalf("final frame = %q, want run.completed", completed)
	}
	if _, err := reader.ReadString('\n'); err == nil {
		t.Fatal("stream stayed open after run.completed")
	}
}

func readFrame(t *testing.T, reader *bufio.Reader) string {
	t.Helper()
	var frame strings.Builder
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("read SSE frame: %v (partial: %q)", err, frame.String())
		}
		frame.WriteString(line)
		if line == "\n" {
			return frame.String()
		}
	}
}

// stalledResponseWriter blocks every Write until its write deadline fires,
// mimicking a connected browser that never reads the response body.
type stalledResponseWriter struct {
	header  http.Header
	entered chan struct{}
	done    chan struct{}
	once    sync.Once
}

func newStalledResponseWriter() *stalledResponseWriter {
	return &stalledResponseWriter{header: make(http.Header), entered: make(chan struct{}), done: make(chan struct{})}
}

func (w *stalledResponseWriter) Header() http.Header { return w.header }
func (w *stalledResponseWriter) WriteHeader(int)     {}
func (w *stalledResponseWriter) Flush()              {}

func (w *stalledResponseWriter) Write([]byte) (int, error) {
	w.once.Do(func() { close(w.entered) })
	<-w.done
	return 0, errors.New("write timeout")
}

func (w *stalledResponseWriter) SetWriteDeadline(deadline time.Time) error {
	if deadline.IsZero() {
		return nil
	}
	time.AfterFunc(time.Until(deadline), func() {
		select {
		case <-w.done:
		default:
			close(w.done)
		}
	})
	return nil
}

func TestStalledRunStreamDoesNotBlockEmitBeyondDeadline(t *testing.T) {
	st, bus, handler, runID := graphHandlersFixture(t)
	handler.writeDeadline = 40 * time.Millisecond
	if _, err := st.AppendNodeEvent(context.Background(), domain.NodeEvent{
		RunID: runID, Type: graph.EventRunStarted, Payload: []byte(`{}`),
	}); err != nil {
		t.Fatalf("AppendNodeEvent: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/graph-runs/"+runID+"/events", nil)
	writer := newStalledResponseWriter()
	handlerDone := make(chan struct{})
	go func() {
		runStreamRouter(handler).ServeHTTP(writer, req)
		close(handlerDone)
	}()

	select {
	case <-writer.entered:
	case <-time.After(time.Second):
		t.Fatal("handler never started writing the replay")
	}

	emitDone := make(chan error, 1)
	go func() {
		for i := 0; i < 140; i++ {
			if _, err := bus.Emit(context.Background(), runID, graph.Event{
				Type:    graph.EventNodeStarted,
				Payload: graph.NodeStarted{InputSummary: "x"},
			}); err != nil {
				emitDone <- err
				return
			}
		}
		emitDone <- nil
	}()

	select {
	case err := <-emitDone:
		if err != nil {
			t.Fatalf("Emit: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Emit stayed blocked after the SSE write deadline")
	}

	select {
	case <-handlerDone:
	case <-time.After(time.Second):
		t.Fatal("SSE handler did not return after the failed write")
	}
}
