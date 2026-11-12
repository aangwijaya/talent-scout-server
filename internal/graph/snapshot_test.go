package graph

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/aangwijaya/talent-scout-server/internal/domain"
)

func snapshotPayload(t *testing.T, value any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return data
}

func TestGraphRunSnapshotFromEvents(t *testing.T) {
	run := domain.GraphRun{
		ID:             "run-1",
		SessionID:      "session-1",
		Status:         domain.GraphRunStatusFailed,
		TotalTokensIn:  120,
		TotalTokensOut: 30,
		CostUSD:        0.004,
	}
	events := []domain.NodeEvent{
		{RunID: run.ID, Seq: 1, Type: EventRunStarted, Payload: json.RawMessage(`{}`)},
		{RunID: run.ID, Seq: 2, NodeID: domain.StrPtr("planner"), Attempt: domain.IntPtr(1), Model: domain.StrPtr("model-a"), Type: EventNodeStarted, Payload: json.RawMessage(`{}`)},
		{RunID: run.ID, Seq: 3, NodeID: domain.StrPtr("planner"), Attempt: domain.IntPtr(1), Model: domain.StrPtr("model-a"), Type: EventNodeCompleted, Payload: json.RawMessage(`{}`)},
		{RunID: run.ID, Seq: 4, Type: EventEdgeTraversed, Payload: json.RawMessage(`{}`)},
		{RunID: run.ID, Seq: 5, NodeID: domain.StrPtr("plan_check"), Attempt: domain.IntPtr(1), Type: EventNodeStarted, Payload: json.RawMessage(`{}`)},
		{RunID: run.ID, Seq: 6, NodeID: domain.StrPtr("plan_check"), Attempt: domain.IntPtr(1), Type: EventNodeFailed, Payload: snapshotPayload(t, NodeFailed{Error: "invalid", WillRetry: false})},
		{RunID: run.ID, Seq: 7, NodeID: domain.StrPtr("judge"), Attempt: domain.IntPtr(1), Model: domain.StrPtr("model-b"), Type: EventNodeStarted, Payload: json.RawMessage(`{}`)},
		{RunID: run.ID, Seq: 8, NodeID: domain.StrPtr("judge"), Attempt: domain.IntPtr(1), Model: domain.StrPtr("model-b"), Type: EventNodeFailed, Payload: snapshotPayload(t, NodeFailed{Error: "timeout", WillRetry: true})},
		{RunID: run.ID, Seq: 9, NodeID: domain.StrPtr("judge"), Attempt: domain.IntPtr(2), Model: domain.StrPtr("model-b"), Type: EventNodeStarted, Payload: json.RawMessage(`{}`)},
		{RunID: run.ID, Seq: 10, NodeID: domain.StrPtr("judge"), Attempt: domain.IntPtr(2), Model: domain.StrPtr("model-b"), Type: EventNodeCompleted, Payload: json.RawMessage(`{}`)},
		{RunID: run.ID, Seq: 11, NodeID: domain.StrPtr("interviewer"), Type: EventNodeSkipped, Payload: snapshotPayload(t, NodeSkipped{Reason: "run_failed"})},
	}

	snapshot := Snapshot(run, events)

	if snapshot.ID != run.ID || snapshot.SessionID != run.SessionID || snapshot.Status != string(run.Status) {
		t.Errorf("snapshot identity = %+v, want run fields", snapshot)
	}
	if snapshot.LastSeq != 11 {
		t.Errorf("last_seq = %d, want 11", snapshot.LastSeq)
	}
	if snapshot.TotalTokensIn != run.TotalTokensIn || snapshot.TotalTokensOut != run.TotalTokensOut || snapshot.CostUSD != run.CostUSD {
		t.Errorf("snapshot totals = %d/%d/%v, want run totals", snapshot.TotalTokensIn, snapshot.TotalTokensOut, snapshot.CostUSD)
	}
	if !reflect.DeepEqual(snapshot.Edges, Edges) {
		t.Errorf("edges = %+v, want the static graph edges", snapshot.Edges)
	}
	if len(snapshot.Nodes) != len(NodeIDs) {
		t.Fatalf("nodes = %d, want %d", len(snapshot.Nodes), len(NodeIDs))
	}

	byID := make(map[string]SnapshotNode, len(snapshot.Nodes))
	for _, node := range snapshot.Nodes {
		byID[node.ID] = node
	}
	assertSnapshotNode(t, byID["planner"], "succeeded", 1, "model-a")
	assertSnapshotNode(t, byID["plan_check"], "failed", 1, "")
	assertSnapshotNode(t, byID["judge"], "succeeded", 2, "model-b")
	assertSnapshotNode(t, byID["interviewer"], "skipped", 0, "")
	assertSnapshotNode(t, byID["guard"], "pending", 0, "")
}

func assertSnapshotNode(t *testing.T, node SnapshotNode, status string, attempts int, model string) {
	t.Helper()
	if node.Status != status {
		t.Errorf("%s status = %q, want %q", node.ID, node.Status, status)
	}
	if node.Attempts != attempts {
		t.Errorf("%s attempts = %d, want %d", node.ID, node.Attempts, attempts)
	}
	gotModel := ""
	if node.Model != nil {
		gotModel = *node.Model
	}
	if gotModel != model {
		t.Errorf("%s model = %q, want %q", node.ID, gotModel, model)
	}
}

func TestGraphRunSnapshotWithoutEventsIsAllPending(t *testing.T) {
	run := domain.GraphRun{ID: "run-2", SessionID: "session-2", Status: domain.GraphRunStatusRunning}
	snapshot := Snapshot(run, nil)
	if snapshot.LastSeq != 0 {
		t.Errorf("last_seq = %d, want 0", snapshot.LastSeq)
	}
	for _, node := range snapshot.Nodes {
		if node.Status != "pending" || node.Attempts != 0 || node.Model != nil {
			t.Errorf("node %s = %+v, want pending/0/nil", node.ID, node)
		}
	}
}
