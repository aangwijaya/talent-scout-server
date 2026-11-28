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
	ID                    string            `json:"id"`
	Title                 string            `json:"title"`
	Description           string            `json:"description"`
	Rubric                []RubricCriterion `json:"rubric"`
	Status                JobStatus         `json:"status"`
	SpokenEnglishRequired bool              `json:"spoken_english_required"`
	CreatedAt             time.Time         `json:"created_at"`
	UpdatedAt             time.Time         `json:"updated_at"`
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
	Authenticity *Authenticity     `json:"authenticity"`
	Fluency      *Fluency          `json:"fluency"`
	CreatedAt    time.Time         `json:"created_at"`
}

type Session struct {
	ID               string        `json:"id"`
	ApplicationID    string        `json:"application_id"`
	TokenHash        string        `json:"token_hash"`
	Status           SessionStatus `json:"status"`
	ExpiresAt        time.Time     `json:"expires_at"`
	StartedAt        *time.Time    `json:"started_at"`
	EndedAt          *time.Time    `json:"ended_at"`
	FinishReason     *FinishReason `json:"finish_reason"`
	QuestionCount    int           `json:"question_count"`
	Voice            bool          `json:"voice"`
	RecordingConsent *bool         `json:"recording_consent"`
	CreatedAt        time.Time     `json:"created_at"`
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
	ID        string        `json:"id"`
	SessionID string        `json:"session_id"`
	TopicID   *string       `json:"topic_id"`
	Role      MessageRole   `json:"role"`
	Kind      MessageKind   `json:"kind"`
	Content   string        `json:"content"`
	Entry     *MessageEntry `json:"entry,omitempty"`
	Audio     *MessageAudio `json:"audio,omitempty"`
	CreatedAt time.Time     `json:"created_at"`
}

type MessageEntryMode string

const (
	MessageEntryModeTyped  MessageEntryMode = "typed"
	MessageEntryModeSpoken MessageEntryMode = "spoken"
)

type MessageEntryFallbackReason string

const (
	MessageEntryFallbackChoseText             MessageEntryFallbackReason = "chose_text"
	MessageEntryFallbackDeclinedRecording     MessageEntryFallbackReason = "declined_recording"
	MessageEntryFallbackMicrophoneUnavailable MessageEntryFallbackReason = "microphone_unavailable"
	MessageEntryFallbackTranscriptionFailed   MessageEntryFallbackReason = "transcription_failed"
)

// MessageEntry holds the client-recorded signals for an applicant answer. It is
// advisory: the authenticity node only explains it to the recruiter and never
// changes the interview flow or a score. The voice fields are meaningful only
// for a spoken answer; a typed answer in a voice session carries a fallback
// reason instead.
type MessageEntry struct {
	PastedChars    int                        `json:"pasted_chars"`
	TypedChars     int                        `json:"typed_chars"`
	DurationMS     int                        `json:"duration_ms"`
	Mode           MessageEntryMode           `json:"mode,omitempty"`
	FallbackReason MessageEntryFallbackReason `json:"fallback_reason,omitempty"`
	StartLatencyMS int                        `json:"start_latency_ms,omitempty"`
	SpeakingMS     int                        `json:"speaking_ms,omitempty"`
	RerecordCount  int                        `json:"rerecord_count,omitempty"`
}

type MessageAudioStatus string

const (
	MessageAudioStatusAvailable MessageAudioStatus = "available"
	MessageAudioStatusDeleted   MessageAudioStatus = "deleted"
	MessageAudioStatusNotStored MessageAudioStatus = "not_stored"
)

// MessageAudio is the recruiter-only recording metadata for a spoken answer.
type MessageAudio struct {
	DurationMS int                `json:"duration_ms"`
	Language   string             `json:"language"`
	Status     MessageAudioStatus `json:"status"`
}

type AuthenticityLevel string

const (
	AuthenticityLevelNone     AuthenticityLevel = "none"
	AuthenticityLevelPossible AuthenticityLevel = "possible"
	AuthenticityLevelLikely   AuthenticityLevel = "likely"
)

// Authenticity is the advisory answer-authenticity assessment shown to the
// recruiter. It is null until the authenticity node has run successfully.
type Authenticity struct {
	Level      AuthenticityLevel `json:"level"`
	Reasons    []string          `json:"reasons"`
	AssessedAt time.Time         `json:"assessed_at"`
}

// TranscriptWord is one word with its start and end time in seconds, as the
// speech-to-text provider reports it. Word timings are stored with the upload
// but never exposed through the API.
type TranscriptWord struct {
	Word  string  `json:"word"`
	Start float64 `json:"start"`
	End   float64 `json:"end"`
}

// Fluency is the advisory spoken-English assessment shown to the recruiter. The
// metrics are computed in code from word timings; the model only writes Summary.
// It is null for a text session, before the run ends, or when the check failed.
type Fluency struct {
	WordsPerMinute       float64   `json:"words_per_minute"`
	PauseRatio           float64   `json:"pause_ratio"`
	FillerRate           float64   `json:"filler_rate"`
	TranscriptConfidence float64   `json:"transcript_confidence"`
	Languages            []string  `json:"languages"`
	Summary              string    `json:"summary"`
	SpokenAnswers        int       `json:"spoken_answers"`
	TypedAnswers         int       `json:"typed_answers"`
	AssessedAt           time.Time `json:"assessed_at"`
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
	NodeID     *string         `json:"node_id"`
	Type       string          `json:"type"`
	Attempt    *int            `json:"attempt"`
	Model      *string         `json:"model"`
	TokensIn   int             `json:"tokens_in"`
	TokensOut  int             `json:"tokens_out"`
	DurationMS int             `json:"duration_ms"`
	Payload    json.RawMessage `json:"payload"`
	CreatedAt  time.Time       `json:"created_at"`
}

// StrPtr returns a pointer to s, for the nullable NodeEvent fields.
func StrPtr(s string) *string { return &s }

// IntPtr returns a pointer to i, for the nullable NodeEvent fields.
func IntPtr(i int) *int { return &i }

func NewID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return hex.EncodeToString(b[0:4]) + "-" + hex.EncodeToString(b[4:6]) + "-" + hex.EncodeToString(b[6:8]) + "-" + hex.EncodeToString(b[8:10]) + "-" + hex.EncodeToString(b[10:16])
}
