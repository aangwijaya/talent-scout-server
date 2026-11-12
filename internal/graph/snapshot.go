package graph

import (
	"encoding/json"

	"github.com/aangwijaya/talent-scout-server/internal/domain"
)

// GraphRunSnapshot is the GET /graph-runs/{runId} response body (docs/API.md).
type GraphRunSnapshot struct {
	ID             string         `json:"id"`
	SessionID      string         `json:"session_id"`
	Status         string         `json:"status"`
	Nodes          []SnapshotNode `json:"nodes"`
	Edges          []EdgeRef      `json:"edges"`
	LastSeq        int64          `json:"last_seq"`
	TotalTokensIn  int            `json:"total_tokens_in"`
	TotalTokensOut int            `json:"total_tokens_out"`
	CostUSD        float64        `json:"cost_usd"`
}

// SnapshotNode is one graph node's state in the run snapshot.
type SnapshotNode struct {
	ID       string  `json:"id"`
	Type     string  `json:"type"`
	Status   string  `json:"status"`
	Attempts int     `json:"attempts"`
	Model    *string `json:"model"`
}

const (
	snapshotNodePending   = "pending"
	snapshotNodeRunning   = "running"
	snapshotNodeSucceeded = "succeeded"
	snapshotNodeFailed    = "failed"
	snapshotNodeSkipped   = "skipped"
)

// Snapshot derives the recruiter's graph view from the persisted run events:
// the static graph shape, each node's last status/attempt/model, and last_seq.
func Snapshot(run domain.GraphRun, events []domain.NodeEvent) GraphRunSnapshot {
	snapshot := GraphRunSnapshot{
		ID:             run.ID,
		SessionID:      run.SessionID,
		Status:         string(run.Status),
		Nodes:          make([]SnapshotNode, 0, len(NodeIDs)),
		Edges:          Edges,
		TotalTokensIn:  run.TotalTokensIn,
		TotalTokensOut: run.TotalTokensOut,
		CostUSD:        run.CostUSD,
	}
	index := make(map[string]int, len(NodeIDs))
	for _, nodeID := range NodeIDs {
		index[nodeID] = len(snapshot.Nodes)
		snapshot.Nodes = append(snapshot.Nodes, SnapshotNode{
			ID:     nodeID,
			Type:   string(nodeTypes[nodeID]),
			Status: snapshotNodePending,
		})
	}

	for _, event := range events {
		if event.Seq > snapshot.LastSeq {
			snapshot.LastSeq = event.Seq
		}
		if event.NodeID == nil {
			continue
		}
		position, ok := index[*event.NodeID]
		if !ok {
			continue
		}
		node := &snapshot.Nodes[position]
		if event.Attempt != nil && *event.Attempt > node.Attempts {
			node.Attempts = *event.Attempt
		}
		if event.Model != nil {
			model := *event.Model
			node.Model = &model
		}

		switch event.Type {
		case EventNodeStarted:
			node.Status = snapshotNodeRunning
		case EventNodeCompleted:
			node.Status = snapshotNodeSucceeded
		case EventNodeFailed:
			var payload NodeFailed
			_ = json.Unmarshal(event.Payload, &payload)
			if payload.WillRetry {
				node.Status = snapshotNodeRunning
			} else {
				node.Status = snapshotNodeFailed
			}
		case EventNodeSkipped:
			node.Status = snapshotNodeSkipped
		}
	}

	return snapshot
}
