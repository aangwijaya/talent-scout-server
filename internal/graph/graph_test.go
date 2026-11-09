package graph

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aangwijaya/talent-scout-server/internal/config"
	"github.com/aangwijaya/talent-scout-server/internal/domain"
	"github.com/aangwijaya/talent-scout-server/internal/llm"
	"github.com/aangwijaya/talent-scout-server/internal/store"
)

func testModels() config.ModelsConfig {
	return config.ModelsConfig{
		Default: config.Defaults{Temperature: 0.3, TimeoutSeconds: 30, MaxRetriesOnHTTPError: 1},
		Nodes: map[string]config.Node{
			"planner":     {Model: "deepseek/deepseek-v4.1-flash", Temperature: f64(0.2), MaxTokens: 800},
			"interviewer": {Model: "z-ai/glm-5.3-flash", MaxTokens: 300, Stream: true},
			"judge":       {Model: "deepseek/deepseek-v4.1-flash", MaxTokens: 200},
			"scorer":      {Model: "deepseek/deepseek-v4.1-flash", MaxTokens: 1200},
		},
		Prices: map[string]config.Price{
			"deepseek/deepseek-v4.1-flash": {In: 0.045, Out: 0.5},
			"z-ai/glm-5.3-flash":           {In: 0.15, Out: 0.50},
		},
	}
}

func f64(v float64) *float64 { return &v }

func newRunner(t *testing.T) (*Runner, *store.Memory, context.Context) {
	t.Helper()
	ctx := context.Background()
	st := store.NewMemory()
	runID := domain.NewID()
	sessionID := domain.NewID()
	if err := st.CreateGraphRun(ctx, domain.GraphRun{ID: runID, SessionID: sessionID, Status: domain.GraphRunStatusRunning}); err != nil {
		t.Fatalf("CreateGraphRun: %v", err)
	}
	llmClient := llm.NewFake(map[string]string{"deepseek/deepseek-v4.1-flash": "planned"})
	runner := NewRunner(st, NewBus(st), llmClient, testModels(), runID, sessionID)
	return runner, st, ctx
}

func eventTypes(t *testing.T, st store.Store, runID string) []string {
	t.Helper()
	events, err := st.ListNodeEvents(context.Background(), runID, 0)
	if err != nil {
		t.Fatalf("ListNodeEvents: %v", err)
	}
	types := make([]string, len(events))
	for i, e := range events {
		types[i] = e.Type
	}
	return types
}

func TestRunnerEventOrder(t *testing.T) {
	runner, st, ctx := newRunner(t)

	if err := runner.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if _, err := runner.RunLLMNode(ctx, "planner", 1, llm.Request{
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "plan this"}},
	}, "", nil); err != nil {
		t.Fatalf("RunLLMNode(planner): %v", err)
	}

	if err := runner.EmitEdge(ctx, "planner", "plan_check", "planned"); err != nil {
		t.Fatalf("EmitEdge: %v", err)
	}

	if err := runner.RunCodeNode(ctx, "plan_check", 1, "", func() (string, error) {
		return "valid", nil
	}); err != nil {
		t.Fatalf("RunCodeNode(plan_check): %v", err)
	}

	if err := runner.EmitCheckPassed(ctx, "plan_check", 1, "plan_topics", nil); err != nil {
		t.Fatalf("EmitCheckPassed: %v", err)
	}

	if err := runner.Complete(ctx, "completed", 0); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	got := eventTypes(t, st, runner.RunID())
	want := []string{
		EventRunStarted,
		EventNodeStarted, EventNodeCompleted,
		EventEdgeTraversed,
		EventNodeStarted, EventNodeCompleted,
		EventCheckPassed,
		EventRunCompleted,
	}
	if len(got) != len(want) {
		t.Fatalf("event types = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("event[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestRunnerEventSeqMonotonic(t *testing.T) {
	runner, st, ctx := newRunner(t)

	if err := runner.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := runner.RunLLMNode(ctx, "planner", 1, llm.Request{Messages: []llm.Message{{Role: llm.RoleUser, Content: "x"}}}, "", nil); err != nil {
		t.Fatalf("RunLLMNode: %v", err)
	}
	if err := runner.Complete(ctx, "completed", 0); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	events, err := st.ListNodeEvents(ctx, runner.RunID(), 0)
	if err != nil {
		t.Fatalf("ListNodeEvents: %v", err)
	}
	for i, e := range events {
		if want := int64(i + 1); e.Seq != want {
			t.Errorf("event[%d].Seq = %d, want %d", i, e.Seq, want)
		}
	}
}

func TestRunStartedPayload(t *testing.T) {
	runner, st, ctx := newRunner(t)
	if err := runner.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	events, _ := st.ListNodeEvents(ctx, runner.RunID(), 0)
	var payload RunStarted
	if err := json.Unmarshal(events[0].Payload, &payload); err != nil {
		t.Fatalf("unmarshal run.started: %v", err)
	}
	if payload.SessionID == "" {
		t.Error("run.started session_id is empty")
	}
	if len(payload.Nodes) != 8 {
		t.Errorf("nodes = %d, want 8", len(payload.Nodes))
	}
	if payload.Nodes[0].ID != "planner" || payload.Nodes[0].Type != "splitter" {
		t.Errorf("nodes[0] = %+v, want planner/splitter", payload.Nodes[0])
	}
	if len(payload.Edges) == 0 {
		t.Error("run.started edges is empty")
	}
}

func TestRunStartedIncludesPlannerRetryEdge(t *testing.T) {
	runner, st, ctx := newRunner(t)
	if err := runner.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	events, err := st.ListNodeEvents(ctx, runner.RunID(), 0)
	if err != nil {
		t.Fatalf("ListNodeEvents: %v", err)
	}
	var payload RunStarted
	if err := json.Unmarshal(events[0].Payload, &payload); err != nil {
		t.Fatalf("unmarshal run.started: %v", err)
	}
	want := EdgeRef{From: "plan_check", To: "planner"}
	for _, e := range payload.Edges {
		if e == want {
			return
		}
	}
	t.Errorf("run.started edges %v lack %+v (planner retry per EVENTS.md §6.2)", payload.Edges, want)
}

func TestNodeCompletedRecordsTokensAndCost(t *testing.T) {
	runner, st, ctx := newRunner(t)

	if _, err := runner.RunLLMNode(ctx, "planner", 1, llm.Request{
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "a reasonably long prompt"}},
	}, "", nil); err != nil {
		t.Fatalf("RunLLMNode: %v", err)
	}

	events, _ := st.ListNodeEvents(ctx, runner.RunID(), 0)
	var completed domain.NodeEvent
	for _, e := range events {
		if e.Type == EventNodeCompleted {
			completed = e
		}
	}
	if completed.Type == "" {
		t.Fatal("no node.completed event")
	}
	var payload NodeCompleted
	if err := json.Unmarshal(completed.Payload, &payload); err != nil {
		t.Fatalf("unmarshal node.completed: %v", err)
	}
	if payload.TokensIn <= 0 || payload.TokensOut <= 0 {
		t.Errorf("tokens = %d/%d, want positive", payload.TokensIn, payload.TokensOut)
	}
	if payload.CostUSD <= 0 {
		t.Errorf("cost_usd = %v, want positive", payload.CostUSD)
	}
	if completed.Model == nil || *completed.Model != "deepseek/deepseek-v4.1-flash" {
		t.Errorf("model = %v, want deepseek/deepseek-v4.1-flash", completed.Model)
	}
}

func TestRunCompletedTotals(t *testing.T) {
	runner, st, ctx := newRunner(t)

	if _, err := runner.RunLLMNode(ctx, "planner", 1, llm.Request{Messages: []llm.Message{{Role: llm.RoleUser, Content: "x"}}}, "", nil); err != nil {
		t.Fatalf("RunLLMNode: %v", err)
	}
	if err := runner.Complete(ctx, "completed", 2); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	events, _ := st.ListNodeEvents(ctx, runner.RunID(), 0)
	var last domain.NodeEvent
	for _, e := range events {
		last = e
	}
	if last.Type != EventRunCompleted {
		t.Fatalf("last event = %q, want run.completed", last.Type)
	}
	var payload RunCompleted
	if err := json.Unmarshal(last.Payload, &payload); err != nil {
		t.Fatalf("unmarshal run.completed: %v", err)
	}
	if payload.Status != "succeeded" || payload.FinishReason != "completed" {
		t.Errorf("run.completed = %+v", payload)
	}
	if payload.TotalTokensIn <= 0 || payload.NeedsManualCount != 2 {
		t.Errorf("run.completed totals = %+v", payload)
	}

	run, err := st.GetGraphRun(ctx, runner.RunID())
	if err != nil {
		t.Fatalf("GetGraphRun: %v", err)
	}
	if run.Status != domain.GraphRunStatusSucceeded || run.EndedAt == nil {
		t.Errorf("graph_run = %+v", run)
	}
	if run.TotalTokensIn != payload.TotalTokensIn || run.CostUSD != payload.CostUSD {
		t.Errorf("graph_run totals (%d, %v) != event totals (%d, %v)", run.TotalTokensIn, run.CostUSD, payload.TotalTokensIn, payload.CostUSD)
	}
}

func TestRunnerFakeRepliesPerNode(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemory()
	runID := domain.NewID()
	sessionID := domain.NewID()
	if err := st.CreateGraphRun(ctx, domain.GraphRun{ID: runID, SessionID: sessionID, Status: domain.GraphRunStatusRunning}); err != nil {
		t.Fatalf("CreateGraphRun: %v", err)
	}
	fake := llm.NewFake(map[string]string{"planner": "plan-reply", "judge": "judge-reply"})
	runner := NewRunner(st, NewBus(st), fake, testModels(), runID, sessionID)

	planner, err := runner.RunLLMNode(ctx, "planner", 1, llm.Request{Messages: []llm.Message{{Role: llm.RoleUser, Content: "x"}}}, "", nil)
	if err != nil {
		t.Fatalf("RunLLMNode(planner): %v", err)
	}
	judge, err := runner.RunLLMNode(ctx, "judge", 1, llm.Request{Messages: []llm.Message{{Role: llm.RoleUser, Content: "x"}}}, "", nil)
	if err != nil {
		t.Fatalf("RunLLMNode(judge): %v", err)
	}
	if planner.Content != "plan-reply" || judge.Content != "judge-reply" {
		t.Errorf("Content = %q/%q, want plan-reply/judge-reply", planner.Content, judge.Content)
	}
}

func TestRunTotalsSumNodeCompleted(t *testing.T) {
	runner, st, ctx := newRunner(t)

	for _, nodeID := range []string{"planner", "judge"} {
		if _, err := runner.RunLLMNode(ctx, nodeID, 1, llm.Request{
			Messages: []llm.Message{{Role: llm.RoleUser, Content: "some prompt text"}},
		}, "", nil); err != nil {
			t.Fatalf("RunLLMNode(%s): %v", nodeID, err)
		}
	}
	if err := runner.Complete(ctx, "completed", 0); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	events, err := st.ListNodeEvents(ctx, runner.RunID(), 0)
	if err != nil {
		t.Fatalf("ListNodeEvents: %v", err)
	}
	var wantIn, wantOut int
	var wantCost float64
	var runCompleted *RunCompleted
	for _, e := range events {
		switch e.Type {
		case EventNodeCompleted:
			var payload NodeCompleted
			if err := json.Unmarshal(e.Payload, &payload); err != nil {
				t.Fatalf("unmarshal node.completed: %v", err)
			}
			wantIn += payload.TokensIn
			wantOut += payload.TokensOut
			wantCost += payload.CostUSD
		case EventRunCompleted:
			var payload RunCompleted
			if err := json.Unmarshal(e.Payload, &payload); err != nil {
				t.Fatalf("unmarshal run.completed: %v", err)
			}
			runCompleted = &payload
		}
	}
	if runCompleted == nil {
		t.Fatal("no run.completed event")
	}
	if wantIn == 0 || wantOut == 0 || wantCost == 0 {
		t.Fatalf("node sums = %d/%d/%v, want all positive", wantIn, wantOut, wantCost)
	}
	if runCompleted.TotalTokensIn != wantIn || runCompleted.TotalTokensOut != wantOut || runCompleted.CostUSD != wantCost {
		t.Errorf("run.completed = %d/%d/%v, want sums %d/%d/%v",
			runCompleted.TotalTokensIn, runCompleted.TotalTokensOut, runCompleted.CostUSD, wantIn, wantOut, wantCost)
	}

	run, err := st.GetGraphRun(ctx, runner.RunID())
	if err != nil {
		t.Fatalf("GetGraphRun: %v", err)
	}
	if run.TotalTokensIn != wantIn || run.TotalTokensOut != wantOut || run.CostUSD != wantCost {
		t.Errorf("stored graph_run = %d/%d/%v, want sums %d/%d/%v",
			run.TotalTokensIn, run.TotalTokensOut, run.CostUSD, wantIn, wantOut, wantCost)
	}
}

func TestRunnerTransportRetryChoreography(t *testing.T) {
	ctx := context.Background()

	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		fmt.Fprint(w, `{"choices":[{"message":{"content":"sufficient"}}],"usage":{"prompt_tokens":8,"completion_tokens":2}}`)
	}))
	defer srv.Close()

	st := store.NewMemory()
	runID := domain.NewID()
	sessionID := domain.NewID()
	if err := st.CreateGraphRun(ctx, domain.GraphRun{ID: runID, SessionID: sessionID, Status: domain.GraphRunStatusRunning}); err != nil {
		t.Fatalf("CreateGraphRun: %v", err)
	}

	runner := NewRunner(st, NewBus(st), llm.NewOpenRouter(srv.URL, "k", nil), testModels(), runID, sessionID)

	resp, err := runner.RunLLMNode(ctx, "judge", 1, llm.Request{
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "verdict"}},
	}, "", nil)
	if err != nil {
		t.Fatalf("RunLLMNode: %v", err)
	}
	if resp.Content != "sufficient" {
		t.Errorf("Content = %q, want sufficient", resp.Content)
	}

	events, err := st.ListNodeEvents(ctx, runID, 0)
	if err != nil {
		t.Fatalf("ListNodeEvents: %v", err)
	}
	got := make([]string, len(events))
	for i, e := range events {
		got[i] = e.Type
	}
	want := []string{EventNodeStarted, EventNodeFailed, EventNodeStarted, EventNodeCompleted}
	if len(got) != len(want) {
		t.Fatalf("event types = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("event[%d] = %q, want %q", i, got[i], want[i])
		}
	}

	var failed NodeFailed
	if err := json.Unmarshal(events[1].Payload, &failed); err != nil {
		t.Fatalf("unmarshal node.failed: %v", err)
	}
	if !failed.WillRetry {
		t.Errorf("node.failed will_retry = false, want true")
	}
	if events[0].Attempt == nil || *events[0].Attempt != 1 {
		t.Errorf("first node.started attempt = %v, want 1", events[0].Attempt)
	}
	if events[3].Attempt == nil || *events[3].Attempt != 2 {
		t.Errorf("node.completed attempt = %v, want 2", events[3].Attempt)
	}
}

func TestRunnerLLMNodeHonorsLoopAttempt(t *testing.T) {
	runner, st, ctx := newRunner(t)

	for _, attempt := range []int{1, 2} {
		if attempt == 2 {
			if err := runner.EmitLoopRetry(ctx, "planner", 2, 2, "invalid_plan", "fix it"); err != nil {
				t.Fatalf("EmitLoopRetry: %v", err)
			}
		}
		if _, err := runner.RunLLMNode(ctx, "planner", attempt, llm.Request{
			Messages: []llm.Message{{Role: llm.RoleUser, Content: "plan"}},
		}, "", nil); err != nil {
			t.Fatalf("RunLLMNode(attempt %d): %v", attempt, err)
		}
	}

	events, err := st.ListNodeEvents(ctx, runner.RunID(), 0)
	if err != nil {
		t.Fatalf("ListNodeEvents: %v", err)
	}
	var started []int
	for _, e := range events {
		if e.Type == EventNodeStarted && e.NodeID != nil && *e.NodeID == "planner" {
			if e.Attempt == nil {
				t.Fatal("planner node.started has nil attempt")
			}
			started = append(started, *e.Attempt)
		}
	}
	want := []int{1, 2}
	if len(started) != len(want) {
		t.Fatalf("planner node.started attempts = %v, want %v", started, want)
	}
	for i := range want {
		if started[i] != want[i] {
			t.Errorf("node.started[%d] attempt = %d, want %d (after loop.retry(attempt=2))", i, started[i], want[i])
		}
	}
}

func TestRunnerTransportRetryContinuesLoopAttemptNumbering(t *testing.T) {
	ctx := context.Background()

	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		fmt.Fprint(w, `{"choices":[{"message":{"content":"scored"}}],"usage":{"prompt_tokens":8,"completion_tokens":2}}`)
	}))
	defer srv.Close()

	st := store.NewMemory()
	runID := domain.NewID()
	sessionID := domain.NewID()
	if err := st.CreateGraphRun(ctx, domain.GraphRun{ID: runID, SessionID: sessionID, Status: domain.GraphRunStatusRunning}); err != nil {
		t.Fatalf("CreateGraphRun: %v", err)
	}
	runner := NewRunner(st, NewBus(st), llm.NewOpenRouter(srv.URL, "k", nil), testModels(), runID, sessionID)

	if _, err := runner.RunLLMNode(ctx, "scorer", 2, llm.Request{
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "score"}},
	}, "", nil); err != nil {
		t.Fatalf("RunLLMNode: %v", err)
	}

	events, err := st.ListNodeEvents(ctx, runID, 0)
	if err != nil {
		t.Fatalf("ListNodeEvents: %v", err)
	}
	wantTypes := []string{EventNodeStarted, EventNodeFailed, EventNodeStarted, EventNodeCompleted}
	wantAttempts := []int{2, 2, 3, 3}
	if len(events) != len(wantTypes) {
		t.Fatalf("event types = %v, want %v", eventTypes(t, st, runID), wantTypes)
	}
	for i := range wantTypes {
		if events[i].Type != wantTypes[i] {
			t.Errorf("event[%d] = %q, want %q", i, events[i].Type, wantTypes[i])
		}
		if events[i].Attempt == nil || *events[i].Attempt != wantAttempts[i] {
			t.Errorf("event[%d] attempt = %v, want %d", i, events[i].Attempt, wantAttempts[i])
		}
	}
}

func TestRunnerTruncationEmitsNodeFailed(t *testing.T) {
	ctx := context.Background()

	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		fmt.Fprint(w, `{"choices":[{"message":{"content":"{\"partial\":"},"finish_reason":"length"}],"usage":{}}`)
	}))
	defer srv.Close()

	st := store.NewMemory()
	runID := domain.NewID()
	sessionID := domain.NewID()
	if err := st.CreateGraphRun(ctx, domain.GraphRun{ID: runID, SessionID: sessionID, Status: domain.GraphRunStatusRunning}); err != nil {
		t.Fatalf("CreateGraphRun: %v", err)
	}
	runner := NewRunner(st, NewBus(st), llm.NewOpenRouter(srv.URL, "k", nil), testModels(), runID, sessionID)

	_, err := runner.RunLLMNode(ctx, "judge", 1, llm.Request{
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "verdict"}},
	}, "", nil)
	if !errors.Is(err, llm.ErrTruncated) {
		t.Fatalf("error = %v, want ErrTruncated", err)
	}
	if calls != 1 {
		t.Errorf("calls = %d, want 1 (truncation is not retryable)", calls)
	}

	events, _ := st.ListNodeEvents(ctx, runID, 0)
	if len(events) != 2 || events[0].Type != EventNodeStarted || events[1].Type != EventNodeFailed {
		t.Fatalf("event types = %v, want [node.started node.failed]", eventTypes(t, st, runID))
	}
	var failed NodeFailed
	if err := json.Unmarshal(events[1].Payload, &failed); err != nil {
		t.Fatalf("unmarshal node.failed: %v", err)
	}
	if failed.WillRetry {
		t.Errorf("node.failed will_retry = true, want false for truncation")
	}
}

func TestMessageDeltaNotPersisted(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemory()
	runID := domain.NewID()
	sessionID := domain.NewID()
	if err := st.CreateGraphRun(ctx, domain.GraphRun{ID: runID, SessionID: sessionID, Status: domain.GraphRunStatusRunning}); err != nil {
		t.Fatalf("CreateGraphRun: %v", err)
	}

	bus := NewBus(st)
	ch, cancel := bus.Subscribe(runID)
	defer cancel()

	if _, err := bus.Emit(ctx, runID, Event{NodeID: domain.StrPtr("interviewer"), Type: EventNodeStarted, Payload: NodeStarted{}}); err != nil {
		t.Fatalf("Emit node.started: %v", err)
	}
	if _, err := bus.Emit(ctx, runID, Event{NodeID: domain.StrPtr("interviewer"), Type: EventMessageDelta, Payload: MessageDelta{MessageID: "m", Delta: "hi"}}); err != nil {
		t.Fatalf("Emit message.delta: %v", err)
	}

	// Both events reached the subscriber, but only node.started is persisted.
	subscriberEvents := []string{(<-ch).Type, (<-ch).Type}
	if subscriberEvents[0] != EventNodeStarted || subscriberEvents[1] != EventMessageDelta {
		t.Errorf("subscriber events = %v, want [node.started message.delta]", subscriberEvents)
	}

	persisted, err := st.ListNodeEvents(ctx, runID, 0)
	if err != nil {
		t.Fatalf("ListNodeEvents: %v", err)
	}
	if len(persisted) != 1 || persisted[0].Type != EventNodeStarted {
		t.Errorf("persisted = %+v, want only node.started (no message.delta)", persisted)
	}
}

func TestBusPersistedEventsAreNotDropped(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemory()
	runID := domain.NewID()
	bus := NewBus(st)
	ch, cancel := bus.Subscribe(runID)
	defer cancel()

	for i := 0; i < subscriberBuffer; i++ {
		if _, err := bus.Emit(ctx, runID, Event{Type: EventNodeSkipped, Payload: NodeSkipped{Reason: "fill"}}); err != nil {
			t.Fatalf("fill emit %d: %v", i, err)
		}
	}

	emitted := make(chan error, 1)
	go func() {
		_, err := bus.Emit(ctx, runID, Event{Type: EventRunCompleted, Payload: RunCompleted{Status: "succeeded"}})
		emitted <- err
	}()

	select {
	case err := <-emitted:
		t.Fatalf("Emit returned (err=%v) while the subscriber buffer was full; a persisted event was dropped", err)
	case <-time.After(100 * time.Millisecond):
	}

	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("no buffered event to drain")
	}
	select {
	case err := <-emitted:
		if err != nil {
			t.Fatalf("Emit: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Emit stayed blocked after the subscriber made room")
	}

	sawCompleted := false
	for i := 0; i < subscriberBuffer; i++ {
		select {
		case ev := <-ch:
			if ev.Type == EventRunCompleted {
				sawCompleted = true
			}
		case <-time.After(time.Second):
			t.Fatalf("delivered only %d events, want %d", i, subscriberBuffer+1)
		}
	}
	if !sawCompleted {
		t.Error("run.completed was never delivered to the subscriber")
	}
}

func TestBusMessageDeltaIsDroppable(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemory()
	runID := domain.NewID()
	bus := NewBus(st)
	_, cancel := bus.Subscribe(runID)
	defer cancel()

	for i := 0; i < subscriberBuffer; i++ {
		if _, err := bus.Emit(ctx, runID, Event{Type: EventMessageDelta, Payload: MessageDelta{Delta: "x"}}); err != nil {
			t.Fatalf("emit delta %d: %v", i, err)
		}
	}

	done := make(chan struct{})
	go func() {
		_, _ = bus.Emit(ctx, runID, Event{Type: EventMessageDelta, Payload: MessageDelta{Delta: "overflow"}})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("message.delta blocked while the subscriber buffer was full; deltas must stay droppable")
	}
}

// A connected but stalled subscriber fills its buffer and would otherwise block
// every persisted Emit. Cancelling the subscription (what the SSE handler does
// after a write deadline) must unblock the publisher.
func TestBusCancelUnblocksStalledPublisher(t *testing.T) {
	st := store.NewMemory()
	bus := NewBus(st)
	runID := domain.NewID()
	_, cancel := bus.Subscribe(runID)

	done := make(chan error, 1)
	go func() {
		for i := 0; i < subscriberBuffer+5; i++ {
			if _, err := bus.Emit(context.Background(), runID, Event{
				Type:    EventNodeStarted,
				Payload: NodeStarted{InputSummary: "x"},
			}); err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()

	select {
	case <-done:
		t.Fatal("Emit finished although the subscriber never read")
	case <-time.After(50 * time.Millisecond):
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Emit after cancel: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Emit still blocked after the subscription was cancelled")
	}
}
