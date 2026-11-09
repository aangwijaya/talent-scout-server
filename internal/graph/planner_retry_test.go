package graph

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/aangwijaya/talent-scout-server/internal/domain"
	"github.com/aangwijaya/talent-scout-server/internal/llm"
)

func TestPlannerOutageIsApplicantRetryable(t *testing.T) {
	st, sess := seedGraphSession(t)
	var plannerCalls int
	client := &stubClient{chat: func(req llm.Request) (llm.Response, error) {
		if req.Node == "planner" {
			plannerCalls++
			if plannerCalls == 1 {
				return llm.Response{}, llm.ErrUnavailable
			}
		}
		return llm.Response{Content: FakeReplies[req.Node]}, nil
	}}
	manager, _ := newTestManager(t, st, client)
	if err := manager.Start(context.Background(), sess); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitIdle(t, manager, sess.ID)

	if !hasRetryableError(t, st, sess.ID) {
		t.Fatal("planner outage did not send a retryable error")
	}
	if hasSessionEnded(t, st, sess.ID) {
		t.Fatal("session ended instead of waiting for POST /retry")
	}
	fresh, _ := st.GetSession(context.Background(), sess.ID)
	if fresh.Status != domain.SessionStatusActive {
		t.Errorf("session status = %q, want active", fresh.Status)
	}

	var sawFixedMessage bool
	var plannerFailedWithRetry bool
	for _, e := range sessionEvents(t, st, sess.ID) {
		switch e.Type {
		case EventError:
			var payload ErrorEvent
			if err := json.Unmarshal(e.Payload, &payload); err != nil {
				t.Fatalf("unmarshal error event: %v", err)
			}
			sawFixedMessage = payload.Retryable && payload.Code == "llm_unavailable" && payload.Message == interviewerUnavailableMessage
		case EventNodeFailed:
			if e.NodeID != nil && *e.NodeID == "planner" {
				var payload NodeFailed
				if err := json.Unmarshal(e.Payload, &payload); err != nil {
					t.Fatalf("unmarshal node.failed: %v", err)
				}
				plannerFailedWithRetry = payload.WillRetry
			}
		}
	}
	if !sawFixedMessage {
		t.Error("retryable error did not use the fixed applicant-facing message")
	}
	if plannerFailedWithRetry {
		t.Error("planner node.failed has will_retry=true, want false before a manual retry")
	}

	if err := manager.Retry(context.Background(), sess); err != nil {
		t.Fatalf("Retry after planner outage: %v", err)
	}
	waitIdle(t, manager, sess.ID)

	if plannerCalls != 2 {
		t.Errorf("planner calls = %d, want 2", plannerCalls)
	}
	topics, _ := st.ListTopicsBySession(context.Background(), sess.ID)
	if len(topics) != 3 {
		t.Errorf("topics = %d, want 3", len(topics))
	}
	messages, _ := st.ListMessagesBySession(context.Background(), sess.ID, false)
	if len(messages) != 1 || messages[0].Role != domain.MessageRoleAssistant || messages[0].Kind != domain.MessageKindOpening {
		t.Errorf("messages = %+v, want one opening assistant message", messages)
	}
	fresh, _ = st.GetSession(context.Background(), sess.ID)
	if fresh.Status != domain.SessionStatusActive {
		t.Errorf("session status = %q, want active", fresh.Status)
	}
}

func TestPlannerOutageEmitsLoopRetryAndRetryEdge(t *testing.T) {
	st, sess := seedGraphSession(t)
	var plannerCalls int
	client := &stubClient{chat: func(req llm.Request) (llm.Response, error) {
		if req.Node == "planner" {
			plannerCalls++
			if plannerCalls == 1 {
				return llm.Response{}, llm.ErrUnavailable
			}
		}
		return llm.Response{Content: FakeReplies[req.Node]}, nil
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

	events := sessionEvents(t, st, sess.ID)
	retryIndex := -1
	for i, e := range events {
		if e.Type == EventLoopRetry {
			retryIndex = i
		}
	}
	if retryIndex < 0 {
		t.Fatal("no loop.retry event")
	}
	var retry LoopRetry
	if err := json.Unmarshal(events[retryIndex].Payload, &retry); err != nil {
		t.Fatalf("unmarshal loop.retry: %v", err)
	}
	wantMax := 2 + testModels().MaxRetries()
	if retry.Attempt != 2 || retry.MaxAttempts != wantMax || retry.Reason != "llm_unavailable" {
		t.Errorf("loop.retry = %+v, want attempt 2 / max %d / reason llm_unavailable", retry, wantMax)
	}
	if retry.MaxAttempts == retry.Attempt {
		t.Errorf("loop.retry max_attempts = attempt (%d), want the step budget", retry.Attempt)
	}
	if events[retryIndex].NodeID == nil || *events[retryIndex].NodeID != "planner" {
		t.Errorf("loop.retry node_id = %v, want planner", events[retryIndex].NodeID)
	}

	if retryIndex+1 >= len(events) || events[retryIndex+1].Type != EventEdgeTraversed {
		t.Fatalf("event after loop.retry = %v, want edge.traversed", eventTypeAt(events, retryIndex+1))
	}
	var edge EdgeTraversed
	if err := json.Unmarshal(events[retryIndex+1].Payload, &edge); err != nil {
		t.Fatalf("unmarshal edge.traversed: %v", err)
	}
	if edge.From != "planner" || edge.To != "planner" || edge.Label != "retry" {
		t.Errorf("retry edge = %+v, want planner→planner (retry)", edge)
	}

	if retryIndex+2 >= len(events) || events[retryIndex+2].Type != EventNodeStarted {
		t.Fatalf("event after retry edge = %v, want node.started", eventTypeAt(events, retryIndex+2))
	}
	if events[retryIndex+2].NodeID == nil || *events[retryIndex+2].NodeID != "planner" {
		t.Errorf("node.started node_id = %v, want planner", events[retryIndex+2].NodeID)
	}
	if events[retryIndex+2].Attempt == nil || *events[retryIndex+2].Attempt != 2 {
		t.Errorf("node.started attempt = %v, want 2", events[retryIndex+2].Attempt)
	}
}

func TestPlannerOutageTwiceEndsSession(t *testing.T) {
	st, sess := seedGraphSession(t)
	client := &stubClient{chat: func(req llm.Request) (llm.Response, error) {
		if req.Node == "planner" {
			return llm.Response{}, llm.ErrUnavailable
		}
		return llm.Response{Content: FakeReplies[req.Node]}, nil
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

	var ended *SessionEnded
	var runFailed string
	for _, e := range sessionEvents(t, st, sess.ID) {
		switch e.Type {
		case EventSessionEnded:
			var payload SessionEnded
			_ = json.Unmarshal(e.Payload, &payload)
			ended = &payload
		case EventRunFailed:
			var payload RunFailed
			_ = json.Unmarshal(e.Payload, &payload)
			runFailed = payload.Error
		}
	}
	if runFailed != "planner_failed" {
		t.Errorf("run.failed = %q, want planner_failed", runFailed)
	}
	if ended == nil || ended.FinishReason != string(domain.FinishReasonFailed) {
		t.Fatalf("session.ended = %+v, want finish_reason failed", ended)
	}
	fresh, _ := st.GetSession(context.Background(), sess.ID)
	if fresh.Status != domain.SessionStatusCompleted || fresh.FinishReason == nil || *fresh.FinishReason != domain.FinishReasonFailed {
		t.Errorf("session = %+v, want completed/failed", fresh)
	}
	app, _ := st.GetApplication(context.Background(), sess.ApplicationID)
	if app.Status != domain.ApplicationStatusFailed {
		t.Errorf("application status = %q, want failed", app.Status)
	}
	if err := manager.Retry(context.Background(), sess); !errors.Is(err, ErrInvalidState) {
		t.Errorf("Retry after permanent failure = %v, want ErrInvalidState", err)
	}
}

func TestManualRetryContinuesPlannerAttempts(t *testing.T) {
	st, sess := seedGraphSession(t)
	var plannerCalls int
	client := &stubClient{chat: func(req llm.Request) (llm.Response, error) {
		if req.Node != "planner" {
			return llm.Response{Content: FakeReplies[req.Node]}, nil
		}
		plannerCalls++
		if plannerCalls == 1 {
			// Simulate one transport retry that also fails: the client invokes
			// OnRetry before its second HTTP attempt.
			if req.OnRetry != nil {
				req.OnRetry(1, llm.ErrUnavailable)
			}
			return llm.Response{}, llm.ErrUnavailable
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

	var attempts []int
	var retryAttempt int
	for _, event := range sessionEvents(t, st, sess.ID) {
		switch {
		case event.Type == EventNodeStarted && event.NodeID != nil && *event.NodeID == "planner" && event.Attempt != nil:
			attempts = append(attempts, *event.Attempt)
		case event.Type == EventLoopRetry && event.Attempt != nil:
			retryAttempt = *event.Attempt
		}
	}
	if want := []int{1, 2, 3}; !slices.Equal(attempts, want) {
		t.Errorf("planner node.started attempts = %v, want %v", attempts, want)
	}
	if retryAttempt != 3 {
		t.Errorf("loop.retry attempt = %d, want 3", retryAttempt)
	}
}

// An outage on the first attempt must not consume the plan_check budget: the
// applicant retry runs plan_check once, and an invalid plan there still gets
// the automatic second plan_check attempt.
func TestApplicantRetryAfterOutageKeepsPlanCheckBudget(t *testing.T) {
	st, sess := seedGraphSession(t)
	var plannerCalls int
	client := &stubClient{chat: func(req llm.Request) (llm.Response, error) {
		if req.Node != "planner" {
			return llm.Response{Content: FakeReplies[req.Node]}, nil
		}
		plannerCalls++
		switch plannerCalls {
		case 1:
			return llm.Response{}, llm.ErrUnavailable
		case 2:
			return llm.Response{Content: `{"topics":[]}`}, nil
		default:
			return llm.Response{Content: FakeReplies["planner"]}, nil
		}
	}}
	manager, _ := newTestManager(t, st, client)
	if err := manager.Start(context.Background(), sess); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitIdle(t, manager, sess.ID)
	if !hasRetryableError(t, st, sess.ID) {
		t.Fatal("planner outage did not send a retryable error")
	}

	if err := manager.Retry(context.Background(), sess); err != nil {
		t.Fatalf("Retry: %v", err)
	}
	waitIdle(t, manager, sess.ID)

	if plannerCalls != 3 {
		t.Errorf("planner calls = %d, want 3 (outage, applicant retry, automatic plan_check retry)", plannerCalls)
	}
	fresh, _ := st.GetSession(context.Background(), sess.ID)
	if fresh.Status != domain.SessionStatusActive {
		t.Errorf("session status = %q, want active", fresh.Status)
	}
	topics, _ := st.ListTopicsBySession(context.Background(), sess.ID)
	if len(topics) != 3 {
		t.Errorf("topics = %d, want 3", len(topics))
	}
	messages, _ := st.ListMessagesBySession(context.Background(), sess.ID, false)
	if len(messages) != 1 || messages[0].Kind != domain.MessageKindOpening {
		t.Errorf("messages = %+v, want one opening message", messages)
	}

	var sawAutomaticRetry bool
	for _, e := range sessionEvents(t, st, sess.ID) {
		if e.Type != EventLoopRetry {
			continue
		}
		var payload LoopRetry
		if err := json.Unmarshal(e.Payload, &payload); err != nil {
			t.Fatalf("unmarshal loop.retry: %v", err)
		}
		if payload.Reason == "invalid_plan" && payload.Attempt == 3 {
			sawAutomaticRetry = true
		}
	}
	if !sawAutomaticRetry {
		t.Error("no automatic plan_check loop.retry at attempt 3")
	}
}

// A plan_check rejection followed by an outage on the automatic retry must keep
// the rejection feedback so the applicant's planner retry sees it.
func TestApplicantRetryAfterOutageKeepsPlanCheckFeedback(t *testing.T) {
	st, sess := seedGraphSession(t)
	var plannerRequests []llm.Request
	client := &stubClient{chat: func(req llm.Request) (llm.Response, error) {
		if req.Node != "planner" {
			return llm.Response{Content: FakeReplies[req.Node]}, nil
		}
		plannerRequests = append(plannerRequests, req)
		switch len(plannerRequests) {
		case 1:
			return llm.Response{Content: `{"topics":[]}`}, nil
		case 2:
			return llm.Response{}, llm.ErrUnavailable
		default:
			return llm.Response{Content: FakeReplies["planner"]}, nil
		}
	}}
	manager, _ := newTestManager(t, st, client)
	if err := manager.Start(context.Background(), sess); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitIdle(t, manager, sess.ID)
	if !hasRetryableError(t, st, sess.ID) {
		t.Fatal("planner outage after a plan_check failure did not send a retryable error")
	}
	if err := manager.Retry(context.Background(), sess); err != nil {
		t.Fatalf("Retry: %v", err)
	}
	waitIdle(t, manager, sess.ID)

	if len(plannerRequests) != 3 {
		t.Fatalf("planner calls = %d, want 3", len(plannerRequests))
	}
	var feedback strings.Builder
	for _, msg := range plannerRequests[2].Messages {
		feedback.WriteString(msg.Content)
		feedback.WriteString("\n")
	}
	if !strings.Contains(feedback.String(), "topic_count must be between 3 and 5") {
		t.Errorf("applicant retry planner request lacks attempt 1 plan_check errors:\n%s", feedback.String())
	}
}
