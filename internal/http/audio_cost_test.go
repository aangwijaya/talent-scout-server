package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aangwijaya/talent-scout-server/internal/auth"
	"github.com/aangwijaya/talent-scout-server/internal/domain"
	"github.com/aangwijaya/talent-scout-server/internal/store"
)

func TestApplicationGraphRunCostIncludesTranscriptionAndSurvivesRunnerUpdate(t *testing.T) {
	f := newApplicationsFixture(t, "log")
	app := f.seedApplication(t, "voice-cost@example.com", domain.ApplicationStatusReview)
	sess, _ := f.seedSession(t, app, domain.SessionStatusCompleted)
	ctx := context.Background()
	if err := f.store.CreateGraphRun(ctx, domain.GraphRun{
		ID: applicationsRunID, SessionID: sess.ID, Status: domain.GraphRunStatusSucceeded, CostUSD: 0.0041,
	}); err != nil {
		t.Fatalf("CreateGraphRun: %v", err)
	}
	if err := f.store.CreateAudioUpload(ctx, store.AudioUpload{
		ID:          domain.NewID(),
		SessionID:   sess.ID,
		ObjectPath:  "sessions/" + sess.ID + "/answer.webm",
		ContentType: "audio/webm",
		SizeBytes:   1024,
		DurationMS:  18000,
		CostUSD:     0.004,
		Transcript:  "a spoken answer",
		Language:    "en",
		UploadedAt:  time.Now().UTC(),
	}); err != nil {
		t.Fatalf("CreateAudioUpload: %v", err)
	}

	getCost := func() float64 {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/applications/"+app.ID, nil)
		req.Header.Set("Authorization", "Bearer "+f.token)
		rec := httptest.NewRecorder()
		f.router.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET application = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
		}
		var body struct {
			GraphRun struct {
				CostUSD float64 `json:"cost_usd"`
			} `json:"graph_run"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode application: %v", err)
		}
		return body.GraphRun.CostUSD
	}

	if got := getCost(); got != 0.0081 {
		t.Errorf("graph_run.cost_usd = %v, want 0.0081 (runner + transcription)", got)
	}

	// The graph runner rewrites cost_usd as a whole value on every finish. That
	// must not erase the transcription cost kept on the audio_uploads rows.
	if err := f.store.UpdateGraphRun(ctx, domain.GraphRun{
		ID: applicationsRunID, Status: domain.GraphRunStatusSucceeded, CostUSD: 0.0041,
	}); err != nil {
		t.Fatalf("UpdateGraphRun: %v", err)
	}
	if got := getCost(); got != 0.0081 {
		t.Errorf("graph_run.cost_usd after runner update = %v, want 0.0081", got)
	}
}

func TestGraphRunSnapshotCostIncludesTranscription(t *testing.T) {
	st, router, cfg := graphRunsRouter(t, domain.GraphRunStatusSucceeded)
	if err := st.CreateAudioUpload(context.Background(), store.AudioUpload{
		ID:          domain.NewID(),
		SessionID:   graphRunsRouterSessionID,
		ObjectPath:  "sessions/" + graphRunsRouterSessionID + "/answer.webm",
		ContentType: "audio/webm",
		SizeBytes:   1024,
		DurationMS:  18000,
		CostUSD:     0.001,
		UploadedAt:  time.Now().UTC(),
	}); err != nil {
		t.Fatalf("CreateAudioUpload: %v", err)
	}
	access, _, err := auth.New(cfg.AuthTokenSecret).IssueAccessToken()
	if err != nil {
		t.Fatalf("IssueAccessToken: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/graph-runs/"+graphRunsRouterRunID, nil)
	req.Header.Set("Authorization", "Bearer "+access)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var snapshot struct {
		CostUSD float64 `json:"cost_usd"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &snapshot); err != nil {
		t.Fatalf("decode snapshot: %v", err)
	}
	if snapshot.CostUSD != 0.0031 {
		t.Errorf("snapshot cost_usd = %v, want 0.0031", snapshot.CostUSD)
	}
}
