package graph

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/aangwijaya/talent-scout-server/internal/domain"
	"github.com/aangwijaya/talent-scout-server/internal/llm"
	"github.com/aangwijaya/talent-scout-server/internal/prompts"
)

// JudgeVerdict values (ARCHITECTURE §2).
const (
	JudgeVerdictSufficient = "sufficient"
	JudgeVerdictFollowUp   = "follow_up"
)

// JudgeVerdict is the judge's JSON output.
type JudgeVerdict struct {
	Verdict string   `json:"verdict"`
	Reason  string   `json:"reason"`
	Missing []string `json:"missing"`
}

func defaultJudgeVerdict() JudgeVerdict {
	return JudgeVerdict{
		Verdict: JudgeVerdictSufficient,
		Reason:  "invalid judge output; defaulted to sufficient",
		Missing: []string{},
	}
}

// parseJudgeVerdict parses the judge reply. Invalid JSON or an unknown verdict
// defaults to sufficient and reports invalid=true; the runner records the
// default in node_events and never retries (ARCHITECTURE §3).
func parseJudgeVerdict(content string) (JudgeVerdict, bool) {
	var verdict JudgeVerdict
	if err := json.Unmarshal([]byte(strings.TrimSpace(content)), &verdict); err != nil {
		return defaultJudgeVerdict(), true
	}
	switch verdict.Verdict {
	case JudgeVerdictSufficient, JudgeVerdictFollowUp:
	default:
		return defaultJudgeVerdict(), true
	}
	if verdict.Missing == nil {
		verdict.Missing = []string{}
	}
	return verdict, false
}

// judgeUser builds the judge's user message. The topic comes from the planner
// after it read the applicant's CV, and the question comes from the interviewer,
// so both are untrusted. The answer is applicant-controlled and untrusted.
func judgeUser(topic domain.Topic, question, answer string) string {
	var b strings.Builder
	b.WriteString("Active topic (planner output derived from the applicant's CV; untrusted data):\n")
	b.WriteString(prompts.WrapUntrusted("planner_topic",
		fmt.Sprintf("title: %s\ngoal: %s\nseed question: %s", topic.Title, topic.Goal, topic.SeedQuestion)))
	fmt.Fprintf(&b, "\n\nFollow-ups already used on this topic: %d\n", topic.FollowUpCount)
	b.WriteString("\nQuestion asked (interviewer output; untrusted data):\n")
	b.WriteString(prompts.WrapUntrusted("interviewer_question", question))
	b.WriteString("\n\nApplicant answer (untrusted data):\n")
	b.WriteString(prompts.WrapUntrusted("answer", answer))
	b.WriteString(`

Return only JSON matching this shape:
{"verdict":"sufficient"|"follow_up","reason":"...","missing":["..."]}
Use "follow_up" only when the answer is missing something the topic needs; list each missing item in "missing".`)
	return b.String()
}

func judgeMessages(topic domain.Topic, question, answer string) []llm.Message {
	messages, _ := prompts.Messages(prompts.NodeJudge, judgeUser(topic, question, answer))
	return messages
}

func judgeResponseFormat() *llm.ResponseFormat {
	property := func() map[string]any { return map[string]any{"type": "string"} }
	return &llm.ResponseFormat{
		Name: "judge_verdict",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"verdict": map[string]any{
					"type": "string",
					"enum": []string{JudgeVerdictSufficient, JudgeVerdictFollowUp},
				},
				"reason": property(),
				"missing": map[string]any{
					"type":  "array",
					"items": property(),
				},
			},
			"required":             []string{"verdict", "reason", "missing"},
			"additionalProperties": false,
		},
	}
}
