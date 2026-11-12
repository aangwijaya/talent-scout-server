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

// The raw transport error is not applicant feedback: its fragments must never
// reach the stored loop.retry payload.
var leakedErrorFragments = []string{
	"upstream 500",
	"https://internal.example",
	"secret-provider-body",
}

func outageError() error {
	return fmt.Errorf("%w: upstream 500 https://internal.example/v1/chat secret-provider-body", llm.ErrUnavailable)
}

func assertManualRetryFeedbackEmpty(t *testing.T, st store.Store, sessionID string) {
	t.Helper()
	found := false
	for _, e := range sessionEvents(t, st, sessionID) {
		if e.Type != EventLoopRetry {
			continue
		}
		var payload LoopRetry
		if err := json.Unmarshal(e.Payload, &payload); err != nil {
			t.Fatalf("unmarshal loop.retry: %v", err)
		}
		if payload.Reason != "llm_unavailable" {
			continue
		}
		found = true
		if payload.Feedback != "" {
			t.Errorf("loop.retry feedback = %q, want empty", payload.Feedback)
		}
		for _, fragment := range leakedErrorFragments {
			if strings.Contains(payload.Feedback, fragment) {
				t.Errorf("loop.retry feedback leaks transport detail %q: %q", fragment, payload.Feedback)
			}
		}
	}
	if !found {
		t.Fatal("no llm_unavailable loop.retry event")
	}
}

func TestPlannerManualRetryFeedbackIsEmpty(t *testing.T) {
	st, sess := seedGraphSession(t)
	var plannerCalls int
	client := &stubClient{chat: func(req llm.Request) (llm.Response, error) {
		if req.Node != "planner" {
			return llm.Response{Content: FakeReplies[req.Node]}, nil
		}
		plannerCalls++
		if plannerCalls == 1 {
			return llm.Response{}, outageError()
		}
		return llm.Response{Content: FakeReplies["planner"]}, nil
	}}
	manager, _ := newTestManager(t, st, client)
	if err := manager.Start(context.Background(), sess); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitIdle(t, manager, sess.ID)
	if err := manager.Retry(context.Background(), sess); err != nil {
		t.Fatalf("Retry: %v", err)
	}
	waitIdle(t, manager, sess.ID)

	assertManualRetryFeedbackEmpty(t, st, sess.ID)
}

func TestInterviewerManualRetryFeedbackIsEmpty(t *testing.T) {
	st, sess := seedGraphSession(t)
	var interviewerCalls int
	client := &stubClient{stream: func(req llm.Request, onDelta func(string) error) (llm.Response, error) {
		if req.Node != "interviewer" {
			content := FakeReplies[req.Node]
			if onDelta != nil {
				_ = onDelta(content)
			}
			return llm.Response{Content: content}, nil
		}
		interviewerCalls++
		if interviewerCalls == 1 {
			return llm.Response{}, outageError()
		}
		content := FakeReplies["interviewer"]
		if onDelta != nil {
			_ = onDelta(content)
		}
		return llm.Response{Content: content}, nil
	}}
	manager, _ := newTestManager(t, st, client)
	if err := manager.Start(context.Background(), sess); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitIdle(t, manager, sess.ID)
	if err := manager.Retry(context.Background(), sess); err != nil {
		t.Fatalf("Retry: %v", err)
	}
	waitIdle(t, manager, sess.ID)

	assertManualRetryFeedbackEmpty(t, st, sess.ID)
}
