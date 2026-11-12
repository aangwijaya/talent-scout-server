package httpapi_test

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aangwijaya/talent-scout-server/internal/auth"
	"github.com/aangwijaya/talent-scout-server/internal/config"
	"github.com/aangwijaya/talent-scout-server/internal/conformance"
	"github.com/aangwijaya/talent-scout-server/internal/domain"
	"github.com/aangwijaya/talent-scout-server/internal/graph"
	httpapi "github.com/aangwijaya/talent-scout-server/internal/http"
	"github.com/aangwijaya/talent-scout-server/internal/llm"
	"github.com/aangwijaya/talent-scout-server/internal/store"
)

const (
	graphRunsRouterRunID     = "7c9e6679-0000-4000-8000-000000000501"
	graphRunsRouterSessionID = "7c9e6679-0000-4000-8000-000000000502"
)

func graphRunsRouter(t *testing.T, runStatus domain.GraphRunStatus) (*store.Memory, http.Handler, config.Config) {
	t.Helper()
	st := store.NewMemory()
	if err := st.CreateGraphRun(context.Background(), domain.GraphRun{
		ID:             graphRunsRouterRunID,
		SessionID:      graphRunsRouterSessionID,
		Status:         runStatus,
		TotalTokensIn:  12000,
		TotalTokensOut: 1800,
		CostUSD:        0.0021,
	}); err != nil {
		t.Fatalf("CreateGraphRun: %v", err)
	}
	cfg := graphRunsConfig()
	router := httpapi.NewRouter(cfg, httpapi.Deps{Store: st, LLM: llm.NewFake(graph.FakeReplies), Models: testGraphModels()})
	return st, router, cfg
}

func graphRunsConfig() config.Config {
	return config.Config{
		Version:           "0.1.0",
		LLMMode:           "fake",
		EmailMode:         "log",
		PublicAppURL:      "https://talentscout.vercel.app",
		RecruiterPassword: "correct horse",
		AuthTokenSecret:   "0123456789abcdef0123456789abcdef",
	}
}

func seedRunEvent(t *testing.T, st *store.Memory, eventType, nodeID string) {
	t.Helper()
	var node *string
	if nodeID != "" {
		node = domain.StrPtr(nodeID)
	}
	if _, err := st.AppendNodeEvent(context.Background(), domain.NodeEvent{
		RunID:   graphRunsRouterRunID,
		NodeID:  node,
		Type:    eventType,
		Payload: []byte(`{}`),
	}); err != nil {
		t.Fatalf("AppendNodeEvent(%s): %v", eventType, err)
	}
}

func TestGraphRunSnapshotMatchesContract(t *testing.T) {
	st, router, cfg := graphRunsRouter(t, domain.GraphRunStatusSucceeded)
	seedRunEvent(t, st, graph.EventRunStarted, "")
	seedRunEvent(t, st, graph.EventNodeStarted, "planner")
	seedRunEvent(t, st, graph.EventNodeCompleted, "planner")
	seedRunEvent(t, st, graph.EventRunCompleted, "")

	access, _, err := auth.New(cfg.AuthTokenSecret).IssueAccessToken()
	if err != nil {
		t.Fatalf("IssueAccessToken: %v", err)
	}
	validator := conformance.New(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/graph-runs/"+graphRunsRouterRunID, nil)
	req.Header.Set("Authorization", "Bearer "+access)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	validator.ValidateResponse(t, req, rec.Result())

	var snapshot struct {
		ID      string `json:"id"`
		Status  string `json:"status"`
		LastSeq int64  `json:"last_seq"`
		Nodes   []struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"nodes"`
		Edges []struct {
			From string `json:"from"`
			To   string `json:"to"`
		} `json:"edges"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &snapshot); err != nil {
		t.Fatalf("decode snapshot: %v", err)
	}
	if snapshot.ID != graphRunsRouterRunID || snapshot.Status != string(domain.GraphRunStatusSucceeded) {
		t.Errorf("snapshot = %s/%s, want %s/succeeded", snapshot.ID, snapshot.Status, graphRunsRouterRunID)
	}
	if snapshot.LastSeq != 4 {
		t.Errorf("last_seq = %d, want 4", snapshot.LastSeq)
	}
	if len(snapshot.Nodes) != len(graph.NodeIDs) || len(snapshot.Edges) == 0 {
		t.Errorf("nodes/edges = %d/%d, want %d/non-empty", len(snapshot.Nodes), len(snapshot.Edges), len(graph.NodeIDs))
	}
	if planner := snapshot.Nodes[0]; planner.ID != "planner" || planner.Status != "succeeded" {
		t.Errorf("nodes[0] = %+v, want planner/succeeded", planner)
	}

	unauthorized := httptest.NewRequest(http.MethodGet, "/api/v1/graph-runs/"+graphRunsRouterRunID, nil)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, unauthorized)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("snapshot without bearer status = %d, want 401", rec.Code)
	}
	validator.ValidateResponse(t, unauthorized, rec.Result())

	missing := httptest.NewRequest(http.MethodGet, "/api/v1/graph-runs/7c9e6679-0000-4000-8000-000000000599", nil)
	missing.Header.Set("Authorization", "Bearer "+access)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, missing)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown run status = %d, want 404", rec.Code)
	}
	validator.ValidateResponse(t, missing, rec.Result())
}

func TestRunSSEReplaysAndStaysOpenWithValidToken(t *testing.T) {
	st, router, cfg := graphRunsRouter(t, domain.GraphRunStatusRunning)
	seedRunEvent(t, st, graph.EventRunStarted, "")
	seedRunEvent(t, st, graph.EventNodeSkipped, "interviewer")
	seedRunEvent(t, st, graph.EventEdgeTraversed, "")

	token, _, err := auth.New(cfg.AuthTokenSecret).IssueSSEToken(graphRunsRouterRunID)
	if err != nil {
		t.Fatalf("IssueSSEToken: %v", err)
	}

	server := httptest.NewServer(router)
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		server.URL+"/api/v1/graph-runs/"+graphRunsRouterRunID+"/events?sse_token="+token+"&since_seq=1", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Type"); !strings.HasPrefix(got, "text/event-stream") {
		t.Errorf("Content-Type = %q, want text/event-stream", got)
	}

	reader := bufio.NewReader(resp.Body)
	first := readSSEFrame(t, reader)
	if !strings.Contains(first, "id: 2") || !strings.Contains(first, "event: node.skipped") {
		t.Fatalf("first replayed frame = %q, want id 2 node.skipped", first)
	}
	second := readSSEFrame(t, reader)
	if !strings.Contains(second, "id: 3") || !strings.Contains(second, "event: edge.traversed") {
		t.Fatalf("second replayed frame = %q, want id 3 edge.traversed", second)
	}

	read := make(chan error, 1)
	go func() {
		_, err := reader.ReadString('\n')
		read <- err
	}()
	select {
	case err := <-read:
		t.Fatalf("stream closed while the run was still running: %v", err)
	case <-time.After(150 * time.Millisecond):
	}
	cancel()
	select {
	case <-read:
	case <-time.After(time.Second):
		t.Fatal("stream did not stop after the client cancelled")
	}
}

func readSSEFrame(t *testing.T, reader *bufio.Reader) string {
	t.Helper()
	var frame strings.Builder
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("read SSE frame: %v (partial: %q)", err, frame.String())
		}
		frame.WriteString(line)
		if line == "\n" {
			return frame.String()
		}
	}
}

// rejectingLookupStore fails the test if a handler reaches the store for an id
// that is not a UUID. A malformed path parameter must be rejected first.
type rejectingLookupStore struct {
	store.Store
	t *testing.T
}

func (s rejectingLookupStore) GetGraphRun(context.Context, string) (domain.GraphRun, error) {
	s.t.Errorf("GetGraphRun called for a malformed run id")
	return domain.GraphRun{}, store.ErrNotFound
}

func (s rejectingLookupStore) ListNodeEvents(context.Context, string, int64) ([]domain.NodeEvent, error) {
	s.t.Errorf("ListNodeEvents called for a malformed run id")
	return nil, store.ErrNotFound
}

func TestGraphRunMalformedIDReturnsNotFoundBeforeStore(t *testing.T) {
	cfg := graphRunsConfig()
	st := rejectingLookupStore{Store: store.NewMemory(), t: t}
	router := httpapi.NewRouter(cfg, httpapi.Deps{Store: st})
	tokens := auth.New(cfg.AuthTokenSecret)
	access, _, err := tokens.IssueAccessToken()
	if err != nil {
		t.Fatalf("IssueAccessToken: %v", err)
	}
	sseToken, _, err := tokens.IssueSSEToken("not-a-uuid")
	if err != nil {
		t.Fatalf("IssueSSEToken: %v", err)
	}
	validator := conformance.New(t)

	cases := []struct {
		name   string
		path   string
		bearer string
	}{
		{name: "snapshot", path: "/api/v1/graph-runs/not-a-uuid", bearer: access},
		{name: "events", path: "/api/v1/graph-runs/not-a-uuid/events?sse_token=" + sseToken},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			if tc.bearer != "" {
				req.Header.Set("Authorization", "Bearer "+tc.bearer)
			}
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)

			if rec.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want 404 (body: %s)", rec.Code, rec.Body.String())
			}
			validator.ValidateResponse(t, req, rec.Result())

			var body struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode error body: %v", err)
			}
			if body.Error.Code != "not_found" {
				t.Errorf("error code = %q, want not_found", body.Error.Code)
			}
		})
	}
}
