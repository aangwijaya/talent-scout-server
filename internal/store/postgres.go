package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"

	"github.com/aangwijaya/talent-scout-server/internal/domain"
)

type Postgres struct {
	pool *pgxpool.Pool
}

func NewPostgres(pool *pgxpool.Pool) *Postgres {
	return &Postgres{pool: pool}
}

func (p *Postgres) CreateJob(ctx context.Context, job domain.Job) error {
	defaultJobStatus(&job)
	rubric, err := json.Marshal(job.Rubric)
	if err != nil {
		return fmt.Errorf("marshal rubric: %w", err)
	}
	now := time.Now().UTC()
	if job.CreatedAt.IsZero() {
		job.CreatedAt = now
	}
	if job.UpdatedAt.IsZero() {
		job.UpdatedAt = job.CreatedAt
	}
	_, err = p.pool.Exec(ctx, `
		INSERT INTO jobs (id, title, description, rubric, status, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		job.ID, job.Title, job.Description, string(rubric), job.Status, job.CreatedAt, job.UpdatedAt)
	return mapError(err)
}

func (p *Postgres) GetJob(ctx context.Context, id string) (domain.Job, error) {
	row := p.pool.QueryRow(ctx, `SELECT `+jobColumns+` FROM jobs WHERE id = $1`, id)
	job, err := scanJob(row)
	if err != nil {
		return domain.Job{}, mapError(err)
	}
	return job, nil
}

func (p *Postgres) ListJobs(ctx context.Context) ([]domain.Job, error) {
	rows, err := p.pool.Query(ctx, `SELECT `+jobColumns+` FROM jobs ORDER BY created_at, id`)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()

	jobs := make([]domain.Job, 0)
	for rows.Next() {
		job, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	return jobs, mapError(rows.Err())
}

func (p *Postgres) CreateApplication(ctx context.Context, app domain.Application) error {
	defaultApplicationStatus(&app)
	if app.CreatedAt.IsZero() {
		app.CreatedAt = time.Now().UTC()
	}
	_, err := p.pool.Exec(ctx, `
		INSERT INTO applications (id, job_id, name, email, cv_object_path, cv_text, status, decision_note, decided_at, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		app.ID, app.JobID, app.Name, app.Email, app.CVObjectPath, app.CVText, app.Status, app.DecisionNote, app.DecidedAt, app.CreatedAt)
	return mapError(err)
}

// CreateApplicationWithSession inserts an application and its first session in
// one transaction so a failed session insert rolls back the application and an
// applicant is never left with an application but no session.
func (p *Postgres) CreateApplicationWithSession(ctx context.Context, app domain.Application, session domain.Session) error {
	if app.Status == "" {
		app.Status = domain.ApplicationStatusInvited
	}
	if session.Status == "" {
		session.Status = domain.SessionStatusInvited
	}
	now := time.Now().UTC()
	if app.CreatedAt.IsZero() {
		app.CreatedAt = now
	}
	if session.CreatedAt.IsZero() {
		session.CreatedAt = app.CreatedAt
	}

	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `
		INSERT INTO applications (id, job_id, name, email, cv_object_path, cv_text, status, decision_note, decided_at, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		app.ID, app.JobID, app.Name, app.Email, app.CVObjectPath, app.CVText, app.Status, app.DecisionNote, app.DecidedAt, app.CreatedAt); err != nil {
		return mapError(err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO sessions (id, application_id, token_hash, status, expires_at, started_at, ended_at, finish_reason, question_count, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		session.ID, session.ApplicationID, session.TokenHash, session.Status, session.ExpiresAt,
		session.StartedAt, session.EndedAt, session.FinishReason, session.QuestionCount, session.CreatedAt); err != nil {
		return mapError(err)
	}
	return tx.Commit(ctx)
}

func (p *Postgres) GetApplication(ctx context.Context, id string) (domain.Application, error) {
	row := p.pool.QueryRow(ctx, `SELECT `+applicationColumns+` FROM applications WHERE id = $1`, id)
	app, err := scanApplication(row)
	if err != nil {
		return domain.Application{}, mapError(err)
	}
	return app, nil
}

func (p *Postgres) GetApplicationByJobEmail(ctx context.Context, jobID, email string) (domain.Application, error) {
	row := p.pool.QueryRow(ctx, `SELECT `+applicationColumns+` FROM applications WHERE job_id = $1 AND email = $2`, jobID, email)
	app, err := scanApplication(row)
	if err != nil {
		return domain.Application{}, mapError(err)
	}
	return app, nil
}

func (p *Postgres) ListApplicationsByJob(ctx context.Context, jobID string, status *domain.ApplicationStatus) ([]domain.Application, error) {
	query := `SELECT ` + applicationColumns + ` FROM applications WHERE job_id = $1`
	args := []any{jobID}
	if status != nil {
		query += ` AND status = $2`
		args = append(args, *status)
	}
	query += ` ORDER BY created_at, id`

	rows, err := p.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()

	apps := make([]domain.Application, 0)
	for rows.Next() {
		app, err := scanApplication(rows)
		if err != nil {
			return nil, err
		}
		apps = append(apps, app)
	}
	return apps, mapError(rows.Err())
}

func (p *Postgres) UpdateApplicationStatus(ctx context.Context, id string, status domain.ApplicationStatus) error {
	tag, err := p.pool.Exec(ctx, `UPDATE applications SET status = $2 WHERE id = $1`, id, status)
	if err != nil {
		return mapError(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (p *Postgres) SetApplicationDecision(ctx context.Context, id string, status domain.ApplicationStatus, note string, decidedAt time.Time) (domain.Application, error) {
	row := p.pool.QueryRow(ctx, `
		UPDATE applications SET status = $2, decision_note = $3, decided_at = $4
		WHERE id = $1
		RETURNING `+applicationColumns,
		id, status, note, decidedAt)
	app, err := scanApplication(row)
	if err != nil {
		return domain.Application{}, mapError(err)
	}
	return app, nil
}

func (p *Postgres) CreateSession(ctx context.Context, session domain.Session) error {
	defaultSessionStatus(&session)
	if session.CreatedAt.IsZero() {
		session.CreatedAt = time.Now().UTC()
	}
	_, err := p.pool.Exec(ctx, `
		INSERT INTO sessions (id, application_id, token_hash, status, expires_at, started_at, ended_at, finish_reason, question_count, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		session.ID, session.ApplicationID, session.TokenHash, session.Status, session.ExpiresAt,
		session.StartedAt, session.EndedAt, session.FinishReason, session.QuestionCount, session.CreatedAt)
	return mapError(err)
}

func (p *Postgres) GetSession(ctx context.Context, id string) (domain.Session, error) {
	row := p.pool.QueryRow(ctx, `SELECT `+sessionColumns+` FROM sessions WHERE id = $1`, id)
	session, err := scanSession(row)
	if err != nil {
		return domain.Session{}, mapError(err)
	}
	return session, nil
}

func (p *Postgres) GetSessionByTokenHash(ctx context.Context, tokenHash string) (domain.Session, error) {
	row := p.pool.QueryRow(ctx, `SELECT `+sessionColumns+` FROM sessions WHERE token_hash = $1`, tokenHash)
	session, err := scanSession(row)
	if err != nil {
		return domain.Session{}, mapError(err)
	}
	return session, nil
}

func (p *Postgres) GetLatestSessionByApplication(ctx context.Context, applicationID string) (domain.Session, error) {
	row := p.pool.QueryRow(ctx, `
		SELECT `+sessionColumns+` FROM sessions
		WHERE application_id = $1
		ORDER BY created_at DESC, id DESC
		LIMIT 1`, applicationID)
	session, err := scanSession(row)
	if err != nil {
		return domain.Session{}, mapError(err)
	}
	return session, nil
}

func (p *Postgres) ListSessionsByStatus(ctx context.Context, status domain.SessionStatus) ([]domain.Session, error) {
	rows, err := p.pool.Query(ctx, `SELECT `+sessionColumns+` FROM sessions WHERE status = $1 ORDER BY created_at, id`, status)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()

	sessions := make([]domain.Session, 0)
	for rows.Next() {
		session, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		sessions = append(sessions, session)
	}
	return sessions, mapError(rows.Err())
}

func (p *Postgres) UpdateSession(ctx context.Context, session domain.Session) error {
	tag, err := p.pool.Exec(ctx, `
		UPDATE sessions SET
			application_id = $2, token_hash = $3, status = $4, expires_at = $5,
			started_at = $6, ended_at = $7, finish_reason = $8, question_count = $9
		WHERE id = $1`,
		session.ID, session.ApplicationID, session.TokenHash, session.Status, session.ExpiresAt,
		session.StartedAt, session.EndedAt, session.FinishReason, session.QuestionCount)
	if err != nil {
		return mapError(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// StartSession atomically transitions a session from invited to active, so a
// concurrent second start cannot both succeed. The conditional UPDATE returns
// no row when the session is not in the invited state; a follow-up lookup
// distinguishes an unknown session (ErrNotFound) from one already started
// (ErrConflict). In the same transaction the application moves from invited to
// in_session.
func (p *Postgres) StartSession(ctx context.Context, id string, startedAt time.Time) (domain.Session, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return domain.Session{}, err
	}
	defer tx.Rollback(ctx)

	row := tx.QueryRow(ctx, `
		UPDATE sessions SET status = $2, started_at = $3
		WHERE id = $1 AND status = $4
		RETURNING `+sessionColumns,
		id, domain.SessionStatusActive, startedAt, domain.SessionStatusInvited)
	session, err := scanSession(row)
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			return domain.Session{}, mapError(err)
		}
		var status domain.SessionStatus
		err = tx.QueryRow(ctx, `SELECT status FROM sessions WHERE id = $1`, id).Scan(&status)
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Session{}, ErrNotFound
		}
		if err != nil {
			return domain.Session{}, mapError(err)
		}
		return domain.Session{}, ErrConflict
	}

	tag, err := tx.Exec(ctx, `
		UPDATE applications SET status = $2
		WHERE id = $1 AND status = $3`,
		session.ApplicationID, domain.ApplicationStatusInSession, domain.ApplicationStatusInvited)
	if err != nil {
		return domain.Session{}, mapError(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.Session{}, ErrConflict
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Session{}, mapError(err)
	}
	return session, nil
}

// ExpireSession transitions an invited session that is already past its expiry
// to expired, and reports whether the transition happened. A session that has
// since changed state is skipped.
func (p *Postgres) ExpireSession(ctx context.Context, id string, now time.Time) (bool, error) {
	tag, err := p.pool.Exec(ctx, `
		UPDATE sessions SET status = $2
		WHERE id = $1 AND status = $3 AND expires_at <= $4`,
		id, domain.SessionStatusExpired, domain.SessionStatusInvited, now)
	if err != nil {
		return false, mapError(err)
	}
	return tag.RowsAffected() > 0, nil
}

// AbandonSession transitions an active session to completed with
// finish_reason=abandoned, and reports whether the transition happened. A
// session that is no longer active is skipped.
func (p *Postgres) AbandonSession(ctx context.Context, id string, now time.Time) (bool, error) {
	tag, err := p.pool.Exec(ctx, `
		UPDATE sessions SET status = $2, finish_reason = $3, ended_at = $4
		WHERE id = $1 AND status = $5`,
		id, domain.SessionStatusCompleted, domain.FinishReasonAbandoned, now, domain.SessionStatusActive)
	if err != nil {
		return false, mapError(err)
	}
	return tag.RowsAffected() > 0, nil
}

func (p *Postgres) CreateTopics(ctx context.Context, topics []domain.Topic) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	for _, topic := range topics {
		defaultTopicStatus(&topic)
		keys, err := json.Marshal(topic.RubricKeys)
		if err != nil {
			return fmt.Errorf("marshal rubric keys: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO topics (id, session_id, position, title, goal, rubric_keys, seed_question, status, follow_up_count)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
			topic.ID, topic.SessionID, topic.Position, topic.Title, topic.Goal, string(keys),
			topic.SeedQuestion, topic.Status, topic.FollowUpCount); err != nil {
			return mapError(err)
		}
	}
	return tx.Commit(ctx)
}

func (p *Postgres) ListTopicsBySession(ctx context.Context, sessionID string) ([]domain.Topic, error) {
	rows, err := p.pool.Query(ctx, `SELECT `+topicColumns+` FROM topics WHERE session_id = $1 ORDER BY position, id`, sessionID)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()

	topics := make([]domain.Topic, 0)
	for rows.Next() {
		topic, err := scanTopic(rows)
		if err != nil {
			return nil, err
		}
		topics = append(topics, topic)
	}
	return topics, mapError(rows.Err())
}

func (p *Postgres) UpdateTopic(ctx context.Context, topic domain.Topic) error {
	keys, err := json.Marshal(topic.RubricKeys)
	if err != nil {
		return fmt.Errorf("marshal rubric keys: %w", err)
	}
	tag, err := p.pool.Exec(ctx, `
		UPDATE topics SET
			position = $2, title = $3, goal = $4, rubric_keys = $5,
			seed_question = $6, status = $7, follow_up_count = $8
		WHERE id = $1`,
		topic.ID, topic.Position, topic.Title, topic.Goal, string(keys),
		topic.SeedQuestion, topic.Status, topic.FollowUpCount)
	if err != nil {
		return mapError(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (p *Postgres) CreateMessage(ctx context.Context, message domain.Message) error {
	if message.CreatedAt.IsZero() {
		message.CreatedAt = time.Now().UTC()
	}
	_, err := p.pool.Exec(ctx, `
		INSERT INTO messages (id, session_id, topic_id, role, kind, content, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		message.ID, message.SessionID, message.TopicID, message.Role, message.Kind, message.Content, message.CreatedAt)
	return mapError(err)
}

func (p *Postgres) ListMessagesBySession(ctx context.Context, sessionID string, includeSystem bool) ([]domain.Message, error) {
	query := `SELECT ` + messageColumns + ` FROM messages WHERE session_id = $1`
	if !includeSystem {
		query += ` AND role <> 'system'`
	}
	query += ` ORDER BY created_at, id`

	rows, err := p.pool.Query(ctx, query, sessionID)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()

	messages := make([]domain.Message, 0)
	for rows.Next() {
		message, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		messages = append(messages, message)
	}
	return messages, mapError(rows.Err())
}

func (p *Postgres) UpsertScores(ctx context.Context, scores []domain.Score) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	now := time.Now().UTC()
	for _, score := range scores {
		defaultScoreStatus(&score)
		if score.ID == "" {
			score.ID = domain.NewID()
		}
		if score.Attempts == 0 {
			score.Attempts = 1
		}
		if score.CreatedAt.IsZero() {
			score.CreatedAt = now
		}
		evidence, err := json.Marshal(score.Evidence)
		if err != nil {
			return fmt.Errorf("marshal evidence: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO scores (id, application_id, rubric_key, score, rationale, evidence, status, attempts, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
			ON CONFLICT (application_id, rubric_key) DO UPDATE SET
				score = EXCLUDED.score, rationale = EXCLUDED.rationale, evidence = EXCLUDED.evidence,
				status = EXCLUDED.status, attempts = EXCLUDED.attempts, created_at = EXCLUDED.created_at`,
			score.ID, score.ApplicationID, score.RubricKey, score.Score, score.Rationale,
			string(evidence), score.Status, score.Attempts, score.CreatedAt); err != nil {
			return mapError(err)
		}
	}
	return tx.Commit(ctx)
}

func (p *Postgres) ListScoresByApplication(ctx context.Context, applicationID string) ([]domain.Score, error) {
	rows, err := p.pool.Query(ctx, `SELECT `+scoreColumns+` FROM scores WHERE application_id = $1 ORDER BY created_at, rubric_key`, applicationID)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()

	scores := make([]domain.Score, 0)
	for rows.Next() {
		score, err := scanScore(rows)
		if err != nil {
			return nil, err
		}
		scores = append(scores, score)
	}
	return scores, mapError(rows.Err())
}

func (p *Postgres) CreateGraphRun(ctx context.Context, run domain.GraphRun) error {
	defaultGraphRunStatus(&run)
	if run.StartedAt.IsZero() {
		run.StartedAt = time.Now().UTC()
	}
	_, err := p.pool.Exec(ctx, `
		INSERT INTO graph_runs (id, session_id, status, started_at, ended_at, total_tokens_in, total_tokens_out, cost_usd, error)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		run.ID, run.SessionID, run.Status, run.StartedAt, run.EndedAt,
		run.TotalTokensIn, run.TotalTokensOut, run.CostUSD, run.Error)
	return mapError(err)
}

func (p *Postgres) GetGraphRun(ctx context.Context, id string) (domain.GraphRun, error) {
	row := p.pool.QueryRow(ctx, `SELECT `+graphRunColumns+` FROM graph_runs WHERE id = $1`, id)
	run, err := scanGraphRun(row)
	if err != nil {
		return domain.GraphRun{}, mapError(err)
	}
	return run, nil
}

func (p *Postgres) GetGraphRunBySession(ctx context.Context, sessionID string) (domain.GraphRun, error) {
	row := p.pool.QueryRow(ctx, `
		SELECT `+graphRunColumns+` FROM graph_runs
		WHERE session_id = $1
		ORDER BY started_at DESC, id DESC
		LIMIT 1`, sessionID)
	run, err := scanGraphRun(row)
	if err != nil {
		return domain.GraphRun{}, mapError(err)
	}
	return run, nil
}

func (p *Postgres) UpdateGraphRun(ctx context.Context, run domain.GraphRun) error {
	tag, err := p.pool.Exec(ctx, `
		UPDATE graph_runs SET
			status = $2, ended_at = $3, total_tokens_in = $4,
			total_tokens_out = $5, cost_usd = $6, error = $7
		WHERE id = $1`,
		run.ID, run.Status, run.EndedAt, run.TotalTokensIn,
		run.TotalTokensOut, run.CostUSD, run.Error)
	if err != nil {
		return mapError(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (p *Postgres) AppendNodeEvent(ctx context.Context, event domain.NodeEvent) (domain.NodeEvent, error) {
	if event.ID == "" {
		event.ID = domain.NewID()
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now().UTC()
	}
	payload := string(event.Payload)
	if payload == "" {
		payload = "{}"
	}

	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return domain.NodeEvent{}, err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1)::bigint)`, event.RunID); err != nil {
		return domain.NodeEvent{}, mapError(err)
	}
	if err := tx.QueryRow(ctx,
		`SELECT COALESCE(MAX(seq), 0) + 1 FROM node_events WHERE run_id = $1`,
		event.RunID).Scan(&event.Seq); err != nil {
		return domain.NodeEvent{}, mapError(err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO node_events (id, run_id, seq, node_id, type, attempt, model, tokens_in, tokens_out, duration_ms, payload, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
		event.ID, event.RunID, event.Seq, event.NodeID, event.Type, event.Attempt,
		event.Model, event.TokensIn, event.TokensOut, event.DurationMS, payload, event.CreatedAt); err != nil {
		return domain.NodeEvent{}, mapError(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.NodeEvent{}, mapError(err)
	}
	return event, nil
}

func (p *Postgres) ListNodeEvents(ctx context.Context, runID string, sinceSeq int64) ([]domain.NodeEvent, error) {
	rows, err := p.pool.Query(ctx, `
		SELECT `+nodeEventColumns+` FROM node_events
		WHERE run_id = $1 AND seq > $2
		ORDER BY seq`, runID, sinceSeq)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()

	events := make([]domain.NodeEvent, 0)
	for rows.Next() {
		event, err := scanNodeEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, mapError(rows.Err())
}

const (
	jobColumns         = `id, title, description, rubric, status, created_at, updated_at`
	applicationColumns = `id, job_id, name, email, cv_object_path, cv_text, status, decision_note, decided_at, created_at`
	sessionColumns     = `id, application_id, token_hash, status, expires_at, started_at, ended_at, finish_reason, question_count, created_at`
	topicColumns       = `id, session_id, position, title, goal, rubric_keys, seed_question, status, follow_up_count`
	messageColumns     = `id, session_id, topic_id, role, kind, content, created_at`
	scoreColumns       = `id, application_id, rubric_key, score, rationale, evidence, status, attempts, created_at`
	graphRunColumns    = `id, session_id, status, started_at, ended_at, total_tokens_in, total_tokens_out, cost_usd::double precision, error`
	nodeEventColumns   = `id, run_id, seq, node_id, type, attempt, model, tokens_in, tokens_out, duration_ms, payload, created_at`
)

type rowScanner interface {
	Scan(dest ...any) error
}

func scanJob(row rowScanner) (domain.Job, error) {
	var job domain.Job
	var rubric []byte
	if err := row.Scan(&job.ID, &job.Title, &job.Description, &rubric, &job.Status, &job.CreatedAt, &job.UpdatedAt); err != nil {
		return domain.Job{}, err
	}
	if err := json.Unmarshal(rubric, &job.Rubric); err != nil {
		return domain.Job{}, fmt.Errorf("unmarshal rubric: %w", err)
	}
	return job, nil
}

func scanApplication(row rowScanner) (domain.Application, error) {
	var app domain.Application
	if err := row.Scan(&app.ID, &app.JobID, &app.Name, &app.Email, &app.CVObjectPath, &app.CVText,
		&app.Status, &app.DecisionNote, &app.DecidedAt, &app.CreatedAt); err != nil {
		return domain.Application{}, err
	}
	return app, nil
}

func scanSession(row rowScanner) (domain.Session, error) {
	var session domain.Session
	var finishReason *string
	if err := row.Scan(&session.ID, &session.ApplicationID, &session.TokenHash, &session.Status, &session.ExpiresAt,
		&session.StartedAt, &session.EndedAt, &finishReason, &session.QuestionCount, &session.CreatedAt); err != nil {
		return domain.Session{}, err
	}
	if finishReason != nil {
		reason := domain.FinishReason(*finishReason)
		session.FinishReason = &reason
	}
	return session, nil
}

func scanTopic(row rowScanner) (domain.Topic, error) {
	var topic domain.Topic
	var keys []byte
	if err := row.Scan(&topic.ID, &topic.SessionID, &topic.Position, &topic.Title, &topic.Goal, &keys,
		&topic.SeedQuestion, &topic.Status, &topic.FollowUpCount); err != nil {
		return domain.Topic{}, err
	}
	if err := json.Unmarshal(keys, &topic.RubricKeys); err != nil {
		return domain.Topic{}, fmt.Errorf("unmarshal rubric keys: %w", err)
	}
	return topic, nil
}

func scanMessage(row rowScanner) (domain.Message, error) {
	var message domain.Message
	if err := row.Scan(&message.ID, &message.SessionID, &message.TopicID, &message.Role, &message.Kind,
		&message.Content, &message.CreatedAt); err != nil {
		return domain.Message{}, err
	}
	return message, nil
}

func scanScore(row rowScanner) (domain.Score, error) {
	var score domain.Score
	var evidence []byte
	if err := row.Scan(&score.ID, &score.ApplicationID, &score.RubricKey, &score.Score, &score.Rationale,
		&evidence, &score.Status, &score.Attempts, &score.CreatedAt); err != nil {
		return domain.Score{}, err
	}
	if err := json.Unmarshal(evidence, &score.Evidence); err != nil {
		return domain.Score{}, fmt.Errorf("unmarshal evidence: %w", err)
	}
	return score, nil
}

func scanGraphRun(row rowScanner) (domain.GraphRun, error) {
	var run domain.GraphRun
	if err := row.Scan(&run.ID, &run.SessionID, &run.Status, &run.StartedAt, &run.EndedAt,
		&run.TotalTokensIn, &run.TotalTokensOut, &run.CostUSD, &run.Error); err != nil {
		return domain.GraphRun{}, err
	}
	return run, nil
}

func scanNodeEvent(row rowScanner) (domain.NodeEvent, error) {
	var event domain.NodeEvent
	var payload []byte
	if err := row.Scan(&event.ID, &event.RunID, &event.Seq, &event.NodeID, &event.Type, &event.Attempt,
		&event.Model, &event.TokensIn, &event.TokensOut, &event.DurationMS, &payload, &event.CreatedAt); err != nil {
		return domain.NodeEvent{}, err
	}
	event.Payload = payload
	return event, nil
}

func mapError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return ErrConflict
	}
	return err
}

func normalizeDSN(dsn string) (string, bool) {
	dsn = strings.TrimSpace(dsn)
	u, err := url.Parse(dsn)
	if err != nil || u.Scheme == "" {
		return dsn, false
	}
	queue := u.Query()
	pgbouncer := queue.Get("pgbouncer") == "true" || u.Port() == "6543"
	if _, ok := queue["pgbouncer"]; ok {
		queue.Del("pgbouncer")
		u.RawQuery = queue.Encode()
		dsn = u.String()
	}
	return dsn, pgbouncer
}

func OpenPool(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	dsn, simple := normalizeDSN(dsn)
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse database url: %w", err)
	}
	if simple && cfg.ConnConfig.DefaultQueryExecMode == pgx.QueryExecModeCacheStatement {
		cfg.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	}
	cfg.MaxConns = 5
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("open database pool: %w", err)
	}
	return pool, nil
}

func OpenSQLDB(dsn string) (*sql.DB, error) {
	dsn, simple := normalizeDSN(dsn)
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse database url: %w", err)
	}
	if simple && cfg.DefaultQueryExecMode == pgx.QueryExecModeCacheStatement {
		cfg.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	}
	db := stdlib.OpenDB(*cfg)
	db.SetMaxOpenConns(5)
	return db, nil
}
