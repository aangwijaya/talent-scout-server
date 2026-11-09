package graph

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/aangwijaya/talent-scout-server/internal/domain"
	"github.com/aangwijaya/talent-scout-server/internal/llm"
	"github.com/aangwijaya/talent-scout-server/internal/prompts"
)

// maxInterviewerWords is where streaming stops. The stored message is exactly
// the text that was streamed, so message.completed and the transcript agree.
const maxInterviewerWords = 120

var (
	errWordLimit  = errors.New("interviewer word limit reached")
	errEmptyReply = errors.New("empty interviewer reply")
)

// interviewerUser builds the interviewer's user message. The active topic is
// trusted (it comes from the planner/job), every applicant answer is untrusted
// and appears only inside a prompts.WrapUntrusted block.
func interviewerUser(topic domain.Topic, transcript []domain.Message, kind domain.MessageKind) string {
	var b strings.Builder
	b.WriteString("Active topic:\n")
	fmt.Fprintf(&b, "- title: %s\n- goal: %s\n- seed question: %s\n", topic.Title, topic.Goal, topic.SeedQuestion)
	b.WriteString("\nMode: ")
	b.WriteString(string(kind))
	b.WriteString("\n\nConversation so far:\n")
	for _, message := range transcript {
		switch message.Role {
		case domain.MessageRoleApplicant:
			b.WriteString("Applicant:\n")
			b.WriteString(prompts.WrapUntrusted("answer", message.Content))
			b.WriteString("\n")
		case domain.MessageRoleAssistant:
			b.WriteString("Interviewer: ")
			b.WriteString(message.Content)
			b.WriteString("\n")
		}
	}
	b.WriteString("\nAsk exactly one question about the active topic, at most 80 words. Do not repeat a question already asked.")
	return b.String()
}

func interviewerMessages(topic domain.Topic, transcript []domain.Message, kind domain.MessageKind) []llm.Message {
	messages, _ := prompts.Messages(prompts.NodeInterviewer, interviewerUser(topic, transcript, kind))
	return messages
}

// wordLimiter forwards at most limit whole words and remembers exactly what was
// forwarded.
type wordLimiter struct {
	limit int
	raw   strings.Builder
	sent  int
}

func newWordLimiter(limit int) *wordLimiter {
	return &wordLimiter{limit: limit}
}

// Add appends a delta and returns the part to emit plus whether the reply went
// beyond the limit (the caller then stops the stream).
func (l *wordLimiter) Add(delta string) (string, bool) {
	l.raw.WriteString(delta)
	allowed := prefixWords(l.raw.String(), l.limit)
	if len(allowed) > l.sent {
		out := allowed[l.sent:]
		l.sent = len(allowed)
		return out, len(allowed) < len(l.raw.String())
	}
	return "", len(allowed) < len(l.raw.String())
}

// Text returns exactly the text that was forwarded to the client.
func (l *wordLimiter) Text() string {
	return prefixWords(l.raw.String(), l.limit)
}

func (l *wordLimiter) Reset() {
	l.raw.Reset()
	l.sent = 0
}

func prefixWords(s string, limit int) string {
	words := 0
	inWord := false
	for i, r := range s {
		if unicode.IsSpace(r) {
			inWord = false
			continue
		}
		if !inWord {
			inWord = true
			words++
			if words > limit {
				return strings.TrimRightFunc(s[:i], unicode.IsSpace)
			}
		}
	}
	return s
}

type streamResult struct {
	Text      string
	Model     string
	Attempt   int
	Usage     llm.Usage
	StartedAt time.Time
}

// streamInterviewer runs one streaming interviewer attempt with the same
// transport-retry choreography as RunLLMNode, but treats truncation and the
// 120-word stop as a finished reply (the deltas already sent are real text).
func (r *Runner) streamInterviewer(ctx context.Context, attempt int, messages []llm.Message, inputSummary, messageID string, limiter *wordLimiter) (streamResult, error) {
	node, err := r.models.Node("interviewer")
	if err != nil {
		return streamResult{}, err
	}
	req := llm.Request{
		Node:        "interviewer",
		Model:       node.Model,
		Messages:    messages,
		Temperature: r.models.Temperature("interviewer"),
		MaxTokens:   node.MaxTokens,
		Stream:      true,
		Timeout:     r.models.Timeout(),
		MaxRetries:  r.models.MaxRetries(),
	}
	if node.Reasoning != nil {
		req.Reasoning = &llm.ReasoningConfig{Effort: node.Reasoning.Effort}
	}

	model := node.Model
	currentAttempt := attempt
	start := time.Now()
	req.OnRetry = func(failedAttempt int, err error) {
		failed := attempt + failedAttempt - 1
		_ = r.emitNodeFailed(ctx, "interviewer", failed, &model, err.Error(), true)
		currentAttempt = failed + 1
		start = time.Now()
		limiter.Reset()
		_ = r.emitNodeStarted(ctx, "interviewer", currentAttempt, &model, inputSummary)
	}

	if err := r.emitNodeStarted(ctx, "interviewer", currentAttempt, &model, inputSummary); err != nil {
		return streamResult{}, err
	}

	onDelta := func(delta string) error {
		out, stopped := limiter.Add(delta)
		if out != "" {
			if _, err := r.bus.Emit(ctx, r.runID, Event{
				NodeID:  domain.StrPtr("interviewer"),
				Attempt: domain.IntPtr(currentAttempt),
				Type:    EventMessageDelta,
				Payload: MessageDelta{MessageID: messageID, Delta: out},
			}); err != nil {
				return err
			}
		}
		if stopped {
			return errWordLimit
		}
		return nil
	}

	resp, err := r.llm.Stream(ctx, req, onDelta)
	if err != nil && !errors.Is(err, errWordLimit) && !errors.Is(err, llm.ErrTruncated) {
		_ = r.emitNodeFailed(ctx, "interviewer", currentAttempt, &model, err.Error(), false)
		return streamResult{}, err
	}

	text := limiter.Text()
	if strings.TrimSpace(text) == "" {
		_ = r.emitNodeFailed(ctx, "interviewer", currentAttempt, &model, errEmptyReply.Error(), false)
		return streamResult{}, errEmptyReply
	}
	return streamResult{Text: text, Model: model, Attempt: currentAttempt, Usage: resp.Usage, StartedAt: start}, nil
}
