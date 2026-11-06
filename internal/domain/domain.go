package domain

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"time"
)

type JobStatus string

const (
	JobStatusOpen   JobStatus = "open"
	JobStatusClosed JobStatus = "closed"
)

type ApplicationStatus string

const (
	ApplicationStatusApplied     ApplicationStatus = "applied"
	ApplicationStatusInvited     ApplicationStatus = "invited"
	ApplicationStatusInSession   ApplicationStatus = "in_session"
	ApplicationStatusScoring     ApplicationStatus = "scoring"
	ApplicationStatusReview      ApplicationStatus = "review"
	ApplicationStatusShortlisted ApplicationStatus = "shortlisted"
	ApplicationStatusRejected    ApplicationStatus = "rejected"
	ApplicationStatusFailed      ApplicationStatus = "failed"
)

type SessionStatus string

const (
	SessionStatusInvited   SessionStatus = "invited"
	SessionStatusActive    SessionStatus = "active"
	SessionStatusCompleted SessionStatus = "completed"
	SessionStatusExpired   SessionStatus = "expired"
	SessionStatusRevoked   SessionStatus = "revoked"
)

type FinishReason string

const (
	FinishReasonCompleted     FinishReason = "completed"
	FinishReasonTimeLimit     FinishReason = "time_limit"
	FinishReasonQuestionLimit FinishReason = "question_limit"
	FinishReasonAbandoned     FinishReason = "abandoned"
	FinishReasonFailed        FinishReason = "failed"
)

type TopicStatus string

const (
	TopicStatusPending TopicStatus = "pending"
	TopicStatusActive  TopicStatus = "active"
	TopicStatusDone    TopicStatus = "done"
)

type MessageRole string

const (
	MessageRoleAssistant MessageRole = "assistant"
	MessageRoleApplicant MessageRole = "applicant"
	MessageRoleSystem    MessageRole = "system"
)

type MessageKind string

const (
	MessageKindOpening  MessageKind = "opening"
	MessageKindQuestion MessageKind = "question"
	MessageKindFollowUp MessageKind = "follow_up"
	MessageKindAnswer   MessageKind = "answer"
	MessageKindClosing  MessageKind = "closing"
)

type ScoreStatus string

const (
	ScoreStatusValid       ScoreStatus = "valid"
	ScoreStatusNeedsManual ScoreStatus = "needs_manual"
)

type GraphRunStatus string

const (
	GraphRunStatusRunning   GraphRunStatus = "running"
	GraphRunStatusSucceeded GraphRunStatus = "succeeded"
	GraphRunStatusFailed    GraphRunStatus = "failed"
)

type RubricCriterion struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Description string `json:"description"`
	Weight      int    `json:"weight"`
}

type Job struct {
	ID          string            `json:"id"`
	Title       string            `json:"title"`
	Description string            `json:"description"`
	Rubric      []RubricCriterion `json:"rubric"`
	Status      JobStatus         `json:"status"`
	CreatedAt   time.Time         `json:"created_at"`
	UpdatedAt   time.Time         `json:"updated_at"`
}

type Application struct {
	ID           string            `json:"id"`
	JobID        string            `json:"job_id"`
	Name         string            `json:"name"`
	Email        string            `json:"email"`
	CVObjectPath string            `json:"cv_object_path"`
	CVText       string            `json:"cv_text"`
	Status       ApplicationStatus `json:"status"`
	DecisionNote string            `json:"decision_note"`
	DecidedAt    *time.Time        `json:"decided_at"`
	CreatedAt    time.Time         `json:"created_at"`
}

type Session struct {
	ID            string        `json:"id"`
	ApplicationID string        `json:"application_id"`
	TokenHash     string        `json:"token_hash"`
	Status        SessionStatus `json:"status"`
	ExpiresAt     time.Time     `json:"expires_at"`
	StartedAt     *time.Time    `json:"started_at"`
	EndedAt       *time.Time    `json:"ended_at"`
	FinishReason  *FinishReason `json:"finish_reason"`
	QuestionCount int           `json:"question_count"`
	CreatedAt     time.Time     `json:"created_at"`
}

type Topic struct {
	ID            string      `json:"id"`
	SessionID     string      `json:"session_id"`
	Position      int         `json:"position"`
	Title         string      `json:"title"`
	Goal          string      `json:"goal"`
	RubricKeys    []string    `json:"rubric_keys"`
	SeedQuestion  string      `json:"seed_question"`
	Status        TopicStatus `json:"status"`
	FollowUpCount int         `json:"follow_up_count"`
}

type Message struct {
	ID        string      `json:"id"`
	SessionID string      `json:"session_id"`
	TopicID   *string     `json:"topic_id"`
	Role      MessageRole `json:"role"`
	Kind      MessageKind `json:"kind"`
	Content   string      `json:"content"`
	CreatedAt time.Time   `json:"created_at"`
}

type ScoreEvidence struct {
	MessageID string `json:"message_id"`
	Quote     string `json:"quote"`
}

type Score struct {
	ID            string          `json:"id"`
	ApplicationID string          `json:"application_id"`
	RubricKey     string          `json:"rubric_key"`
	Score         *int            `json:"score"`
	Rationale     string          `json:"rationale"`
	Evidence      []ScoreEvidence `json:"evidence"`
	Status        ScoreStatus     `json:"status"`
	Attempts      int             `json:"attempts"`
	CreatedAt     time.Time       `json:"created_at"`
}

type GraphRun struct {
	ID             string         `json:"id"`
	SessionID      string         `json:"session_id"`
	Status         GraphRunStatus `json:"status"`
	StartedAt      time.Time      `json:"started_at"`
	EndedAt        *time.Time     `json:"ended_at"`
	TotalTokensIn  int            `json:"total_tokens_in"`
	TotalTokensOut int            `json:"total_tokens_out"`
	CostUSD        float64        `json:"cost_usd"`
	Error          *string        `json:"error"`
}

type NodeEvent struct {
	ID         string          `json:"id"`
	RunID      string          `json:"run_id"`
	Seq        int64           `json:"seq"`
	NodeID     string          `json:"node_id"`
	Type       string          `json:"type"`
	Attempt    int             `json:"attempt"`
	Model      *string         `json:"model"`
	TokensIn   int             `json:"tokens_in"`
	TokensOut  int             `json:"tokens_out"`
	DurationMS int             `json:"duration_ms"`
	Payload    json.RawMessage `json:"payload"`
	CreatedAt  time.Time       `json:"created_at"`
}

func NewID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return hex.EncodeToString(b[0:4]) + "-" + hex.EncodeToString(b[4:6]) + "-" + hex.EncodeToString(b[6:8]) + "-" + hex.EncodeToString(b[8:10]) + "-" + hex.EncodeToString(b[10:16])
}
