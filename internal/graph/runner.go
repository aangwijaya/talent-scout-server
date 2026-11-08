package graph

import (
	"context"
	"time"

	"github.com/aangwijaya/talent-scout-server/internal/config"
	"github.com/aangwijaya/talent-scout-server/internal/domain"
	"github.com/aangwijaya/talent-scout-server/internal/llm"
	"github.com/aangwijaya/talent-scout-server/internal/store"
)

// Runner orchestrates one graph run: it emits run/node/check/edge events in the
// order defined by docs/EVENTS.md §6, persists them via the bus, and keeps the
// run token/cost totals. It is not safe for concurrent use by multiple
// goroutines; one runner goroutine drives one active session.
type Runner struct {
	store     store.Store
	bus       *Bus
	llm       llm.Client
	models    config.ModelsConfig
	runID     string
	sessionID string

	totalTokensIn  int
	totalTokensOut int
	costUSD        float64
}

// NewRunner builds a runner for runID (already created via store.CreateGraphRun).
func NewRunner(st store.Store, bus *Bus, llm llm.Client, models config.ModelsConfig, runID, sessionID string) *Runner {
	return &Runner{store: st, bus: bus, llm: llm, models: models, runID: runID, sessionID: sessionID}
}

// RunID returns the run this runner drives.
func (r *Runner) RunID() string { return r.runID }

// Start emits run.started with the graph definition.
func (r *Runner) Start(ctx context.Context) error {
	_, err := r.bus.Emit(ctx, r.runID, Event{
		Type:    EventRunStarted,
		Payload: RunStarted{SessionID: r.sessionID, Nodes: Nodes(), Edges: Edges},
	})
	return err
}

// RunLLMNode runs an LLM node exactly once per attempt, applying the model
// config from config/models.yaml and the §6.3 transport-retry choreography:
// node.started(attempt=1), on a retryable failure node.failed(will_retry=true)
// + node.started(attempt=2), then node.completed or node.failed(will_retry=false).
// onDelta receives streamed chunks (ignored for non-stream nodes).
func (r *Runner) RunLLMNode(ctx context.Context, nodeID string, req llm.Request, inputSummary string, onDelta func(string) error) (llm.Response, error) {
	node, err := r.models.Node(nodeID)
	if err != nil {
		return llm.Response{}, err
	}
	req.Model = node.Model
	req.Temperature = r.models.Temperature(nodeID)
	req.MaxTokens = node.MaxTokens
	req.Stream = node.Stream
	req.Timeout = r.models.Timeout()
	req.MaxRetries = r.models.MaxRetries()
	if node.Reasoning != nil {
		req.Reasoning = &llm.ReasoningConfig{Effort: node.Reasoning.Effort}
	}

	model := node.Model
	attempt := 1
	start := time.Now()
	req.OnRetry = func(failedAttempt int, err error) {
		_ = r.emitNodeFailed(ctx, nodeID, failedAttempt, &model, err.Error(), true)
		attempt = failedAttempt + 1
		start = time.Now()
		_ = r.emitNodeStarted(ctx, nodeID, attempt, &model, inputSummary)
	}

	if err := r.emitNodeStarted(ctx, nodeID, 1, &model, inputSummary); err != nil {
		return llm.Response{}, err
	}

	var resp llm.Response
	if req.Stream {
		resp, err = r.llm.Stream(ctx, req, onDelta)
	} else {
		resp, err = r.llm.Chat(ctx, req)
	}
	if err != nil {
		_ = r.emitNodeFailed(ctx, nodeID, attempt, &model, err.Error(), false)
		return llm.Response{}, err
	}

	if err := r.emitNodeCompleted(ctx, nodeID, attempt, &model, summarize(resp.Content, 80),
		resp.Usage.PromptTokens, resp.Usage.CompletionTokens, time.Since(start)); err != nil {
		return llm.Response{}, err
	}
	return resp, nil
}

// RunCodeNode runs a deterministic code node (no LLM). It emits node.started
// then node.completed, or node.failed if fn returns an error.
func (r *Runner) RunCodeNode(ctx context.Context, nodeID string, attempt int, inputSummary string, fn func() (outputSummary string, err error)) error {
	if err := r.emitNodeStarted(ctx, nodeID, attempt, nil, inputSummary); err != nil {
		return err
	}
	start := time.Now()
	summary, err := fn()
	if err != nil {
		_ = r.emitNodeFailed(ctx, nodeID, attempt, nil, err.Error(), false)
		return err
	}
	return r.emitNodeCompleted(ctx, nodeID, attempt, nil, summary, 0, 0, time.Since(start))
}

// Emit publishes a raw event with explicit node/attempt metadata.
func (r *Runner) Emit(ctx context.Context, nodeID string, attempt *int, model *string, eventType string, payload any) error {
	_, err := r.bus.Emit(ctx, r.runID, Event{NodeID: strPtr(nodeID), Attempt: attempt, Model: model, Type: eventType, Payload: payload})
	return err
}

func (r *Runner) emitNodeStarted(ctx context.Context, nodeID string, attempt int, model *string, inputSummary string) error {
	_, err := r.bus.Emit(ctx, r.runID, Event{
		NodeID:  domain.StrPtr(nodeID),
		Attempt: domain.IntPtr(attempt),
		Model:   model,
		Type:    EventNodeStarted,
		Payload: NodeStarted{Model: model, InputSummary: inputSummary},
	})
	return err
}

func (r *Runner) emitNodeCompleted(ctx context.Context, nodeID string, attempt int, model *string, summary string, tokensIn, tokensOut int, duration time.Duration) error {
	cost := 0.0
	if model != nil {
		cost = r.models.CostUSD(*model, tokensIn, tokensOut)
	}
	r.totalTokensIn += tokensIn
	r.totalTokensOut += tokensOut
	r.costUSD += cost

	_, err := r.bus.Emit(ctx, r.runID, Event{
		NodeID:     domain.StrPtr(nodeID),
		Attempt:    domain.IntPtr(attempt),
		Model:      model,
		Type:       EventNodeCompleted,
		TokensIn:   tokensIn,
		TokensOut:  tokensOut,
		DurationMS: int(duration.Milliseconds()),
		Payload: NodeCompleted{
			OutputSummary: summary,
			TokensIn:      tokensIn,
			TokensOut:     tokensOut,
			DurationMS:    int(duration.Milliseconds()),
			CostUSD:       cost,
		},
	})
	return err
}

func (r *Runner) emitNodeFailed(ctx context.Context, nodeID string, attempt int, model *string, errMsg string, willRetry bool) error {
	_, err := r.bus.Emit(ctx, r.runID, Event{
		NodeID:  domain.StrPtr(nodeID),
		Attempt: domain.IntPtr(attempt),
		Model:   model,
		Type:    EventNodeFailed,
		Payload: NodeFailed{Error: errMsg, WillRetry: willRetry},
	})
	return err
}

// EmitNodeSkipped emits node.skipped for a node that never ran.
func (r *Runner) EmitNodeSkipped(ctx context.Context, nodeID, reason string) error {
	_, err := r.bus.Emit(ctx, r.runID, Event{
		NodeID:  domain.StrPtr(nodeID),
		Type:    EventNodeSkipped,
		Payload: NodeSkipped{Reason: reason},
	})
	return err
}

// EmitEdge emits edge.traversed.
func (r *Runner) EmitEdge(ctx context.Context, from, to, label string) error {
	_, err := r.bus.Emit(ctx, r.runID, Event{
		Type:    EventEdgeTraversed,
		Payload: EdgeTraversed{From: from, To: to, Label: label},
	})
	return err
}

// EmitCheckPassed emits check.passed for a code node attempt.
func (r *Runner) EmitCheckPassed(ctx context.Context, nodeID string, attempt int, check string, details map[string]any) error {
	_, err := r.bus.Emit(ctx, r.runID, Event{
		NodeID:  domain.StrPtr(nodeID),
		Attempt: domain.IntPtr(attempt),
		Type:    EventCheckPassed,
		Payload: CheckPassed{Check: check, Details: details},
	})
	return err
}

// EmitCheckFailed emits check.failed for a code node attempt.
func (r *Runner) EmitCheckFailed(ctx context.Context, nodeID string, attempt int, check string, errs []CheckError) error {
	_, err := r.bus.Emit(ctx, r.runID, Event{
		NodeID:  domain.StrPtr(nodeID),
		Attempt: domain.IntPtr(attempt),
		Type:    EventCheckFailed,
		Payload: CheckFailed{Check: check, Errors: errs},
	})
	return err
}

// EmitLoopRetry emits loop.retry for an automatic node retry.
func (r *Runner) EmitLoopRetry(ctx context.Context, nodeID string, attempt, maxAttempts int, reason, feedback string) error {
	_, err := r.bus.Emit(ctx, r.runID, Event{
		NodeID:  domain.StrPtr(nodeID),
		Attempt: domain.IntPtr(attempt),
		Type:    EventLoopRetry,
		Payload: LoopRetry{Attempt: attempt, MaxAttempts: maxAttempts, Reason: reason, Feedback: feedback},
	})
	return err
}

// EmitTopicChanged emits topic.changed.
func (r *Runner) EmitTopicChanged(ctx context.Context, nodeID string, attempt int, topicID string, position int, title, status string) error {
	_, err := r.bus.Emit(ctx, r.runID, Event{
		NodeID:  domain.StrPtr(nodeID),
		Attempt: domain.IntPtr(attempt),
		Type:    EventTopicChanged,
		Payload: TopicChanged{TopicID: topicID, Position: position, Title: title, Status: status},
	})
	return err
}

// EmitMessageCompleted emits message.completed (assistant or applicant).
func (r *Runner) EmitMessageCompleted(ctx context.Context, nodeID string, attempt int, message domain.Message) error {
	_, err := r.bus.Emit(ctx, r.runID, Event{
		NodeID:  domain.StrPtr(nodeID),
		Attempt: domain.IntPtr(attempt),
		Type:    EventMessageCompleted,
		Payload: MessageCompleted{Message: message},
	})
	return err
}

// Complete emits run.completed and marks the run succeeded with its totals.
func (r *Runner) Complete(ctx context.Context, finishReason string, needsManualCount int) error {
	_, err := r.bus.Emit(ctx, r.runID, Event{
		Type: EventRunCompleted,
		Payload: RunCompleted{
			Status:           string(domain.GraphRunStatusSucceeded),
			FinishReason:     finishReason,
			TotalTokensIn:    r.totalTokensIn,
			TotalTokensOut:   r.totalTokensOut,
			CostUSD:          r.costUSD,
			NeedsManualCount: needsManualCount,
		},
	})
	if err != nil {
		return err
	}
	return r.finishRun(ctx, domain.GraphRunStatusSucceeded, nil)
}

// Fail emits run.failed and marks the run failed.
func (r *Runner) Fail(ctx context.Context, errMsg string) error {
	_, err := r.bus.Emit(ctx, r.runID, Event{
		Type:    EventRunFailed,
		Payload: RunFailed{Error: errMsg},
	})
	if err != nil {
		return err
	}
	return r.finishRun(ctx, domain.GraphRunStatusFailed, &errMsg)
}

func (r *Runner) finishRun(ctx context.Context, status domain.GraphRunStatus, errMsg *string) error {
	ended := time.Now().UTC()
	run := domain.GraphRun{
		ID:             r.runID,
		Status:         status,
		EndedAt:        &ended,
		TotalTokensIn:  r.totalTokensIn,
		TotalTokensOut: r.totalTokensOut,
		CostUSD:        r.costUSD,
		Error:          errMsg,
	}
	return r.store.UpdateGraphRun(ctx, run)
}

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func summarize(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
