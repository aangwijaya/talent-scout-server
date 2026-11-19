package graph

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/aangwijaya/talent-scout-server/internal/domain"
	"github.com/aangwijaya/talent-scout-server/internal/llm"
)

func TestInterviewerReplyEndsAtFirstQuestion(t *testing.T) {
	tests := []struct {
		name     string
		streamed []string
		want     string
	}{
		{
			name: "two questions keep only the first",
			streamed: []string{
				"Tell me about a feature you shipped",
				". What did you measure",
				"? And why did that matter?",
			},
			want: "Tell me about a feature you shipped. What did you measure?",
		},
		{
			name:     "single question is unchanged",
			streamed: []string{"What did you measure before and after?"},
			want:     "What did you measure before and after?",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st, sess := seedGraphSession(t)
			client := &stubClient{stream: func(req llm.Request, onDelta func(string) error) (llm.Response, error) {
				if req.Node != "interviewer" {
					return llm.Response{Content: FakeReplies[req.Node]}, nil
				}
				for _, delta := range tc.streamed {
					if err := onDelta(delta); err != nil {
						return llm.Response{}, err
					}
				}
				return llm.Response{Content: strings.Join(tc.streamed, "")}, nil
			}}
			manager, _ := newTestManager(t, st, client)
			events, cancel := manager.bus.SubscribeSession(sess.ID)
			defer cancel()

			if err := manager.Start(context.Background(), sess); err != nil {
				t.Fatalf("Start: %v", err)
			}
			waitIdle(t, manager, sess.ID)

			messages, _ := st.ListMessagesBySession(context.Background(), sess.ID, false)
			if len(messages) != 1 || messages[0].Content != tc.want {
				t.Fatalf("messages = %+v, want one assistant message %q", messages, tc.want)
			}
			if got := collectedInterviewerDeltas(t, events); got != tc.want {
				t.Errorf("streamed text = %q, want %q", got, tc.want)
			}

			for _, event := range sessionEvents(t, st, sess.ID) {
				if event.Type == EventNodeFailed && event.NodeID != nil && *event.NodeID == "interviewer" {
					t.Errorf("interviewer emitted node.failed: %s", event.Payload)
				}
			}
		})
	}
}

func collectedInterviewerDeltas(t *testing.T, events <-chan domain.NodeEvent) string {
	t.Helper()
	var b strings.Builder
	for {
		select {
		case event := <-events:
			if event.Type != EventMessageDelta || event.NodeID == nil || *event.NodeID != "interviewer" {
				continue
			}
			var payload MessageDelta
			if err := json.Unmarshal(event.Payload, &payload); err != nil {
				t.Fatalf("decode message.delta: %v", err)
			}
			b.WriteString(payload.Delta)
		default:
			return b.String()
		}
	}
}

func wordsText(n int) string {
	words := make([]string, n)
	for i := range words {
		words[i] = "word"
	}
	return strings.Join(words, " ")
}

// The 120-word stop must still end the stream, not only trim what is stored.
func TestWordLimiterStopsTheStreamAtTheWordLimit(t *testing.T) {
	l := newWordLimiter(maxInterviewerWords)
	out, err := l.Add(wordsText(130) + " Why?")
	if !errors.Is(err, errWordLimit) {
		t.Fatalf("err = %v, want errWordLimit so the stream stops", err)
	}
	if got := len(strings.Fields(out)); got != maxInterviewerWords {
		t.Errorf("emitted words = %d, want %d", got, maxInterviewerWords)
	}
	if got := len(strings.Fields(l.Text())); got != maxInterviewerWords {
		t.Errorf("stored words = %d, want %d", got, maxInterviewerWords)
	}
}

// A question mark after the word limit, in the same delta, must not let more
// than 120 words through.
func TestWordLimiterQuestionAfterTheLimitKeepsTheLimit(t *testing.T) {
	l := newWordLimiter(maxInterviewerWords)
	_, err := l.Add(wordsText(123) + " word? word")
	if !errors.Is(err, errWordLimit) {
		t.Errorf("err = %v, want errWordLimit", err)
	}
	if got := len(strings.Fields(l.Text())); got != maxInterviewerWords {
		t.Errorf("stored words = %d, want %d", got, maxInterviewerWords)
	}
}
