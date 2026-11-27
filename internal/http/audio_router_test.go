package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"testing"
	"time"

	"github.com/aangwijaya/talent-scout-server/internal/domain"
	"github.com/aangwijaya/talent-scout-server/internal/graph"
	httpapi "github.com/aangwijaya/talent-scout-server/internal/http"
	"github.com/aangwijaya/talent-scout-server/internal/llm"
	"github.com/aangwijaya/talent-scout-server/internal/session"
	"github.com/aangwijaya/talent-scout-server/internal/storage"
	"github.com/aangwijaya/talent-scout-server/internal/store"
	"github.com/aangwijaya/talent-scout-server/internal/transcribe"
)

// TestUploadAudioRouteTranscribesThroughTheRouter proves the route wiring and
// SetAudio: deleting either leaves this test unable to return 200.
func TestUploadAudioRouteTranscribesThroughTheRouter(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemory()
	jobID := "7c9e6679-0000-4000-8000-000000000601"
	appID := "7c9e6679-0000-4000-8000-000000000602"
	sessionID := "7c9e6679-0000-4000-8000-000000000603"
	if err := st.CreateJob(ctx, domain.Job{
		ID:          jobID,
		Title:       "Senior Frontend Engineer",
		Description: "We are hiring a senior frontend engineer with deep Svelte and React experience.",
		Status:      domain.JobStatusOpen,
		Rubric: []domain.RubricCriterion{
			{Key: "frontend_depth", Label: "Frontend depth", Weight: 3},
			{Key: "system_design", Label: "System design", Weight: 2},
			{Key: "testing_quality", Label: "Testing and quality", Weight: 2},
			{Key: "communication", Label: "Communication", Weight: 1},
		},
	}); err != nil {
		t.Fatalf("seed job: %v", err)
	}
	if err := st.CreateApplication(ctx, domain.Application{
		ID: appID, JobID: jobID, Name: "Budi", Email: "budi@example.com",
		CVText: "Senior Svelte engineer.", Status: domain.ApplicationStatusInvited,
	}); err != nil {
		t.Fatalf("seed application: %v", err)
	}
	raw, hash, err := session.NewToken()
	if err != nil {
		t.Fatalf("new token: %v", err)
	}
	if err := st.CreateSession(ctx, domain.Session{
		ID: sessionID, ApplicationID: appID, TokenHash: hash,
		Status: domain.SessionStatusInvited, ExpiresAt: time.Now().Add(24 * time.Hour), Voice: true,
	}); err != nil {
		t.Fatalf("seed session: %v", err)
	}

	cfg := graphRunsConfig()
	cfg.SessionMaxMinutes = 15
	cfg.SessionMaxQuestions = 15
	fakeTranscriber := transcribe.NewFake()
	router := httpapi.NewRouter(cfg, httpapi.Deps{
		Store:        st,
		LLM:          llm.NewFake(graph.FakeReplies),
		Models:       testGraphModels(),
		AudioStorage: storage.NewFake(),
		Transcriber:  fakeTranscriber,
	})

	start := httptest.NewRequest(http.MethodPost, "/api/v1/sessions/"+raw+"/start", bytes.NewReader([]byte(`{"recording_consent":true}`)))
	start.Header.Set("Content-Type", "application/json")
	startRec := httptest.NewRecorder()
	router.ServeHTTP(startRec, start)
	if startRec.Code != http.StatusOK {
		t.Fatalf("start status = %d, want 200 (body: %s)", startRec.Code, startRec.Body.String())
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/"+raw, nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET session = %d (body: %s)", rec.Code, rec.Body.String())
		}
		var detail struct {
			QuestionCount int `json:"question_count"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &detail); err != nil {
			t.Fatalf("decode session: %v", err)
		}
		if detail.QuestionCount >= 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the opening question never completed")
		}
		time.Sleep(5 * time.Millisecond)
	}

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", `form-data; name="audio"; filename="answer.webm"`)
	header.Set("Content-Type", "audio/webm;codecs=opus")
	part, err := writer.CreatePart(header)
	if err != nil {
		t.Fatalf("CreatePart: %v", err)
	}
	if _, err := part.Write([]byte("webm recording bytes")); err != nil {
		t.Fatalf("write audio: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart: %v", err)
	}
	upload := httptest.NewRequest(http.MethodPost, "/api/v1/sessions/"+raw+"/audio", &body)
	upload.Header.Set("Content-Type", writer.FormDataContentType())
	uploadRec := httptest.NewRecorder()
	router.ServeHTTP(uploadRec, upload)

	if uploadRec.Code != http.StatusOK {
		t.Fatalf("upload status = %d, want 200 (body: %s)", uploadRec.Code, uploadRec.Body.String())
	}
	var got struct {
		AudioID    string `json:"audio_id"`
		Transcript string `json:"transcript"`
		Stored     bool   `json:"stored"`
	}
	if err := json.Unmarshal(uploadRec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode upload: %v", err)
	}
	if got.AudioID == "" || got.Transcript != "fake transcript" || !got.Stored {
		t.Fatalf("upload = %+v, want the fake transcriber's transcript stored", got)
	}
}
