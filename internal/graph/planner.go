package graph

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/aangwijaya/talent-scout-server/internal/domain"
	"github.com/aangwijaya/talent-scout-server/internal/llm"
	"github.com/aangwijaya/talent-scout-server/internal/prompts"
)

// plannerUser builds the planner's user message. The job description and
// rubric are trusted; the CV is applicant-controlled and only ever appears
// inside a capped prompts.CVBlock.
func plannerUser(job domain.Job, cvText string, feedback []PlanCheckError) string {
	var b strings.Builder
	b.WriteString("Job title: ")
	b.WriteString(job.Title)
	b.WriteString("\n\nJob description:\n")
	b.WriteString(job.Description)
	b.WriteString("\n\nRubric criteria (key: label - description, weight):\n")
	for _, criterion := range job.Rubric {
		fmt.Fprintf(&b, "- %s: %s - %s (weight %d)\n", criterion.Key, criterion.Label, criterion.Description, criterion.Weight)
	}
	b.WriteString("\nCV (untrusted data):\n")
	b.WriteString(prompts.CVBlock(cvText))
	b.WriteString("\n\nReturn only JSON matching this shape: ")
	b.WriteString(`{"topics":[{"title":"...","goal":"...","rubric_keys":["..."],"seed_question":"..."}]}`)
	b.WriteString("\nUse 3 to 5 topics. Every rubric key must appear in at least one topic and every rubric_keys entry must exist in the rubric.")
	if len(feedback) > 0 {
		b.WriteString("\nThe previous plan was rejected for these reasons; fix them:\n")
		for _, err := range feedback {
			b.WriteString("- ")
			b.WriteString(err.String())
			b.WriteString("\n")
		}
	}
	return b.String()
}

func plannerMessages(job domain.Job, cvText string, feedback []PlanCheckError) []llm.Message {
	messages, _ := prompts.Messages(prompts.NodePlanner, plannerUser(job, cvText, feedback))
	return messages
}

func plannerResponseFormat() *llm.ResponseFormat {
	property := func() map[string]any {
		return map[string]any{"type": "string"}
	}
	topic := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"title":         property(),
			"goal":          property(),
			"seed_question": property(),
			"rubric_keys": map[string]any{
				"type":  "array",
				"items": property(),
			},
		},
		"required":             []string{"title", "goal", "rubric_keys", "seed_question"},
		"additionalProperties": false,
	}
	return &llm.ResponseFormat{
		Name: "interview_plan",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"topics": map[string]any{
					"type":  "array",
					"items": topic,
				},
			},
			"required":             []string{"topics"},
			"additionalProperties": false,
		},
	}
}

func parsePlan(content string) (Plan, error) {
	var plan Plan
	content = strings.TrimSpace(content)
	if err := json.Unmarshal([]byte(content), &plan); err != nil {
		return Plan{}, fmt.Errorf("parse plan JSON: %w", err)
	}
	return plan, nil
}

func planCheckPayload(errs []PlanCheckError) []CheckError {
	out := make([]CheckError, 0, len(errs))
	for _, err := range errs {
		out = append(out, CheckError{RubricKey: err.RubricKey, Reason: err.Reason})
	}
	return out
}

func planFeedback(errs []PlanCheckError) string {
	parts := make([]string, 0, len(errs))
	for _, err := range errs {
		parts = append(parts, err.String())
	}
	return strings.Join(parts, "; ")
}
