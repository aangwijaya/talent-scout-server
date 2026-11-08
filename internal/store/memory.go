package store

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/aangwijaya/talent-scout-server/internal/domain"
)

type Memory struct {
	mu           sync.RWMutex
	jobs         map[string]domain.Job
	applications map[string]domain.Application
	sessions     map[string]domain.Session
	topics       map[string]domain.Topic
	messages     map[string]domain.Message
	scores       map[string]domain.Score
	graphRuns    map[string]domain.GraphRun
	nodeEvents   map[string]domain.NodeEvent
	lastSeq      map[string]int64
}

func NewMemory() *Memory {
	return &Memory{
		jobs:         make(map[string]domain.Job),
		applications: make(map[string]domain.Application),
		sessions:     make(map[string]domain.Session),
		topics:       make(map[string]domain.Topic),
		messages:     make(map[string]domain.Message),
		scores:       make(map[string]domain.Score),
		graphRuns:    make(map[string]domain.GraphRun),
		nodeEvents:   make(map[string]domain.NodeEvent),
		lastSeq:      make(map[string]int64),
	}
}

func (m *Memory) CreateJob(_ context.Context, job domain.Job) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.jobs[job.ID]; ok {
		return ErrConflict
	}
	defaultJobStatus(&job)
	now := time.Now().UTC()
	if job.CreatedAt.IsZero() {
		job.CreatedAt = now
	}
	if job.UpdatedAt.IsZero() {
		job.UpdatedAt = job.CreatedAt
	}
	m.jobs[job.ID] = cloneJob(job)
	return nil
}

func (m *Memory) GetJob(_ context.Context, id string) (domain.Job, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	job, ok := m.jobs[id]
	if !ok {
		return domain.Job{}, ErrNotFound
	}
	return cloneJob(job), nil
}

func (m *Memory) ListJobs(_ context.Context) ([]domain.Job, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	jobs := make([]domain.Job, 0, len(m.jobs))
	for _, job := range m.jobs {
		jobs = append(jobs, cloneJob(job))
	}
	sort.Slice(jobs, func(i, j int) bool {
		if jobs[i].CreatedAt.Equal(jobs[j].CreatedAt) {
			return jobs[i].ID < jobs[j].ID
		}
		return jobs[i].CreatedAt.Before(jobs[j].CreatedAt)
	})
	return jobs, nil
}

func (m *Memory) CreateApplication(_ context.Context, app domain.Application) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.applications[app.ID]; ok {
		return ErrConflict
	}
	defaultApplicationStatus(&app)
	for _, existing := range m.applications {
		if existing.JobID == app.JobID && existing.Email == app.Email {
			return ErrConflict
		}
	}
	if app.CreatedAt.IsZero() {
		app.CreatedAt = time.Now().UTC()
	}
	m.applications[app.ID] = app
	return nil
}

// CreateApplicationWithSession inserts an application and its first session
// atomically: either both rows exist or neither does. Used by the apply flow so
// a failed session insert never leaves an application without a session.
func (m *Memory) CreateApplicationWithSession(_ context.Context, app domain.Application, session domain.Session) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.applications[app.ID]; ok {
		return ErrConflict
	}
	for _, existing := range m.applications {
		if existing.JobID == app.JobID && existing.Email == app.Email {
			return ErrConflict
		}
	}
	if _, ok := m.sessions[session.ID]; ok {
		return ErrConflict
	}
	for _, existing := range m.sessions {
		if existing.TokenHash == session.TokenHash {
			return ErrConflict
		}
	}
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
	m.applications[app.ID] = app
	m.sessions[session.ID] = session
	return nil
}

func (m *Memory) GetApplication(_ context.Context, id string) (domain.Application, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	app, ok := m.applications[id]
	if !ok {
		return domain.Application{}, ErrNotFound
	}
	return app, nil
}

func (m *Memory) GetApplicationByJobEmail(_ context.Context, jobID, email string) (domain.Application, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, app := range m.applications {
		if app.JobID == jobID && app.Email == email {
			return app, nil
		}
	}
	return domain.Application{}, ErrNotFound
}

func (m *Memory) ListApplicationsByJob(_ context.Context, jobID string, status *domain.ApplicationStatus) ([]domain.Application, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	apps := make([]domain.Application, 0)
	for _, app := range m.applications {
		if app.JobID != jobID {
			continue
		}
		if status != nil && app.Status != *status {
			continue
		}
		apps = append(apps, app)
	}
	sort.Slice(apps, func(i, j int) bool {
		if apps[i].CreatedAt.Equal(apps[j].CreatedAt) {
			return apps[i].ID < apps[j].ID
		}
		return apps[i].CreatedAt.Before(apps[j].CreatedAt)
	})
	return apps, nil
}

func (m *Memory) UpdateApplicationStatus(_ context.Context, id string, status domain.ApplicationStatus) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	app, ok := m.applications[id]
	if !ok {
		return ErrNotFound
	}
	app.Status = status
	m.applications[id] = app
	return nil
}

func (m *Memory) SetApplicationDecision(_ context.Context, id string, status domain.ApplicationStatus, note string, decidedAt time.Time) (domain.Application, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	app, ok := m.applications[id]
	if !ok {
		return domain.Application{}, ErrNotFound
	}
	app.Status = status
	app.DecisionNote = note
	app.DecidedAt = &decidedAt
	m.applications[id] = app
	return app, nil
}

func (m *Memory) CreateSession(_ context.Context, session domain.Session) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.sessions[session.ID]; ok {
		return ErrConflict
	}
	defaultSessionStatus(&session)
	for _, existing := range m.sessions {
		if existing.TokenHash == session.TokenHash {
			return ErrConflict
		}
	}
	if session.CreatedAt.IsZero() {
		session.CreatedAt = time.Now().UTC()
	}
	m.sessions[session.ID] = session
	return nil
}

func (m *Memory) GetSession(_ context.Context, id string) (domain.Session, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	session, ok := m.sessions[id]
	if !ok {
		return domain.Session{}, ErrNotFound
	}
	return session, nil
}

func (m *Memory) GetSessionByTokenHash(_ context.Context, tokenHash string) (domain.Session, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, session := range m.sessions {
		if session.TokenHash == tokenHash {
			return session, nil
		}
	}
	return domain.Session{}, ErrNotFound
}

func (m *Memory) GetLatestSessionByApplication(_ context.Context, applicationID string) (domain.Session, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var latest domain.Session
	found := false
	for _, session := range m.sessions {
		if session.ApplicationID != applicationID {
			continue
		}
		if !found || session.CreatedAt.After(latest.CreatedAt) || (session.CreatedAt.Equal(latest.CreatedAt) && session.ID > latest.ID) {
			latest = session
			found = true
		}
	}
	if !found {
		return domain.Session{}, ErrNotFound
	}
	return latest, nil
}

func (m *Memory) ListSessionsByStatus(_ context.Context, status domain.SessionStatus) ([]domain.Session, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	sessions := make([]domain.Session, 0)
	for _, session := range m.sessions {
		if session.Status == status {
			sessions = append(sessions, session)
		}
	}
	sort.Slice(sessions, func(i, j int) bool {
		if sessions[i].CreatedAt.Equal(sessions[j].CreatedAt) {
			return sessions[i].ID < sessions[j].ID
		}
		return sessions[i].CreatedAt.Before(sessions[j].CreatedAt)
	})
	return sessions, nil
}

func (m *Memory) UpdateSession(_ context.Context, session domain.Session) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.sessions[session.ID]; !ok {
		return ErrNotFound
	}
	for id, existing := range m.sessions {
		if id != session.ID && existing.TokenHash == session.TokenHash {
			return ErrConflict
		}
	}
	m.sessions[session.ID] = session
	return nil
}

func (m *Memory) StartSession(_ context.Context, id string, startedAt time.Time) (domain.Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	session, ok := m.sessions[id]
	if !ok {
		return domain.Session{}, ErrNotFound
	}
	if session.Status != domain.SessionStatusInvited {
		return domain.Session{}, ErrConflict
	}
	app, ok := m.applications[session.ApplicationID]
	if !ok {
		return domain.Session{}, ErrNotFound
	}
	if app.Status != domain.ApplicationStatusInvited {
		return domain.Session{}, ErrConflict
	}
	session.Status = domain.SessionStatusActive
	session.StartedAt = &startedAt
	app.Status = domain.ApplicationStatusInSession
	m.sessions[id] = session
	m.applications[app.ID] = app
	return session, nil
}

// ExpireSession transitions an invited session that is already past its expiry
// to expired. It reports whether the transition happened; a session that has
// since changed state is skipped (changed=false).
func (m *Memory) ExpireSession(_ context.Context, id string, now time.Time) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	session, ok := m.sessions[id]
	if !ok {
		return false, ErrNotFound
	}
	if session.Status != domain.SessionStatusInvited || session.ExpiresAt.After(now) {
		return false, nil
	}
	session.Status = domain.SessionStatusExpired
	m.sessions[id] = session
	return true, nil
}

// AbandonSession transitions an active session to completed with
// finish_reason=abandoned. It reports whether the transition happened; a
// session that is no longer active is skipped (changed=false).
func (m *Memory) AbandonSession(_ context.Context, id string, now time.Time) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	session, ok := m.sessions[id]
	if !ok {
		return false, ErrNotFound
	}
	if session.Status != domain.SessionStatusActive {
		return false, nil
	}
	reason := domain.FinishReasonAbandoned
	session.Status = domain.SessionStatusCompleted
	session.FinishReason = &reason
	session.EndedAt = &now
	m.sessions[id] = session
	return true, nil
}

func (m *Memory) CreateTopics(_ context.Context, topics []domain.Topic) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for i := range topics {
		defaultTopicStatus(&topics[i])
		if _, ok := m.topics[topics[i].ID]; ok {
			return ErrConflict
		}
	}
	for _, topic := range topics {
		m.topics[topic.ID] = cloneTopic(topic)
	}
	return nil
}

func (m *Memory) ListTopicsBySession(_ context.Context, sessionID string) ([]domain.Topic, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	topics := make([]domain.Topic, 0)
	for _, topic := range m.topics {
		if topic.SessionID == sessionID {
			topics = append(topics, cloneTopic(topic))
		}
	}
	sort.Slice(topics, func(i, j int) bool {
		if topics[i].Position == topics[j].Position {
			return topics[i].ID < topics[j].ID
		}
		return topics[i].Position < topics[j].Position
	})
	return topics, nil
}

func (m *Memory) UpdateTopic(_ context.Context, topic domain.Topic) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.topics[topic.ID]; !ok {
		return ErrNotFound
	}
	m.topics[topic.ID] = cloneTopic(topic)
	return nil
}

func (m *Memory) CreateMessage(_ context.Context, message domain.Message) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.messages[message.ID]; ok {
		return ErrConflict
	}
	if message.CreatedAt.IsZero() {
		message.CreatedAt = time.Now().UTC()
	}
	m.messages[message.ID] = message
	return nil
}

func (m *Memory) ListMessagesBySession(_ context.Context, sessionID string, includeSystem bool) ([]domain.Message, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	messages := make([]domain.Message, 0)
	for _, message := range m.messages {
		if message.SessionID != sessionID {
			continue
		}
		if !includeSystem && message.Role == domain.MessageRoleSystem {
			continue
		}
		messages = append(messages, message)
	}
	sort.Slice(messages, func(i, j int) bool {
		if messages[i].CreatedAt.Equal(messages[j].CreatedAt) {
			return messages[i].ID < messages[j].ID
		}
		return messages[i].CreatedAt.Before(messages[j].CreatedAt)
	})
	return messages, nil
}

func (m *Memory) UpsertScores(_ context.Context, scores []domain.Score) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now().UTC()
	for _, score := range scores {
		defaultScoreStatus(&score)
		if score.Attempts == 0 {
			score.Attempts = 1
		}
		if score.CreatedAt.IsZero() {
			score.CreatedAt = now
		}
		existingID := ""
		for id, existing := range m.scores {
			if existing.ApplicationID == score.ApplicationID && existing.RubricKey == score.RubricKey {
				existingID = id
				delete(m.scores, id)
				break
			}
		}
		switch {
		case existingID != "":
			score.ID = existingID
		case score.ID == "":
			score.ID = domain.NewID()
		}
		m.scores[score.ID] = cloneScore(score)
	}
	return nil
}

func (m *Memory) ListScoresByApplication(_ context.Context, applicationID string) ([]domain.Score, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	scores := make([]domain.Score, 0)
	for _, score := range m.scores {
		if score.ApplicationID == applicationID {
			scores = append(scores, cloneScore(score))
		}
	}
	sort.Slice(scores, func(i, j int) bool {
		if scores[i].CreatedAt.Equal(scores[j].CreatedAt) {
			return scores[i].RubricKey < scores[j].RubricKey
		}
		return scores[i].CreatedAt.Before(scores[j].CreatedAt)
	})
	return scores, nil
}

func (m *Memory) CreateGraphRun(_ context.Context, run domain.GraphRun) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.graphRuns[run.ID]; ok {
		return ErrConflict
	}
	defaultGraphRunStatus(&run)
	if run.StartedAt.IsZero() {
		run.StartedAt = time.Now().UTC()
	}
	m.graphRuns[run.ID] = run
	return nil
}

func (m *Memory) GetGraphRun(_ context.Context, id string) (domain.GraphRun, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	run, ok := m.graphRuns[id]
	if !ok {
		return domain.GraphRun{}, ErrNotFound
	}
	return run, nil
}

func (m *Memory) GetGraphRunBySession(_ context.Context, sessionID string) (domain.GraphRun, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var latest domain.GraphRun
	found := false
	for _, run := range m.graphRuns {
		if run.SessionID != sessionID {
			continue
		}
		if !found || run.StartedAt.After(latest.StartedAt) || (run.StartedAt.Equal(latest.StartedAt) && run.ID > latest.ID) {
			latest = run
			found = true
		}
	}
	if !found {
		return domain.GraphRun{}, ErrNotFound
	}
	return latest, nil
}

func (m *Memory) UpdateGraphRun(_ context.Context, run domain.GraphRun) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.graphRuns[run.ID]; !ok {
		return ErrNotFound
	}
	m.graphRuns[run.ID] = run
	return nil
}

func (m *Memory) AppendNodeEvent(_ context.Context, event domain.NodeEvent) (domain.NodeEvent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if event.ID == "" {
		event.ID = domain.NewID()
	}
	if event.Attempt == 0 {
		event.Attempt = 1
	}
	if _, ok := m.nodeEvents[event.ID]; ok {
		return domain.NodeEvent{}, ErrConflict
	}
	event.Seq = m.lastSeq[event.RunID] + 1
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now().UTC()
	}
	m.lastSeq[event.RunID] = event.Seq
	m.nodeEvents[event.ID] = cloneNodeEvent(event)
	return cloneNodeEvent(event), nil
}

func (m *Memory) ListNodeEvents(_ context.Context, runID string, sinceSeq int64) ([]domain.NodeEvent, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	events := make([]domain.NodeEvent, 0)
	for _, event := range m.nodeEvents {
		if event.RunID == runID && event.Seq > sinceSeq {
			events = append(events, cloneNodeEvent(event))
		}
	}
	sort.Slice(events, func(i, j int) bool {
		return events[i].Seq < events[j].Seq
	})
	return events, nil
}

func cloneJob(job domain.Job) domain.Job {
	job.Rubric = append([]domain.RubricCriterion(nil), job.Rubric...)
	return job
}

func cloneTopic(topic domain.Topic) domain.Topic {
	topic.RubricKeys = append([]string(nil), topic.RubricKeys...)
	return topic
}

func cloneScore(score domain.Score) domain.Score {
	score.Evidence = append([]domain.ScoreEvidence(nil), score.Evidence...)
	return score
}

func cloneNodeEvent(event domain.NodeEvent) domain.NodeEvent {
	event.Payload = append([]byte(nil), event.Payload...)
	return event
}
