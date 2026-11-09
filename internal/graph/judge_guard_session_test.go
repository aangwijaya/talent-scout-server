package graph

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/aangwijaya/talent-scout-server/internal/config"
	"github.com/aangwijaya/talent-scout-server/internal/domain"
	"github.com/aangwijaya/talent-scout-server/internal/llm"
	"github.com/aangwijaya/talent-scout-server/internal/session"
)

func filteredRunEvents(events []domain.NodeEvent) []domain.NodeEvent {
	out := make([]domain.NodeEvent, 0, len(events))
	for _, event := range events {
		switch {
		case strings.HasPrefix(event.Type, "session."):
			continue
		case event.Type == EventMessageDelta:
			continue
		default:
			out = append(out, event)
		}
	}
	return out
}

func applicantAnswerEventIndex(t *testing.T, events []domain.NodeEvent) int {
	t.Helper()
	filtered := filteredRunEvents(events)
	for i, event := range filtered {
		if event.Type != EventMessageCompleted {
			continue
		}
		var payload MessageCompleted
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			t.Fatalf("unmarshal message.completed: %v", err)
		}
		if payload.Message.Role == domain.MessageRoleApplicant {
			return i
		}
	}
	t.Fatal("no applicant message.completed event")
	return -1
}

func nodeCompletedPayload(t *testing.T, events []domain.NodeEvent, nodeID string) NodeCompleted {
	t.Helper()
	var last NodeCompleted
	found := false
	for _, event := range events {
		if event.Type != EventNodeCompleted || event.NodeID == nil || *event.NodeID != nodeID {
			continue
		}
		var payload NodeCompleted
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			t.Fatalf("unmarshal node.completed(%s): %v", nodeID, err)
		}
		last = payload
		found = true
	}
	if !found {
		t.Fatalf("no node.completed for %s", nodeID)
	}
	return last
}

func lastMessageKind(t *testing.T, st interface {
	ListMessagesBySession(context.Context, string, bool) ([]domain.Message, error)
}, sessionID string) domain.MessageKind {
	t.Helper()
	messages, err := st.ListMessagesBySession(context.Background(), sessionID, false)
	if err != nil {
		t.Fatalf("ListMessagesBySession: %v", err)
	}
	if len(messages) == 0 {
		t.Fatal("no messages")
	}
	return messages[len(messages)-1].Kind
}

func TestJudgeGuardEventSequenceForFollowUp(t *testing.T) {
	st, sess := seedGraphSession(t)
	manager, _ := newTestManager(t, st, &stubClient{})
	if err := manager.Start(context.Background(), sess); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitIdle(t, manager, sess.ID)
	if _, err := manager.SubmitAnswer(context.Background(), sess, "I measured first paint."); err != nil {
		t.Fatalf("SubmitAnswer: %v", err)
	}
	waitIdle(t, manager, sess.ID)

	events := sessionEvents(t, st, sess.ID)
	filtered := filteredRunEvents(events)
	idx := applicantAnswerEventIndex(t, events)
	wantTypes := []string{
		EventEdgeTraversed,
		EventNodeStarted, EventNodeCompleted,
		EventEdgeTraversed,
		EventNodeStarted, EventNodeCompleted,
		EventEdgeTraversed,
		EventNodeStarted, EventMessageCompleted, EventNodeCompleted,
	}
	if idx+len(wantTypes) > len(filtered) {
		t.Fatalf("events after answer = %v, want prefix %v", filtered[idx+1:], wantTypes)
	}
	for i, want := range wantTypes {
		if got := filtered[idx+1+i].Type; got != want {
			t.Errorf("event after answer[%d] = %q, want %q (events: %v)", i, got, want, graphEventTypes(events))
		}
	}

	wantNodes := []string{"", "judge", "judge", "", "guard", "guard", "", "interviewer", "interviewer", "interviewer"}
	for i, want := range wantNodes {
		event := filtered[idx+1+i]
		got := ""
		if event.NodeID != nil {
			got = *event.NodeID
		}
		if got != want {
			t.Errorf("event after answer[%d] node_id = %q, want %q", i, got, want)
		}
	}

	for _, tc := range []struct {
		index int
		from  string
		to    string
		label string
	}{
		{0, "interviewer", "judge", "answer"},
		{3, "judge", "guard", "verdict"},
		{6, "guard", "interviewer", "follow_up"},
	} {
		var edge EdgeTraversed
		if err := json.Unmarshal(filtered[idx+1+tc.index].Payload, &edge); err != nil {
			t.Fatalf("unmarshal edge: %v", err)
		}
		if edge.From != tc.from || edge.To != tc.to || edge.Label != tc.label {
			t.Errorf("edge = %+v, want %s→%s (%s)", edge, tc.from, tc.to, tc.label)
		}
	}

	for _, event := range filtered {
		if event.Type == EventLoopRetry && event.NodeID != nil && (*event.NodeID == "judge" || *event.NodeID == "guard") {
			t.Errorf("loop.retry emitted for %s; judge/guard must not retry", *event.NodeID)
		}
	}

	topics, _ := st.ListTopicsBySession(context.Background(), sess.ID)
	if topics[0].FollowUpCount != 1 {
		t.Errorf("topic follow_up_count = %d, want 1", topics[0].FollowUpCount)
	}
	if got := lastMessageKind(t, st, sess.ID); got != domain.MessageKindFollowUp {
		t.Errorf("last message kind = %q, want follow_up", got)
	}
}

func TestJudgeInvalidJSONDefaultsToSufficient(t *testing.T) {
	st, sess := seedGraphSession(t)
	client := &stubClient{chat: func(req llm.Request) (llm.Response, error) {
		if req.Node == "judge" {
			return llm.Response{Content: "not json at all"}, nil
		}
		return llm.Response{Content: FakeReplies[req.Node]}, nil
	}}
	manager, _ := newTestManager(t, st, client)
	if err := manager.Start(context.Background(), sess); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitIdle(t, manager, sess.ID)
	if _, err := manager.SubmitAnswer(context.Background(), sess, "An answer."); err != nil {
		t.Fatalf("SubmitAnswer: %v", err)
	}
	waitIdle(t, manager, sess.ID)

	events := sessionEvents(t, st, sess.ID)
	judge := nodeCompletedPayload(t, events, "judge")
	if !strings.Contains(judge.OutputSummary, "verdict=sufficient") || !strings.Contains(judge.OutputSummary, "invalid_json") {
		t.Errorf("judge node.completed summary = %q, want the default sufficient verdict recorded", judge.OutputSummary)
	}
	guard := nodeCompletedPayload(t, events, "guard")
	if guard.OutputSummary != string(GuardNextTopic) {
		t.Errorf("guard action after invalid judge JSON = %q, want %q", guard.OutputSummary, GuardNextTopic)
	}
	for _, event := range events {
		if event.Type == EventLoopRetry {
			t.Errorf("loop.retry emitted for judge/guard path: %+v", event)
		}
	}
}

func TestGuardDropsFollowUpWhenQuestionsCannotCoverTopics(t *testing.T) {
	st, sess := seedGraphSession(t)
	bus := NewBus(st)
	cfg := config.Config{SessionMaxMinutes: 15, SessionMaxQuestions: 3}
	manager := NewSessionManager(cfg, st, bus, &stubClient{}, testModels(), session.NewRegistry(), nil)

	if err := manager.Start(context.Background(), sess); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitIdle(t, manager, sess.ID)
	if _, err := manager.SubmitAnswer(context.Background(), sess, "Answer one."); err != nil {
		t.Fatalf("SubmitAnswer: %v", err)
	}
	waitIdle(t, manager, sess.ID)

	guard := nodeCompletedPayload(t, sessionEvents(t, st, sess.ID), "guard")
	if guard.OutputSummary != string(GuardNextTopic) {
		t.Errorf("guard action = %q, want %q: a follow-up would leave too few questions for the remaining topics", guard.OutputSummary, GuardNextTopic)
	}
	topics, _ := st.ListTopicsBySession(context.Background(), sess.ID)
	if topics[0].Status != domain.TopicStatusDone || topics[1].Status != domain.TopicStatusActive {
		t.Errorf("topics = %+v, want topic 1 done and topic 2 active", topics)
	}
	if topics[0].FollowUpCount != 0 {
		t.Errorf("topic 1 follow_up_count = %d, want 0 (follow-up was dropped)", topics[0].FollowUpCount)
	}
	if got := lastMessageKind(t, st, sess.ID); got != domain.MessageKindQuestion {
		t.Errorf("last message kind = %q, want question on the next topic", got)
	}
}

func TestGuardNextTopicEventOrder(t *testing.T) {
	st, sess := seedGraphSession(t)
	client := &stubClient{chat: func(req llm.Request) (llm.Response, error) {
		if req.Node == "judge" {
			return llm.Response{Content: `{"verdict":"sufficient","reason":"ok","missing":[]}`}, nil
		}
		return llm.Response{Content: FakeReplies[req.Node]}, nil
	}}
	manager, _ := newTestManager(t, st, client)
	if err := manager.Start(context.Background(), sess); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitIdle(t, manager, sess.ID)
	if _, err := manager.SubmitAnswer(context.Background(), sess, "A sufficient answer."); err != nil {
		t.Fatalf("SubmitAnswer: %v", err)
	}
	waitIdle(t, manager, sess.ID)

	events := sessionEvents(t, st, sess.ID)
	filtered := filteredRunEvents(events)
	idx := applicantAnswerEventIndex(t, events)
	// The guard transitions to the next topic: node.completed(guard) →
	// topic.changed(guard) → edge(guard→interviewer, next_topic) → interviewer.
	guardCompleted := -1
	for i := idx + 1; i < len(filtered); i++ {
		if filtered[i].Type == EventNodeCompleted && filtered[i].NodeID != nil && *filtered[i].NodeID == "guard" {
			guardCompleted = i
			break
		}
	}
	if guardCompleted < 0 {
		t.Fatal("no guard node.completed after the answer")
	}
	var completed NodeCompleted
	if err := json.Unmarshal(filtered[guardCompleted].Payload, &completed); err != nil {
		t.Fatalf("unmarshal guard node.completed: %v", err)
	}
	if completed.OutputSummary != string(GuardNextTopic) {
		t.Errorf("guard action = %q, want %q", completed.OutputSummary, GuardNextTopic)
	}
	if got := filtered[guardCompleted+1].Type; got != EventTopicChanged {
		t.Errorf("event after guard node.completed = %q, want topic.changed", got)
	}
	if filtered[guardCompleted+1].NodeID == nil || *filtered[guardCompleted+1].NodeID != "guard" {
		t.Errorf("topic.changed node_id = %v, want guard", filtered[guardCompleted+1].NodeID)
	}
	if got := filtered[guardCompleted+2].Type; got != EventEdgeTraversed {
		t.Errorf("event after topic.changed = %q, want edge.traversed", got)
	}
	var edge EdgeTraversed
	if err := json.Unmarshal(filtered[guardCompleted+2].Payload, &edge); err != nil {
		t.Fatalf("unmarshal edge: %v", err)
	}
	if edge.From != "guard" || edge.To != "interviewer" || edge.Label != "next_topic" {
		t.Errorf("edge = %+v, want guard→interviewer (next_topic)", edge)
	}
}

func TestGuardFinishesWhenAllTopicsAreDone(t *testing.T) {
	st, sess := seedGraphSession(t)
	client := &stubClient{chat: func(req llm.Request) (llm.Response, error) {
		if req.Node == "judge" {
			return llm.Response{Content: `{"verdict":"sufficient","reason":"ok","missing":[]}`}, nil
		}
		return llm.Response{Content: FakeReplies[req.Node]}, nil
	}}
	manager, _ := newTestManager(t, st, client)
	if err := manager.Start(context.Background(), sess); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitIdle(t, manager, sess.ID)

	topics, _ := st.ListTopicsBySession(context.Background(), sess.ID)
	for i := 0; i < len(topics); i++ {
		if _, err := manager.SubmitAnswer(context.Background(), sess, "A sufficient answer."); err != nil {
			t.Fatalf("SubmitAnswer %d: %v", i+1, err)
		}
		waitIdle(t, manager, sess.ID)
	}

	fresh, _ := st.GetSession(context.Background(), sess.ID)
	if fresh.Status != domain.SessionStatusCompleted || fresh.FinishReason == nil || *fresh.FinishReason != domain.FinishReasonCompleted {
		t.Errorf("session = %+v, want completed/completed", fresh)
	}
	app, _ := st.GetApplication(context.Background(), sess.ApplicationID)
	if app.Status != domain.ApplicationStatusReview {
		t.Errorf("application status = %q, want review after scoring", app.Status)
	}

	events := sessionEvents(t, st, sess.ID)
	guard := nodeCompletedPayload(t, events, "guard")
	if guard.OutputSummary != string(GuardFinish) {
		t.Errorf("last guard action = %q, want %q", guard.OutputSummary, GuardFinish)
	}
	var sawScorerEdge, sawEnded bool
	for _, event := range events {
		switch event.Type {
		case EventSessionEnded:
			sawEnded = true
		case EventEdgeTraversed:
			var edge EdgeTraversed
			if err := json.Unmarshal(event.Payload, &edge); err != nil {
				t.Fatalf("unmarshal edge: %v", err)
			}
			if edge.From == "guard" && edge.To == "scorer" && edge.Label == "finish" {
				sawScorerEdge = true
			}
		}
	}
	if !sawScorerEdge {
		t.Error("no edge.traversed(guard→scorer, finish)")
	}
	if !sawEnded {
		t.Error("no session.ended event")
	}
	topics, _ = st.ListTopicsBySession(context.Background(), sess.ID)
	for _, topic := range topics {
		if topic.Status != domain.TopicStatusDone {
			t.Errorf("topic %d status = %q, want done", topic.Position, topic.Status)
		}
	}
}

func TestGuardStopsAtTimeLimit(t *testing.T) {
	st, sess := seedGraphSession(t)
	client := &stubClient{chat: func(req llm.Request) (llm.Response, error) {
		if req.Node == "judge" {
			return llm.Response{Content: `{"verdict":"follow_up","reason":"more","missing":[]}`}, nil
		}
		return llm.Response{Content: FakeReplies[req.Node]}, nil
	}}
	manager, _ := newTestManager(t, st, client)
	if err := manager.Start(context.Background(), sess); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitIdle(t, manager, sess.ID)

	fresh, _ := st.GetSession(context.Background(), sess.ID)
	past := time.Now().Add(-16 * time.Minute)
	fresh.StartedAt = &past
	if err := st.UpdateSession(context.Background(), fresh); err != nil {
		t.Fatalf("UpdateSession: %v", err)
	}

	if _, err := manager.SubmitAnswer(context.Background(), sess, "An answer after the deadline."); err != nil {
		t.Fatalf("SubmitAnswer: %v", err)
	}
	waitIdle(t, manager, sess.ID)

	guard := nodeCompletedPayload(t, sessionEvents(t, st, sess.ID), "guard")
	if guard.OutputSummary != string(GuardFinish) {
		t.Errorf("guard action = %q, want %q", guard.OutputSummary, GuardFinish)
	}
	fresh, _ = st.GetSession(context.Background(), sess.ID)
	if fresh.Status != domain.SessionStatusCompleted || fresh.FinishReason == nil || *fresh.FinishReason != domain.FinishReasonTimeLimit {
		t.Errorf("session = %+v, want completed/time_limit", fresh)
	}
}
