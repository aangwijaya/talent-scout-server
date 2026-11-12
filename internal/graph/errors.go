package graph

import (
	"context"
	"errors"
	"log/slog"

	"github.com/aangwijaya/talent-scout-server/internal/llm"
)

// Codes for node.failed.error on LLM nodes (docs/EVENTS.md §4). The raw
// transport/provider detail is never sent; it stays in the server log.
const (
	nodeFailedTimeout        = "timeout"
	nodeFailedLLMUnavailable = "llm_unavailable"
	nodeFailedEmptyReply     = "empty_reply"
	nodeFailedLLMError       = "llm_error"
)

// nodeFailedCode maps an LLM call error to the closed set of node.failed codes.
func nodeFailedCode(err error) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return nodeFailedTimeout
	case errors.Is(err, errEmptyReply):
		return nodeFailedEmptyReply
	case errors.Is(err, llm.ErrUnavailable):
		return nodeFailedLLMUnavailable
	}
	var retryable *llm.RetryableError
	if errors.As(err, &retryable) {
		return nodeFailedLLMUnavailable
	}
	return nodeFailedLLMError
}

// logNodeFailed keeps the raw error in the server log, with the run, node and
// attempt, while the event carries only its code.
func (r *Runner) logNodeFailed(nodeID string, attempt int, err error) {
	slog.Default().Error("llm node failed", "run_id", r.runID, "node_id", nodeID, "attempt", attempt, "error", err)
}
