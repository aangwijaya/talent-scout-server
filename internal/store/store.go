package store

import (
	"context"
	"errors"
	"time"

	"github.com/aangwijaya/talent-scout-server/internal/domain"
)

var (
	ErrNotFound = errors.New("not found")
	ErrConflict = errors.New("conflict")
)

func defaultJobStatus(job *domain.Job) {
	if job.Status == "" {
		job.Status = domain.JobStatusOpen
	}
}

func defaultApplicationStatus(app *domain.Application) {
	if app.Status == "" {
		app.Status = domain.ApplicationStatusApplied
	}
}

func defaultSessionStatus(session *domain.Session) {
	if session.Status == "" {
		session.Status = domain.SessionStatusInvited
	}
}

func defaultTopicStatus(topic *domain.Topic) {
	if topic.Status == "" {
		topic.Status = domain.TopicStatusPending
	}
}

func defaultScoreStatus(score *domain.Score) {
	if score.Status == "" {
		score.Status = domain.ScoreStatusValid
	}
}

func defaultGraphRunStatus(run *domain.GraphRun) {
	if run.Status == "" {
		run.Status = domain.GraphRunStatusRunning
	}
}

// AudioUpload is the internal metadata for a recording in the private audio
// bucket. MessageID is nil until the upload is attached to an answer. Counted
// is false for a transcription the provider billed but the session rejected
// (no speech, too long, over budget): its cost still belongs to the run, but
// its duration does not consume the session's audio budget.
// The table is not exposed through the API; retention only reads deleted_at.
// CostUSD stays on this row because the graph runner rewrites graph_runs.cost_usd
// as a whole value; API read paths add the row sum back.
type AudioUpload struct {
	ID          string
	SessionID   string
	ObjectPath  string
	ContentType string
	SizeBytes   int
	DurationMS  int
	Transcript  string
	Language    string
	Words       []domain.TranscriptWord
	Confidence  float64
	CostUSD     float64
	Stored      bool
	Counted     bool
	MessageID   *string
	UploadedAt  time.Time
	DeletedAt   *time.Time
}

// AudioUsage is the per-session sum of recorded audio and its transcription
// cost. A re-recording counts too: the budget is for recorded audio, not sent
// answers.
type AudioUsage struct {
	DurationMS int
	CostUSD    float64
}

var (
	_ Store = (*Memory)(nil)
	_ Store = (*Postgres)(nil)
)

type Store interface {
	CreateJob(ctx context.Context, job domain.Job) error
	CreateJobWithinLimit(ctx context.Context, job domain.Job, limit int) error
	UpdateJob(ctx context.Context, job domain.Job) error
	UpdateJobRubricIfNoApplications(ctx context.Context, job domain.Job) error
	CountJobs(ctx context.Context) (int, error)
	GetJob(ctx context.Context, id string) (domain.Job, error)
	ListJobs(ctx context.Context) ([]domain.Job, error)

	CreateApplication(ctx context.Context, app domain.Application) error
	CreateApplicationWithSession(ctx context.Context, app domain.Application, session domain.Session) error
	GetApplication(ctx context.Context, id string) (domain.Application, error)
	GetApplicationByJobEmail(ctx context.Context, jobID, email string) (domain.Application, error)
	ListApplicationsByJob(ctx context.Context, jobID string, status *domain.ApplicationStatus) ([]domain.Application, error)
	CountApplicationsByJob(ctx context.Context, jobID string) (int, error)
	UpdateApplicationStatus(ctx context.Context, id string, status domain.ApplicationStatus) error
	SetApplicationDecision(ctx context.Context, id string, status domain.ApplicationStatus, note string, decidedAt time.Time) (domain.Application, error)
	SetApplicationAuthenticity(ctx context.Context, id string, authenticity *domain.Authenticity) error
	SetApplicationFluency(ctx context.Context, id string, fluency *domain.Fluency) error

	CreateSession(ctx context.Context, session domain.Session) error
	GetSession(ctx context.Context, id string) (domain.Session, error)
	GetSessionByTokenHash(ctx context.Context, tokenHash string) (domain.Session, error)
	GetLatestSessionByApplication(ctx context.Context, applicationID string) (domain.Session, error)
	ListSessionsByStatus(ctx context.Context, status domain.SessionStatus) ([]domain.Session, error)
	UpdateSession(ctx context.Context, session domain.Session) error
	StartSession(ctx context.Context, id string, startedAt time.Time) (domain.Session, error)
	StartSessionWithConsent(ctx context.Context, id string, startedAt time.Time, consent *bool) (domain.Session, error)
	ExpireSession(ctx context.Context, id string, now time.Time) (bool, error)
	AbandonSession(ctx context.Context, id string, now time.Time) (bool, error)
	FinishSession(ctx context.Context, id string, reason domain.FinishReason, now time.Time) (bool, error)
	ResendInvite(ctx context.Context, revokeSessionID *string, session domain.Session) error

	CreateTopics(ctx context.Context, topics []domain.Topic) error
	ListTopicsBySession(ctx context.Context, sessionID string) ([]domain.Topic, error)
	UpdateTopic(ctx context.Context, topic domain.Topic) error

	CreateMessage(ctx context.Context, message domain.Message) error
	CreateMessageWithAudioUpload(ctx context.Context, message domain.Message, audioUploadID string) error
	ListMessagesBySession(ctx context.Context, sessionID string, includeSystem bool) ([]domain.Message, error)

	CreateAudioUpload(ctx context.Context, upload AudioUpload) error
	GetAudioUpload(ctx context.Context, id string) (AudioUpload, error)
	GetAudioUsage(ctx context.Context, sessionID string) (AudioUsage, error)
	AttachAudioUpload(ctx context.Context, id, messageID string) error
	SetAudioUploadStored(ctx context.Context, id string, stored bool) error
	ListAudioUploadsBySession(ctx context.Context, sessionID string) ([]AudioUpload, error)
	ListAudioUploadsDue(ctx context.Context, sentBefore, unsentBefore time.Time) ([]AudioUpload, error)
	MarkAudioUploadDeleted(ctx context.Context, id string, deletedAt time.Time) error

	UpsertScores(ctx context.Context, scores []domain.Score) error
	ListScoresByApplication(ctx context.Context, applicationID string) ([]domain.Score, error)

	CreateGraphRun(ctx context.Context, run domain.GraphRun) error
	GetGraphRun(ctx context.Context, id string) (domain.GraphRun, error)
	GetGraphRunBySession(ctx context.Context, sessionID string) (domain.GraphRun, error)
	ListGraphRunsByStatus(ctx context.Context, status domain.GraphRunStatus) ([]domain.GraphRun, error)
	UpdateGraphRun(ctx context.Context, run domain.GraphRun) error

	AppendNodeEvent(ctx context.Context, event domain.NodeEvent) (domain.NodeEvent, error)
	ListNodeEvents(ctx context.Context, runID string, sinceSeq int64) ([]domain.NodeEvent, error)
}
