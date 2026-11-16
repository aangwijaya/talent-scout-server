package graph

import (
	"context"
	"encoding/json"
	"time"

	"github.com/aangwijaya/talent-scout-server/internal/domain"
	"github.com/aangwijaya/talent-scout-server/internal/store"
)

// restartFailureReason marks a run or node attempt that a server restart
// interrupted. Automatic runner resume is post-MVP (ARCHITECTURE §7), so on
// boot an interrupted run cannot continue and must be closed.
const restartFailureReason = "server_restart"

// ReconcileResult reports what the boot reconciliation closed.
type ReconcileResult struct {
	RunsClosed     int
	SessionsClosed int
	NodesClosed    int
}

// ReconcileOrphanedRuns closes graph runs left running by a previous process,
// along with any node attempt that has a node.started and no terminal event,
// and finishes their sessions and applications as failed so resend-invite can
// recover the applicant. It also finishes active sessions that have no runner
// (no running run) instead of letting them accept answers nobody will process.
func ReconcileOrphanedRuns(ctx context.Context, st store.Store) (ReconcileResult, error) {
	var result ReconcileResult
	now := time.Now().UTC()

	runs, err := st.ListGraphRunsByStatus(ctx, domain.GraphRunStatusRunning)
	if err != nil {
		return result, err
	}

	handled := make(map[string]struct{}, len(runs))
	for _, run := range runs {
		sess, err := st.GetSession(ctx, run.SessionID)
		if err != nil {
			return result, err
		}
		handled[sess.ID] = struct{}{}

		nodes, err := closeDanglingNodes(ctx, st, run.ID)
		if err != nil {
			return result, err
		}
		result.NodesClosed += nodes

		if err := appendRunEvent(ctx, st, run.ID, EventRunFailed, RunFailed{Error: restartFailureReason}); err != nil {
			return result, err
		}
		message := restartFailureReason
		ended := now
		if err := st.UpdateGraphRun(ctx, domain.GraphRun{
			ID: run.ID, Status: domain.GraphRunStatusFailed, EndedAt: &ended, Error: &message,
		}); err != nil {
			return result, err
		}
		result.RunsClosed++

		wasActive := sess.Status == domain.SessionStatusActive
		if wasActive {
			changed, err := st.FinishSession(ctx, sess.ID, domain.FinishReasonFailed, now)
			if err != nil {
				return result, err
			}
			if changed {
				result.SessionsClosed++
			}
		}
		if err := markApplicationFailed(ctx, st, sess.ApplicationID); err != nil {
			return result, err
		}
		if wasActive {
			if err := appendRunEvent(ctx, st, run.ID, EventSessionEnded, SessionEnded{FinishReason: string(domain.FinishReasonFailed)}); err != nil {
				return result, err
			}
		}
	}

	active, err := st.ListSessionsByStatus(ctx, domain.SessionStatusActive)
	if err != nil {
		return result, err
	}
	for _, sess := range active {
		if _, ok := handled[sess.ID]; ok {
			continue
		}
		if run, runErr := st.GetGraphRunBySession(ctx, sess.ID); runErr == nil && run.Status == domain.GraphRunStatusRunning {
			// A running run created between the two lists: close it the same way.
			nodes, err := closeDanglingNodes(ctx, st, run.ID)
			if err != nil {
				return result, err
			}
			result.NodesClosed += nodes
			if err := appendRunEvent(ctx, st, run.ID, EventRunFailed, RunFailed{Error: restartFailureReason}); err != nil {
				return result, err
			}
			message := restartFailureReason
			ended := now
			if err := st.UpdateGraphRun(ctx, domain.GraphRun{ID: run.ID, Status: domain.GraphRunStatusFailed, EndedAt: &ended, Error: &message}); err != nil {
				return result, err
			}
			result.RunsClosed++
			if err := appendRunEvent(ctx, st, run.ID, EventSessionEnded, SessionEnded{FinishReason: string(domain.FinishReasonFailed)}); err != nil {
				return result, err
			}
		}
		changed, err := st.FinishSession(ctx, sess.ID, domain.FinishReasonFailed, now)
		if err != nil {
			return result, err
		}
		if changed {
			result.SessionsClosed++
		}
		if err := markApplicationFailed(ctx, st, sess.ApplicationID); err != nil {
			return result, err
		}
	}

	return result, nil
}

// closeDanglingNodes writes exactly one terminal node.failed for every attempt
// that has a node.started but no node.completed/node.failed/node.skipped.
func closeDanglingNodes(ctx context.Context, st store.Store, runID string) (int, error) {
	events, err := st.ListNodeEvents(ctx, runID, 0)
	if err != nil {
		return 0, err
	}

	type attemptKey struct {
		nodeID  string
		attempt int
	}
	open := make(map[attemptKey]domain.NodeEvent)
	order := make([]attemptKey, 0)
	for _, event := range events {
		if event.NodeID == nil {
			continue
		}
		attempt := 0
		if event.Attempt != nil {
			attempt = *event.Attempt
		}
		key := attemptKey{nodeID: *event.NodeID, attempt: attempt}
		switch event.Type {
		case EventNodeStarted:
			if _, ok := open[key]; !ok {
				order = append(order, key)
			}
			open[key] = event
		case EventNodeCompleted, EventNodeFailed, EventNodeSkipped:
			delete(open, key)
		}
	}

	closed := 0
	for _, key := range order {
		started, ok := open[key]
		if !ok {
			continue
		}
		if err := appendNodeEvent(ctx, st, runID, started.NodeID, started.Attempt, EventNodeFailed, NodeFailed{Error: restartFailureReason}); err != nil {
			return closed, err
		}
		closed++
	}
	return closed, nil
}

func markApplicationFailed(ctx context.Context, st store.Store, applicationID string) error {
	app, err := st.GetApplication(ctx, applicationID)
	if err != nil {
		return err
	}
	if app.Status != domain.ApplicationStatusInSession && app.Status != domain.ApplicationStatusScoring {
		return nil
	}
	return st.UpdateApplicationStatus(ctx, app.ID, domain.ApplicationStatusFailed)
}

func appendRunEvent(ctx context.Context, st store.Store, runID, eventType string, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = st.AppendNodeEvent(ctx, domain.NodeEvent{RunID: runID, Type: eventType, Payload: data})
	return err
}

func appendNodeEvent(ctx context.Context, st store.Store, runID string, nodeID *string, attempt *int, eventType string, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = st.AppendNodeEvent(ctx, domain.NodeEvent{
		RunID: runID, NodeID: nodeID, Attempt: attempt, Type: eventType, Payload: data,
	})
	return err
}
