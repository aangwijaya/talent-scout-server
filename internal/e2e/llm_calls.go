package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

type llmRequestInfo struct {
	Model     string `json:"model"`
	MaxTokens int    `json:"max_tokens"`
	Stream    bool   `json:"stream"`
	Messages  []struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"messages"`
}

type llmResponseInfo struct {
	FinishReason     string
	PromptTokens     int
	CompletionTokens int
}

func nodeFromRequest(req llmRequestInfo) string {
	for _, message := range req.Messages {
		if message.Role != "system" {
			continue
		}
		switch {
		case strings.Contains(message.Content, "You are the planner"):
			return "planner"
		case strings.Contains(message.Content, "You are the interviewer"):
			return "interviewer"
		case strings.Contains(message.Content, "You are the judge"):
			return "judge"
		case strings.Contains(message.Content, "You are the scorer"):
			return "scorer"
		case strings.Contains(message.Content, "You are the authenticity reviewer"):
			return "authenticity"
		case strings.Contains(message.Content, "You are the fluency reviewer"):
			return "fluency"
		case strings.Contains(message.Content, "You are a hiring rubric assistant"):
			return "rubric_suggester"
		}
	}
	return "unknown"
}

// truncationProblems reports the ways a captured call looks truncated. Every
// node is checked, so a new node is covered without editing a hand-picked list.
func truncationProblems(node string, req llmRequestInfo, resp llmResponseInfo) []string {
	var problems []string
	// The interviewer's stream is stopped on purpose at the first "?" or at 120
	// words, then drained only for usage. The provider keeps generating after
	// that stop and can end the stream with finish_reason=length even though the
	// forwarded reply was complete; parseStream ignores finish_reason after a
	// guard stop, so the raw capture cannot tell the two cases apart. Guard its
	// cap with completion_tokens instead.
	if node != "interviewer" && resp.FinishReason == "length" {
		problems = append(problems, fmt.Sprintf("%s hit finish_reason=length at max_tokens=%d", node, req.MaxTokens))
	}
	if resp.CompletionTokens > req.MaxTokens {
		problems = append(problems, fmt.Sprintf("%s completion_tokens=%d exceeds max_tokens=%d", node, resp.CompletionTokens, req.MaxTokens))
	}
	return problems
}

func parseResponse(raw []byte) llmResponseInfo {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return llmResponseInfo{}
	}
	if trimmed[0] == '{' {
		var body struct {
			Choices []struct {
				FinishReason string `json:"finish_reason"`
			} `json:"choices"`
			Usage struct {
				PromptTokens     int `json:"prompt_tokens"`
				CompletionTokens int `json:"completion_tokens"`
			} `json:"usage"`
		}
		if err := json.Unmarshal(trimmed, &body); err == nil {
			info := llmResponseInfo{PromptTokens: body.Usage.PromptTokens, CompletionTokens: body.Usage.CompletionTokens}
			if len(body.Choices) > 0 {
				info.FinishReason = body.Choices[0].FinishReason
			}
			return info
		}
	}

	var info llmResponseInfo
	for _, line := range strings.Split(string(raw), "\n") {
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" || data == "[DONE]" || !strings.HasPrefix(data, "{") {
			continue
		}
		var chunk struct {
			Choices []struct {
				FinishReason string `json:"finish_reason"`
			} `json:"choices"`
			Usage *struct {
				PromptTokens     int `json:"prompt_tokens"`
				CompletionTokens int `json:"completion_tokens"`
			} `json:"usage"`
		}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}
		if chunk.Usage != nil {
			info.PromptTokens = chunk.Usage.PromptTokens
			info.CompletionTokens = chunk.Usage.CompletionTokens
		}
		for _, choice := range chunk.Choices {
			if choice.FinishReason != "" {
				info.FinishReason = choice.FinishReason
			}
		}
	}
	return info
}
