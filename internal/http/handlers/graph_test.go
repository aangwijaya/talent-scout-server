package handlers

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
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
	expectEOFWithin(t, reader, 500*time.Millisecond)
}

func readFrame(t *testing.T, reader *bufio.Reader) string {
	t.Helper()
	frame, err := readFrameErr(reader)
	if err != nil {
		t.Fatalf("read SSE frame: %v (partial: %q)", err, frame)
	}
	return frame
}

func readFrameErr(reader *bufio.Reader) (string, error) {
	var frame strings.Builder
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return frame.String(), err
		}
		frame.WriteString(line)
		if line == "\n" {
			return frame.String(), nil
		}
	}
}

func readFrameWithin(t *testing.T, reader *bufio.Reader, timeout time.Duration) string {
	t.Helper()
	type result struct {
		frame string
		err   error
	}
	ch := make(chan result, 1)
	go func() {
		frame, err := readFrameErr(reader)
		ch <- result{frame: frame, err: err}
	}()
	select {
	case res := <-ch:
		if res.err != nil {
			t.Fatalf("read SSE frame: %v (partial: %q)", res.err, res.frame)
		}
		return res.frame
	case <-time.After(timeout):
		t.Fatalf("timed out after %s waiting for an SSE frame", timeout)
		return ""
	}
}

func expectEOFWithin(t *testing.T, reader *bufio.Reader, timeout time.Duration) {
	t.Helper()
	ch := make(chan error, 1)
	go func() {
		_, err := reader.ReadString('\n')
		ch <- err
	}()
	select {
	case err := <-ch:
		if !errors.Is(err, io.EOF) {
			t.Fatalf("stream read error = %v, want io.EOF", err)
		}
	case <-time.After(timeout):
		t.Fatalf("stream did not reach EOF within %s", timeout)
	}
}

// afterListStore runs afterList once, right after ListNodeEvents returns. It
// reproduces events that land between the replay read and the live loop.
type afterListStore struct {
	store.Store
	once  sync.Once
	after func()
}

func (s *afterListStore) ListNodeEvents(ctx context.Context, runID string, sinceSeq int64) ([]domain.NodeEvent, error) {
	events, err := s.Store.ListNodeEvents(ctx, runID, sinceSeq)
	s.once.Do(s.after)
	return events, err
}

func newAfterListFixture(t *testing.T, after func(st *store.Memory, bus *graph.Bus, runID string)) (*store.Memory, *graph.Bus, *GraphRuns, string) {
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
	wrapped := &afterListStore{Store: st, after: func() { after(st, bus, runID) }}
	handler := NewGraphRuns(wrapped)
	handler.SetBus(bus)
	return st, bus, handler, runID
}

func startRunStream(t *testing.T, handler *GraphRuns, runID, lastEventID, rawQuery string) *bufio.Reader {
	t.Helper()
	server := httptest.NewServer(runStreamRouter(handler))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(func() {
		cancel()
		server.Close()
	})
	url := server.URL + "/graph-runs/" + runID + "/events"
	if rawQuery != "" {
		url += "?" + rawQuery
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	if lastEventID != "" {
		req.Header.Set("Last-Event-ID", lastEventID)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	return bufio.NewReader(resp.Body)
}

func TestRunStreamDeliversTerminalEventEmittedDuringReplay(t *testing.T) {
	st, _, handler, runID := newAfterListFixture(t, func(st *store.Memory, bus *graph.Bus, runID string) {
		if _, err := bus.Emit(context.Background(), runID, graph.Event{
			Type:    graph.EventRunCompleted,
			Payload: map[string]any{},
		}); err != nil {
			t.Errorf("Emit(run.completed): %v", err)
		}
		if err := st.UpdateGraphRun(context.Background(), domain.GraphRun{
			ID:     runID,
			Status: domain.GraphRunStatusSucceeded,
		}); err != nil {
			t.Errorf("UpdateGraphRun(succeeded): %v", err)
		}
	})
	if _, err := st.AppendNodeEvent(context.Background(), domain.NodeEvent{
		RunID: runID, Type: graph.EventRunStarted, Payload: []byte(`{}`),
	}); err != nil {
		t.Fatalf("AppendNodeEvent: %v", err)
	}

	reader := startRunStream(t, handler, runID, "", "")
	if frame := readFrameWithin(t, reader, time.Second); !strings.Contains(frame, "event: run.started") {
		t.Fatalf("first frame = %q, want run.started", frame)
	}
	terminal := readFrameWithin(t, reader, time.Second)
	if !strings.Contains(terminal, "event: run.completed") {
		t.Fatalf("terminal frame = %q, want run.completed", terminal)
	}
	expectEOFWithin(t, reader, 500*time.Millisecond)
}

func TestRunStreamClosesWithEOFAfterTerminalEvent(t *testing.T) {
	st, bus, handler, runID := graphHandlersFixture(t)
	if _, err := st.AppendNodeEvent(context.Background(), domain.NodeEvent{
		RunID: runID, Type: graph.EventRunStarted, Payload: []byte(`{}`),
	}); err != nil {
		t.Fatalf("AppendNodeEvent: %v", err)
	}

	reader := startRunStream(t, handler, runID, "", "")
	if frame := readFrameWithin(t, reader, time.Second); !strings.Contains(frame, "event: run.started") {
		t.Fatalf("first frame = %q, want run.started", frame)
	}

	emitRunEvent(t, bus, runID, graph.EventRunCompleted, "")
	terminal := readFrameWithin(t, reader, time.Second)
	if !strings.Contains(terminal, "event: run.completed") {
		t.Fatalf("terminal frame = %q, want run.completed", terminal)
	}
	expectEOFWithin(t, reader, 500*time.Millisecond)
}

func TestRunStreamHeartbeatsWhileIdle(t *testing.T) {
	_, _, handler, runID := graphHandlersFixture(t)
	handler.heartbeatInterval = 20 * time.Millisecond

	reader := startRunStream(t, handler, runID, "", "")
	frame := readFrameWithin(t, reader, 500*time.Millisecond)
	if !strings.Contains(frame, ": ping") {
		t.Fatalf("idle frame = %q, want a : ping heartbeat", frame)
	}
}

func TestEndedRunStreamClosesAfterReplay(t *testing.T) {
	st, _, handler, runID := graphHandlersFixture(t)
	if _, err := st.AppendNodeEvent(context.Background(), domain.NodeEvent{
		RunID: runID, Type: graph.EventRunStarted, Payload: []byte(`{}`),
	}); err != nil {
		t.Fatalf("AppendNodeEvent: %v", err)
	}
	terminal, err := st.AppendNodeEvent(context.Background(), domain.NodeEvent{
		RunID: runID, Type: graph.EventRunCompleted, Payload: []byte(`{}`),
	})
	if err != nil {
		t.Fatalf("AppendNodeEvent: %v", err)
	}
	if err := st.UpdateGraphRun(context.Background(), domain.GraphRun{
		ID:     runID,
		Status: domain.GraphRunStatusSucceeded,
	}); err != nil {
		t.Fatalf("UpdateGraphRun: %v", err)
	}

	if terminal.Seq <= 0 {
		t.Fatalf("terminal seq = %d, want > 0", terminal.Seq)
	}
	// The replay is empty (since_seq = last seq); the stream must close at once.
	reader := startRunStream(t, handler, runID, "", "since_seq="+strconv.FormatInt(terminal.Seq, 10))
	expectEOFWithin(t, reader, 500*time.Millisecond)
}

func TestRunStreamDeliversHandoverEventExactlyOnce(t *testing.T) {
	st, bus, handler, runID := newAfterListFixture(t, func(_ *store.Memory, bus *graph.Bus, runID string) {
		if _, err := bus.Emit(context.Background(), runID, graph.Event{
			NodeID:  domain.StrPtr("guard"),
			Type:    graph.EventNodeStarted,
			Payload: map[string]any{},
		}); err != nil {
			t.Errorf("Emit(node.started): %v", err)
		}
	})
	if _, err := st.AppendNodeEvent(context.Background(), domain.NodeEvent{
		RunID: runID, Type: graph.EventRunStarted, Payload: []byte(`{}`),
	}); err != nil {
		t.Fatalf("AppendNodeEvent: %v", err)
	}

	reader := startRunStream(t, handler, runID, "1", "")
	handover := readFrameWithin(t, reader, time.Second)
	if !strings.Contains(handover, "event: node.started") || !strings.Contains(handover, "id: 2") {
		t.Fatalf("handover frame = %q, want node.started id 2", handover)
	}

	emitRunEvent(t, bus, runID, graph.EventRunCompleted, "")
	next := readFrameWithin(t, reader, time.Second)
	if !strings.Contains(next, "event: run.completed") || !strings.Contains(next, "id: 3") {
		t.Fatalf("next frame = %q, want run.completed id 3 (handover must not repeat)", next)
	}
	expectEOFWithin(t, reader, 500*time.Millisecond)
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
