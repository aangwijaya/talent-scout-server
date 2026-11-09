// Package graph holds the graph definition, the ordered event bus, and the
// runner that orchestrates nodes and persists node_events.
package graph

import (
	"github.com/aangwijaya/talent-scout-server/internal/domain"
)

// Event names (source of truth: docs/EVENTS.md §5). Names and data payloads
// must not change without a contract change.
const (
	EventRunStarted       = "run.started"
	EventRunCompleted     = "run.completed"
	EventRunFailed        = "run.failed"
	EventNodeStarted      = "node.started"
	EventNodeCompleted    = "node.completed"
	EventNodeFailed       = "node.failed"
	EventNodeSkipped      = "node.skipped"
	EventCheckPassed      = "check.passed"
	EventCheckFailed      = "check.failed"
	EventLoopRetry        = "loop.retry"
	EventEdgeTraversed    = "edge.traversed"
	EventTopicChanged     = "topic.changed"
	EventMessageCompleted = "message.completed"
	EventMessageDelta     = "message.delta"
	EventError            = "error"
	EventSessionUpdated   = "session.updated"
	EventSessionEnded     = "session.ended"
)

// NodeType identifies the kind of graph node (docs/API.md "graph-runs").
type NodeType string

const (
	NodeTypeSplitter  NodeType = "splitter"
	NodeTypeWorker    NodeType = "worker"
	NodeTypeGate      NodeType = "gate"
	NodeTypeCode      NodeType = "code"
	NodeTypeHumanGate NodeType = "human_gate"
)

// NodeRef is one entry of the run.started nodes list.
type NodeRef struct {
	ID   string `json:"id"`
	Type string `json:"type"`
}

// EdgeRef is one entry of the run.started edges list.
type EdgeRef struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// NodeIDs is the ordered machine-node list. recruiter_gate is a human gate
// outside the runner and emits no node events, but it is reported in run.started.
var NodeIDs = []string{
	"planner", "plan_check", "interviewer", "judge", "guard", "scorer", "evidence_check", "recruiter_gate",
}

var nodeTypes = map[string]NodeType{
	"planner":        NodeTypeSplitter,
	"plan_check":     NodeTypeCode,
	"interviewer":    NodeTypeWorker,
	"judge":          NodeTypeGate,
	"guard":          NodeTypeCode,
	"scorer":         NodeTypeWorker,
	"evidence_check": NodeTypeCode,
	"recruiter_gate": NodeTypeHumanGate,
}

// Edges is the static machine-node graph reported in run.started. It includes
// every transition the runner can traverse, including the plan_check→planner
// retry (EVENTS.md §6.2).
var Edges = []EdgeRef{
	{From: "planner", To: "plan_check"},
	{From: "plan_check", To: "planner"},
	{From: "plan_check", To: "interviewer"},
	{From: "interviewer", To: "judge"},
	{From: "interviewer", To: "interviewer"},
	{From: "judge", To: "guard"},
	{From: "guard", To: "interviewer"},
	{From: "guard", To: "scorer"},
	{From: "scorer", To: "evidence_check"},
	{From: "evidence_check", To: "scorer"},
	{From: "evidence_check", To: "recruiter_gate"},
}

// Nodes returns the run.started nodes list.
func Nodes() []NodeRef {
	out := make([]NodeRef, 0, len(NodeIDs))
	for _, id := range NodeIDs {
		out = append(out, NodeRef{ID: id, Type: string(nodeTypes[id])})
	}
	return out
}

// RunStarted is the run.started data payload.
type RunStarted struct {
	SessionID string    `json:"session_id"`
	Nodes     []NodeRef `json:"nodes"`
	Edges     []EdgeRef `json:"edges"`
}

// NodeStarted is the node.started data payload.
type NodeStarted struct {
	Model        *string `json:"model"`
	InputSummary string  `json:"input_summary"`
}

// NodeCompleted is the node.completed data payload.
type NodeCompleted struct {
	OutputSummary string  `json:"output_summary"`
	TokensIn      int     `json:"tokens_in"`
	TokensOut     int     `json:"tokens_out"`
	DurationMS    int     `json:"duration_ms"`
	CostUSD       float64 `json:"cost_usd"`
}

// NodeFailed is the node.failed data payload.
type NodeFailed struct {
	Error     string `json:"error"`
	WillRetry bool   `json:"will_retry"`
}

// NodeSkipped is the node.skipped data payload.
type NodeSkipped struct {
	Reason string `json:"reason"`
}

// CheckPassed is the check.passed data payload.
type CheckPassed struct {
	Check   string         `json:"check"`
	Details map[string]any `json:"details"`
}

// CheckError is one entry of check.failed errors.
type CheckError struct {
	RubricKey string `json:"rubric_key,omitempty"`
	Reason    string `json:"reason"`
	Quote     string `json:"quote,omitempty"`
}

// CheckFailed is the check.failed data payload.
type CheckFailed struct {
	Check  string       `json:"check"`
	Errors []CheckError `json:"errors"`
}

// LoopRetry is the loop.retry data payload.
type LoopRetry struct {
	Attempt     int    `json:"attempt"`
	MaxAttempts int    `json:"max_attempts"`
	Reason      string `json:"reason"`
	Feedback    string `json:"feedback"`
}

// EdgeTraversed is the edge.traversed data payload.
type EdgeTraversed struct {
	From  string `json:"from"`
	To    string `json:"to"`
	Label string `json:"label"`
}

// TopicChanged is the topic.changed data payload.
type TopicChanged struct {
	TopicID  string `json:"topic_id"`
	Position int    `json:"position"`
	Title    string `json:"title"`
	Status   string `json:"status"`
}

// MessageCompleted is the message.completed data payload.
type MessageCompleted struct {
	Message domain.Message `json:"message"`
}

// RunCompleted is the run.completed data payload.
type RunCompleted struct {
	Status           string  `json:"status"`
	FinishReason     string  `json:"finish_reason"`
	TotalTokensIn    int     `json:"total_tokens_in"`
	TotalTokensOut   int     `json:"total_tokens_out"`
	CostUSD          float64 `json:"cost_usd"`
	NeedsManualCount int     `json:"needs_manual_count"`
}

// RunFailed is the run.failed data payload.
type RunFailed struct {
	Error string `json:"error"`
}

// MessageDelta is the message.delta data payload (never persisted).
type MessageDelta struct {
	MessageID string `json:"message_id"`
	Delta     string `json:"delta"`
}

// ErrorEvent is the error data payload (session stream).
type ErrorEvent struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
}

// CurrentTopic is the current_topic field of session.updated.
type CurrentTopic struct {
	Position int    `json:"position"`
	Title    string `json:"title"`
	Total    int    `json:"total"`
}

// SessionUpdated is the session.updated data payload.
type SessionUpdated struct {
	Status        string        `json:"status"`
	DeadlineAt    *string       `json:"deadline_at"`
	QuestionCount int           `json:"question_count"`
	MaxQuestions  int           `json:"max_questions"`
	CurrentTopic  *CurrentTopic `json:"current_topic"`
}

// SessionEnded is the session.ended data payload.
type SessionEnded struct {
	FinishReason string `json:"finish_reason"`
}
