package graph

import (
	"testing"
	"time"

	"github.com/aangwijaya/talent-scout-server/internal/domain"
)

func TestDecideGuard(t *testing.T) {
	start := time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC)
	at := func(min int) time.Time { return start.Add(time.Duration(min) * time.Minute) }
	sufficient := JudgeVerdict{Verdict: JudgeVerdictSufficient}
	followUp := JudgeVerdict{Verdict: JudgeVerdictFollowUp}

	cases := []struct {
		name         string
		verdict      JudgeVerdict
		followUps    int
		questions    int
		elapsedMin   int
		remaining    int
		startedAt    *time.Time
		maxQuestions int
		maxMinutes   int
		wantAction   GuardAction
		wantReason   domain.FinishReason
	}{
		{
			name: "follow up when budget allows", verdict: followUp, followUps: 0, questions: 1, elapsedMin: 1, remaining: 2,
			startedAt: &start, maxQuestions: 15, maxMinutes: 15, wantAction: GuardFollowUp,
		},
		{
			name: "second follow up allowed", verdict: followUp, followUps: 1, questions: 2, elapsedMin: 2, remaining: 2,
			startedAt: &start, maxQuestions: 15, maxMinutes: 15, wantAction: GuardFollowUp,
		},
		{
			name: "follow up limit reached moves to next topic", verdict: followUp, followUps: 2, questions: 3, elapsedMin: 3, remaining: 2,
			startedAt: &start, maxQuestions: 15, maxMinutes: 15, wantAction: GuardNextTopic,
		},
		{
			name: "sufficient answer moves to next topic", verdict: sufficient, followUps: 0, questions: 1, elapsedMin: 1, remaining: 2,
			startedAt: &start, maxQuestions: 15, maxMinutes: 15, wantAction: GuardNextTopic,
		},
		{
			name: "sufficient answer on the last topic finishes", verdict: sufficient, followUps: 0, questions: 3, elapsedMin: 3, remaining: 0,
			startedAt: &start, maxQuestions: 15, maxMinutes: 15, wantAction: GuardFinish, wantReason: domain.FinishReasonCompleted,
		},
		{
			name: "follow up on the last topic is allowed", verdict: followUp, followUps: 0, questions: 3, elapsedMin: 3, remaining: 0,
			startedAt: &start, maxQuestions: 15, maxMinutes: 15, wantAction: GuardFollowUp,
		},
		{
			name: "follow up limit on the last topic finishes", verdict: followUp, followUps: 2, questions: 3, elapsedMin: 3, remaining: 0,
			startedAt: &start, maxQuestions: 15, maxMinutes: 15, wantAction: GuardFinish, wantReason: domain.FinishReasonCompleted,
		},
		{
			name: "question limit wins when time remains", verdict: followUp, followUps: 0, questions: 15, elapsedMin: 5, remaining: 2,
			startedAt: &start, maxQuestions: 15, maxMinutes: 15, wantAction: GuardFinish, wantReason: domain.FinishReasonQuestionLimit,
		},
		{
			name: "time limit wins when both limits are reached", verdict: followUp, followUps: 0, questions: 15, elapsedMin: 20, remaining: 2,
			startedAt: &start, maxQuestions: 15, maxMinutes: 15, wantAction: GuardFinish, wantReason: domain.FinishReasonTimeLimit,
		},
		{
			name: "time limit finishes even with topics left", verdict: sufficient, followUps: 0, questions: 3, elapsedMin: 15, remaining: 2,
			startedAt: &start, maxQuestions: 15, maxMinutes: 15, wantAction: GuardFinish, wantReason: domain.FinishReasonTimeLimit,
		},
		{
			name: "drop follow up when questions cannot cover remaining topics", verdict: followUp, followUps: 0, questions: 13, elapsedMin: 5, remaining: 2,
			startedAt: &start, maxQuestions: 15, maxMinutes: 15, wantAction: GuardNextTopic,
		},
		{
			name: "drop follow up when time cannot cover remaining topics", verdict: followUp, followUps: 0, questions: 13, elapsedMin: 14, remaining: 1,
			startedAt: &start, maxQuestions: 15, maxMinutes: 15, wantAction: GuardNextTopic,
		},
		{
			name: "follow up kept when time covers all remaining topics", verdict: followUp, followUps: 0, questions: 5, elapsedMin: 5, remaining: 2,
			startedAt: &start, maxQuestions: 15, maxMinutes: 15, wantAction: GuardFollowUp,
		},
		{
			name: "unknown verdict is treated as sufficient", verdict: JudgeVerdict{Verdict: "banana"}, followUps: 0, questions: 1, elapsedMin: 1, remaining: 2,
			startedAt: &start, maxQuestions: 15, maxMinutes: 15, wantAction: GuardNextTopic,
		},
		{
			name: "missing started_at does not block follow up", verdict: followUp, followUps: 0, questions: 1, elapsedMin: 1, remaining: 2,
			startedAt: nil, maxQuestions: 15, maxMinutes: 15, wantAction: GuardFollowUp,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := DecideGuard(GuardInput{
				Verdict:         tc.verdict,
				FollowUpCount:   tc.followUps,
				QuestionCount:   tc.questions,
				StartedAt:       tc.startedAt,
				Now:             at(tc.elapsedMin),
				RemainingTopics: tc.remaining,
				MaxFollowUps:    maxFollowUpsPerTopic,
				MaxQuestions:    tc.maxQuestions,
				MaxMinutes:      tc.maxMinutes,
			})
			if got.Action != tc.wantAction || got.FinishReason != tc.wantReason {
				t.Errorf("DecideGuard(%s) = %+v, want action %q reason %q", tc.name, got, tc.wantAction, tc.wantReason)
			}
		})
	}
}
