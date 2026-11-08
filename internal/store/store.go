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

var (
	_ Store = (*Memory)(nil)
	_ Store = (*Postgres)(nil)
)

type Store interface {
	CreateJob(ctx context.Context, job domain.Job) error
	GetJob(ctx context.Context, id string) (domain.Job, error)
	ListJobs(ctx context.Context) ([]domain.Job, error)

	CreateApplication(ctx context.Context, app domain.Application) error
	GetApplication(ctx context.Context, id string) (domain.Application, error)
	GetApplicationByJobEmail(ctx context.Context, jobID, email string) (domain.Application, error)
	ListApplicationsByJob(ctx context.Context, jobID string, status *domain.ApplicationStatus) ([]domain.Application, error)
	UpdateApplicationStatus(ctx context.Context, id string, status domain.ApplicationStatus) error
	SetApplicationDecision(ctx context.Context, id string, status domain.ApplicationStatus, note string, decidedAt time.Time) (domain.Application, error)

	CreateSession(ctx context.Context, session domain.Session) error
	GetSession(ctx context.Context, id string) (domain.Session, error)
	GetSessionByTokenHash(ctx context.Context, tokenHash string) (domain.Session, error)
	GetLatestSessionByApplication(ctx context.Context, applicationID string) (domain.Session, error)
	ListSessionsByStatus(ctx context.Context, status domain.SessionStatus) ([]domain.Session, error)
	UpdateSession(ctx context.Context, session domain.Session) error
	StartSession(ctx context.Context, id string, startedAt time.Time) (domain.Session, error)

	CreateTopics(ctx context.Context, topics []domain.Topic) error
	ListTopicsBySession(ctx context.Context, sessionID string) ([]domain.Topic, error)
	UpdateTopic(ctx context.Context, topic domain.Topic) error

	CreateMessage(ctx context.Context, message domain.Message) error
	ListMessagesBySession(ctx context.Context, sessionID string, includeSystem bool) ([]domain.Message, error)

	UpsertScores(ctx context.Context, scores []domain.Score) error
	ListScoresByApplication(ctx context.Context, applicationID string) ([]domain.Score, error)

	CreateGraphRun(ctx context.Context, run domain.GraphRun) error
	GetGraphRun(ctx context.Context, id string) (domain.GraphRun, error)
	GetGraphRunBySession(ctx context.Context, sessionID string) (domain.GraphRun, error)
	UpdateGraphRun(ctx context.Context, run domain.GraphRun) error

	AppendNodeEvent(ctx context.Context, event domain.NodeEvent) (domain.NodeEvent, error)
	ListNodeEvents(ctx context.Context, runID string, sinceSeq int64) ([]domain.NodeEvent, error)
}
