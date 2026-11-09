package graph

import (
	"time"

	"github.com/aangwijaya/talent-scout-server/internal/domain"
)

// Guard limits (ARCHITECTURE §3).
const (
	maxFollowUpsPerTopic = 2
	defaultMaxQuestions  = 15
	defaultMaxMinutes    = 15
)

// GuardAction is what the interview loop does after an answer.
type GuardAction string

const (
	GuardFollowUp  GuardAction = "follow_up"
	GuardNextTopic GuardAction = "next_topic"
	GuardFinish    GuardAction = "finish"
)

// GuardInput is the deterministic guard's input: the judge verdict plus the
// session counters/timers and how many topics are still pending. It never calls
// the LLM.
type GuardInput struct {
	Verdict         JudgeVerdict
	FollowUpCount   int
	QuestionCount   int
	StartedAt       *time.Time
	Now             time.Time
	RemainingTopics int
	MaxFollowUps    int
	MaxQuestions    int
	MaxMinutes      int
}

// GuardDecision is the guard's output; FinishReason is set only for finish.
type GuardDecision struct {
	Action       GuardAction
	FinishReason domain.FinishReason
}

// DecideGuard applies the loop limits: max 2 follow-ups per topic, max 15
// questions and max 15 minutes per session (configurable). A follow-up is
// dropped when the remaining question/time budget could no longer cover every
// remaining topic, so a late topic is not lost to follow-up questions.
func DecideGuard(in GuardInput) GuardDecision {
	maxFollowUps := in.MaxFollowUps
	if maxFollowUps <= 0 {
		maxFollowUps = maxFollowUpsPerTopic
	}
	maxQuestions := in.MaxQuestions
	if maxQuestions <= 0 {
		maxQuestions = defaultMaxQuestions
	}
	maxMinutes := in.MaxMinutes
	if maxMinutes <= 0 {
		maxMinutes = defaultMaxMinutes
	}

	if in.StartedAt != nil && !in.Now.Before(in.StartedAt.Add(time.Duration(maxMinutes)*time.Minute)) {
		return GuardDecision{Action: GuardFinish, FinishReason: domain.FinishReasonTimeLimit}
	}
	if in.QuestionCount >= maxQuestions {
		return GuardDecision{Action: GuardFinish, FinishReason: domain.FinishReasonQuestionLimit}
	}

	remaining := in.RemainingTopics
	if remaining < 0 {
		remaining = 0
	}

	if in.Verdict.Verdict == JudgeVerdictFollowUp && in.FollowUpCount < maxFollowUps {
		if guardCanFit(in, remaining+1, maxQuestions, maxMinutes) {
			return GuardDecision{Action: GuardFollowUp}
		}
	}

	if remaining > 0 {
		return GuardDecision{Action: GuardNextTopic}
	}
	return GuardDecision{Action: GuardFinish, FinishReason: domain.FinishReasonCompleted}
}

// guardCanFit reports whether slots more questions still fit the remaining
// question budget and the observed pace (elapsed / questions asked). The pace
// estimate keeps the rule deterministic without a per-question constant.
func guardCanFit(in GuardInput, slots, maxQuestions, maxMinutes int) bool {
	if in.QuestionCount+slots > maxQuestions {
		return false
	}
	if in.StartedAt == nil {
		return true
	}
	deadline := in.StartedAt.Add(time.Duration(maxMinutes) * time.Minute)
	if !in.Now.Before(deadline) {
		return false
	}
	elapsed := in.Now.Sub(*in.StartedAt)
	if elapsed <= 0 || in.QuestionCount <= 0 {
		return true
	}
	perQuestion := elapsed / time.Duration(in.QuestionCount)
	return time.Duration(slots)*perQuestion <= deadline.Sub(in.Now)
}
