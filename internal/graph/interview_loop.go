package graph

import (
	"context"
	"fmt"
	"time"

	"github.com/aangwijaya/talent-scout-server/internal/domain"
	"github.com/aangwijaya/talent-scout-server/internal/llm"
)

// judgeAndGuard runs the answer gate: interviewer→judge→guard, then asks a
// follow-up, moves to the next topic, or finishes the interview. The order
// follows docs/EVENTS.md §6.1.
func (m *SessionManager) judgeAndGuard(ctx context.Context, sess domain.Session, st *liveSession, topic domain.Topic) {
	messages, err := m.store.ListMessagesBySession(ctx, sess.ID, false)
	if err != nil {
		m.failRun(ctx, sess, st, "load_transcript_failed")
		return
	}
	answer, ok := lastApplicantMessage(messages)
	if !ok {
		m.failRun(ctx, sess, st, "no_answer")
		return
	}
	question := questionBeforeAnswer(messages, answer.ID)

	if err := st.runner.EmitEdge(ctx, "interviewer", "judge", "answer"); err != nil {
		m.failRun(ctx, sess, st, "edge_failed")
		return
	}
	verdict, _, err := m.runJudge(ctx, st, topic, question, answer.Content)
	if err != nil {
		m.failRun(ctx, sess, st, "judge_failed")
		return
	}

	if err := st.runner.EmitEdge(ctx, "judge", "guard", "verdict"); err != nil {
		m.failRun(ctx, sess, st, "edge_failed")
		return
	}
	decision, err := m.runGuard(ctx, sess, st, topic, verdict)
	if err != nil {
		m.failRun(ctx, sess, st, "guard_failed")
		return
	}

	switch decision.Action {
	case GuardFollowUp:
		topic.FollowUpCount++
		if err := m.store.UpdateTopic(ctx, topic); err != nil {
			m.failRun(ctx, sess, st, "update_topic_failed")
			return
		}
		if err := st.runner.EmitEdge(ctx, "guard", "interviewer", "follow_up"); err != nil {
			m.failRun(ctx, sess, st, "edge_failed")
			return
		}
		m.runInterviewer(ctx, sess, st, topic, domain.MessageKindFollowUp, 1, &verdict)
	case GuardNextTopic:
		next, ok, err := m.activateNextTopic(ctx, topic)
		if err != nil {
			m.failRun(ctx, sess, st, "activate_topic_failed")
			return
		}
		if !ok {
			if err := st.runner.EmitEdge(ctx, "guard", "scorer", "finish"); err != nil {
				m.failRun(ctx, sess, st, "edge_failed")
				return
			}
			m.finishInterview(ctx, sess, st, domain.FinishReasonCompleted)
			return
		}
		if err := st.runner.EmitTopicChanged(ctx, "guard", 1, next.ID, next.Position, next.Title, string(next.Status)); err != nil {
			m.failRun(ctx, sess, st, "topic_changed_failed")
			return
		}
		fresh, _ := m.store.GetSession(ctx, sess.ID)
		m.emitSessionUpdated(ctx, st, fresh, &next)
		if err := st.runner.EmitEdge(ctx, "guard", "interviewer", "next_topic"); err != nil {
			m.failRun(ctx, sess, st, "edge_failed")
			return
		}
		m.runInterviewer(ctx, sess, st, next, domain.MessageKindQuestion, 1, nil)
	default:
		if err := st.runner.EmitEdge(ctx, "guard", "scorer", "finish"); err != nil {
			m.failRun(ctx, sess, st, "edge_failed")
			return
		}
		m.finishInterview(ctx, sess, st, decision.FinishReason)
	}
}

// runJudge runs one judge attempt. An invalid reply still completes the node
// (with the default verdict recorded in node_events) and is never retried. A
// transport failure is a permanent node failure per docs/EVENTS.md §6.3, so the
// caller ends the run; only invalid JSON gets the sufficient fallback.
func (m *SessionManager) runJudge(ctx context.Context, st *liveSession, topic domain.Topic, question, answer string) (JudgeVerdict, int, error) {
	st.executed["judge"] = true
	var verdict JudgeVerdict
	_, attempt, err := st.runner.runLLMNodeWithSummary(ctx, "judge", 1, llm.Request{
		Messages:       judgeMessages(topic, question, answer),
		ResponseFormat: judgeResponseFormat(),
	}, fmt.Sprintf("topic %d: %s", topic.Position, topic.Title), nil, func(resp llm.Response) string {
		parsed, invalid := parseJudgeVerdict(resp.Content)
		verdict = parsed
		if invalid {
			return "verdict=" + JudgeVerdictSufficient + " (invalid_json)"
		}
		return "verdict=" + parsed.Verdict
	})
	if err != nil {
		return JudgeVerdict{}, attempt, err
	}
	return verdict, attempt, nil
}

// runGuard reads the counters and lets the deterministic guard decide. It never
// calls the LLM and never emits loop.retry.
func (m *SessionManager) runGuard(ctx context.Context, sess domain.Session, st *liveSession, topic domain.Topic, verdict JudgeVerdict) (GuardDecision, error) {
	st.executed["guard"] = true
	var decision GuardDecision
	err := st.runner.RunCodeNode(ctx, "guard", 1, "verdict="+verdict.Verdict, func() (string, error) {
		fresh, err := m.store.GetSession(ctx, sess.ID)
		if err != nil {
			return "", err
		}
		topics, err := m.store.ListTopicsBySession(ctx, sess.ID)
		if err != nil {
			return "", err
		}
		remaining := 0
		for _, candidate := range topics {
			if candidate.Status == domain.TopicStatusPending {
				remaining++
			}
		}
		decision = DecideGuard(GuardInput{
			Verdict:         verdict,
			FollowUpCount:   topic.FollowUpCount,
			QuestionCount:   fresh.QuestionCount,
			StartedAt:       fresh.StartedAt,
			Now:             time.Now().UTC(),
			RemainingTopics: remaining,
			MaxFollowUps:    maxFollowUpsPerTopic,
			MaxQuestions:    m.cfg.SessionMaxQuestions,
			MaxMinutes:      m.cfg.SessionMaxMinutes,
		})
		return string(decision.Action), nil
	})
	if err != nil {
		return GuardDecision{}, err
	}
	return decision, nil
}

// activateNextTopic marks current done and activates the next pending topic. It
// reports ok=false with a nil error when no topic is left; a store error is
// returned separately so the caller can fail the run instead of pretending the
// interview finished cleanly.
func (m *SessionManager) activateNextTopic(ctx context.Context, current domain.Topic) (domain.Topic, bool, error) {
	topics, err := m.store.ListTopicsBySession(ctx, current.SessionID)
	if err != nil {
		return domain.Topic{}, false, err
	}
	var next *domain.Topic
	for i := range topics {
		if topics[i].Status != domain.TopicStatusPending {
			continue
		}
		if topics[i].Position <= current.Position {
			continue
		}
		if next == nil || topics[i].Position < next.Position {
			next = &topics[i]
		}
	}
	if next == nil {
		for i := range topics {
			if topics[i].Status != domain.TopicStatusPending {
				continue
			}
			if next == nil || topics[i].Position < next.Position {
				next = &topics[i]
			}
		}
	}
	if next == nil {
		return domain.Topic{}, false, nil
	}

	current.Status = domain.TopicStatusDone
	if err := m.store.UpdateTopic(ctx, current); err != nil {
		return domain.Topic{}, false, err
	}
	next.Status = domain.TopicStatusActive
	if err := m.store.UpdateTopic(ctx, *next); err != nil {
		return domain.Topic{}, false, err
	}
	return *next, true, nil
}

// finishInterview ends the interview loop. Scoring (server#11) continues the run
// from the guard→scorer edge, so no run.completed is emitted here.
func (m *SessionManager) finishInterview(ctx context.Context, sess domain.Session, st *liveSession, reason domain.FinishReason) {
	if topic, err := m.activeTopic(ctx, sess.ID); err == nil && topic != nil {
		topic.Status = domain.TopicStatusDone
		_ = m.store.UpdateTopic(ctx, *topic)
	}

	now := time.Now().UTC()
	changed, err := m.store.FinishSession(ctx, sess.ID, reason, now)
	if err != nil {
		m.failRun(ctx, sess, st, "finish_session_failed")
		return
	}
	if changed {
		if app, err := m.store.GetApplication(ctx, sess.ApplicationID); err == nil && app.Status == domain.ApplicationStatusInSession {
			_ = m.store.UpdateApplicationStatus(ctx, app.ID, domain.ApplicationStatusScoring)
		}
		_, _ = m.bus.Emit(ctx, st.runID, Event{Type: EventSessionEnded, Payload: SessionEnded{FinishReason: string(reason)}})
	}

	m.registry.Remove(sess.ID)
	m.mu.Lock()
	st.done = true
	st.busy = false
	st.waitingRetry = false
	st.retry = nil
	m.mu.Unlock()
}

func lastApplicantMessage(messages []domain.Message) (domain.Message, bool) {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == domain.MessageRoleApplicant {
			return messages[i], true
		}
	}
	return domain.Message{}, false
}

// questionBeforeAnswer returns the assistant message that the applicant
// answered. It is interviewer output, so prompts must treat it as untrusted.
func questionBeforeAnswer(messages []domain.Message, answerID string) string {
	seenAnswer := false
	for i := len(messages) - 1; i >= 0; i-- {
		message := messages[i]
		if !seenAnswer {
			if message.ID == answerID {
				seenAnswer = true
			}
			continue
		}
		if message.Role == domain.MessageRoleAssistant {
			return message.Content
		}
	}
	return ""
}
