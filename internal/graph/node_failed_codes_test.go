package graph

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/aangwijaya/talent-scout-server/internal/llm"
	"github.com/aangwijaya/talent-scout-server/internal/store"
)

const leakedTransportDetail = "upstream 500 https://internal.example/v1/chat secret-provider-body"

var leakedTransportFragments = []string{"upstream 500", "https://internal.example", "secret-provider-body"}

func nodeFailedPayloads(t *testing.T, st store.Store, sessionID, nodeID string) []NodeFailed {
	t.Helper()
	var out []NodeFailed
	for _, e := range sessionEvents(t, st, sessionID) {
		if e.Type != EventNodeFailed || e.NodeID == nil || *e.NodeID != nodeID {
			continue
		}
		var payload NodeFailed
		if err := json.Unmarshal(e.Payload, &payload); err != nil {
			t.Fatalf("unmarshal node.failed: %v", err)
		}
		out = append(out, payload)
	}
	return out
}

func assertNoLeakedTransportDetail(t *testing.T, st store.Store, sessionID string) {
	t.Helper()
	for _, e := range sessionEvents(t, st, sessionID) {
		for _, fragment := range leakedTransportFragments {
			if strings.Contains(string(e.Payload), fragment) {
				t.Errorf("%s event payload leaks transport detail %q: %s", e.Type, fragment, e.Payload)
			}
		}
	}
}

func TestPlannerOutageNodeFailedUsesLLMUnavailableCode(t *testing.T) {
	st, sess := seedGraphSession(t)
	rawErr := fmt.Errorf("%w: %s", llm.ErrUnavailable, leakedTransportDetail)
	client := &stubClient{chat: func(req llm.Request) (llm.Response, error) {
		if req.Node != "planner" {
			return llm.Response{Content: FakeReplies[req.Node]}, nil
		}
		if req.OnRetry != nil {
			req.OnRetry(1, rawErr)
		}
		return llm.Response{}, rawErr
	}}
	manager, _ := newTestManager(t, st, client)
	if err := manager.Start(context.Background(), sess); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitIdle(t, manager, sess.ID)

	failures := nodeFailedPayloads(t, st, sess.ID, "planner")
	if len(failures) != 2 {
		t.Fatalf("planner node.failed events = %d, want 2 (transport retry and final failure)", len(failures))
	}
	var sawRetry, sawFinal bool
	for _, failure := range failures {
		if failure.Error != "llm_unavailable" {
			t.Errorf("planner node.failed error = %q, want llm_unavailable", failure.Error)
		}
		if failure.WillRetry {
			sawRetry = true
		} else {
			sawFinal = true
		}
	}
	if !sawRetry || !sawFinal {
		t.Errorf("planner node.failed will_retry = retry:%v final:%v, want both", sawRetry, sawFinal)
	}
	assertNoLeakedTransportDetail(t, st, sess.ID)
}

func TestInterviewerOutageNodeFailedUsesLLMUnavailableCode(t *testing.T) {
	st, sess := seedGraphSession(t)
	rawErr := fmt.Errorf("%w: %s", llm.ErrUnavailable, leakedTransportDetail)
	client := &stubClient{stream: func(req llm.Request, onDelta func(string) error) (llm.Response, error) {
		if req.Node != "interviewer" {
			content := FakeReplies[req.Node]
			if onDelta != nil {
				_ = onDelta(content)
			}
			return llm.Response{Content: content}, nil
		}
		if req.OnRetry != nil {
			req.OnRetry(1, rawErr)
		}
		return llm.Response{}, rawErr
	}}
	manager, _ := newTestManager(t, st, client)
	if err := manager.Start(context.Background(), sess); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitIdle(t, manager, sess.ID)

	failures := nodeFailedPayloads(t, st, sess.ID, "interviewer")
	if len(failures) != 2 {
		t.Fatalf("interviewer node.failed events = %d, want 2 (transport retry and final failure)", len(failures))
	}
	var sawRetry, sawFinal bool
	for _, failure := range failures {
		if failure.Error != "llm_unavailable" {
			t.Errorf("interviewer node.failed error = %q, want llm_unavailable", failure.Error)
		}
		if failure.WillRetry {
			sawRetry = true
		} else {
			sawFinal = true
		}
	}
	if !sawRetry || !sawFinal {
		t.Errorf("interviewer node.failed will_retry = retry:%v final:%v, want both", sawRetry, sawFinal)
	}
	assertNoLeakedTransportDetail(t, st, sess.ID)
}

func TestLLMNodeFailedDeadlineUsesTimeoutCode(t *testing.T) {
	runner, st, ctx := newRunner(t)
	runner.llm = &stubClient{chat: func(req llm.Request) (llm.Response, error) {
		return llm.Response{}, fmt.Errorf("Post %q: %w", "https://internal.example/v1/chat", context.DeadlineExceeded)
	}}

	if _, err := runner.RunLLMNode(ctx, "planner", 1, llm.Request{
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "plan"}},
	}, "", nil); err == nil {
		t.Fatal("RunLLMNode succeeded, want a deadline error")
	}

	events, err := st.ListNodeEvents(ctx, runner.RunID(), 0)
	if err != nil {
		t.Fatalf("ListNodeEvents: %v", err)
	}
	if len(events) != 2 || events[1].Type != EventNodeFailed {
		t.Fatalf("event types = %v, want [node.started node.failed]", eventTypes(t, st, runner.RunID()))
	}
	var failed NodeFailed
	if err := json.Unmarshal(events[1].Payload, &failed); err != nil {
		t.Fatalf("unmarshal node.failed: %v", err)
	}
	if failed.Error != "timeout" {
		t.Errorf("node.failed error = %q, want timeout", failed.Error)
	}
}

func TestLLMNodeFailedNonRetryableUsesLLMErrorAndDoesNotLeak(t *testing.T) {
	runner, st, ctx := newRunner(t)
	runner.llm = &stubClient{chat: func(req llm.Request) (llm.Response, error) {
		return llm.Response{}, fmt.Errorf("status 400: %s", leakedTransportDetail)
	}}

	if _, err := runner.RunLLMNode(ctx, "planner", 1, llm.Request{
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "plan"}},
	}, "", nil); err == nil {
		t.Fatal("RunLLMNode succeeded, want a non-retryable error")
	}

	events, err := st.ListNodeEvents(ctx, runner.RunID(), 0)
	if err != nil {
		t.Fatalf("ListNodeEvents: %v", err)
	}
	if len(events) != 2 || events[1].Type != EventNodeFailed {
		t.Fatalf("event types = %v, want [node.started node.failed]", eventTypes(t, st, runner.RunID()))
	}
	var failed NodeFailed
	if err := json.Unmarshal(events[1].Payload, &failed); err != nil {
		t.Fatalf("unmarshal node.failed: %v", err)
	}
	if failed.Error != "llm_error" {
		t.Errorf("node.failed error = %q, want llm_error", failed.Error)
	}
	for _, e := range events {
		for _, fragment := range leakedTransportFragments {
			if strings.Contains(string(e.Payload), fragment) {
				t.Errorf("%s event payload leaks transport detail %q: %s", e.Type, fragment, e.Payload)
			}
		}
	}
}

func TestInterviewerEmptyReplyUsesEmptyReplyCode(t *testing.T) {
	st, sess := seedGraphSession(t)
	client := &stubClient{stream: func(req llm.Request, onDelta func(string) error) (llm.Response, error) {
		if req.Node != "interviewer" {
			content := FakeReplies[req.Node]
			if onDelta != nil {
				_ = onDelta(content)
			}
			return llm.Response{Content: content}, nil
		}
		return llm.Response{Content: "   "}, nil
	}}
	manager, _ := newTestManager(t, st, client)
	if err := manager.Start(context.Background(), sess); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitIdle(t, manager, sess.ID)

	failures := nodeFailedPayloads(t, st, sess.ID, "interviewer")
	if len(failures) != 1 {
		t.Fatalf("interviewer node.failed events = %d, want 1", len(failures))
	}
	if failures[0].Error != "empty_reply" {
		t.Errorf("interviewer node.failed error = %q, want empty_reply", failures[0].Error)
	}
}

func TestNodeFailedCodesAreClosedList(t *testing.T) {
	valid := map[string]bool{"timeout": true, "llm_unavailable": true, "empty_reply": true, "llm_error": true}
	for name, err := range map[string]error{
		"deadline":    fmt.Errorf("wrapped: %w", context.DeadlineExceeded),
		"unavailable": fmt.Errorf("wrapped: %w", llm.ErrUnavailable),
		"retryable":   &llm.RetryableError{Err: fmt.Errorf("upstream status 503")},
		"empty_reply": errEmptyReply,
		"other":       fmt.Errorf("status 400: %s", leakedTransportDetail),
		"nil-ish":     fmt.Errorf("some other failure"),
	} {
		code := nodeFailedCode(err)
		if !valid[code] {
			t.Errorf("nodeFailedCode(%s) = %q, not in the closed list", name, code)
		}
	}
	if got := nodeFailedCode(context.DeadlineExceeded); got != "timeout" {
		t.Errorf("nodeFailedCode(deadline) = %q, want timeout", got)
	}
	if got := nodeFailedCode(llm.ErrUnavailable); got != "llm_unavailable" {
		t.Errorf("nodeFailedCode(ErrUnavailable) = %q, want llm_unavailable", got)
	}
	if got := nodeFailedCode(&llm.RetryableError{Err: fmt.Errorf("x")}); got != "llm_unavailable" {
		t.Errorf("nodeFailedCode(RetryableError) = %q, want llm_unavailable", got)
	}
	if got := nodeFailedCode(errEmptyReply); got != "empty_reply" {
		t.Errorf("nodeFailedCode(errEmptyReply) = %q, want empty_reply", got)
	}
	if got := nodeFailedCode(fmt.Errorf("status 400: %s", leakedTransportDetail)); got != "llm_error" {
		t.Errorf("nodeFailedCode(non-retryable) = %q, want llm_error", got)
	}
}
