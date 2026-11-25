package httpapi_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aangwijaya/talent-scout-server/internal/config"
	"github.com/aangwijaya/talent-scout-server/internal/conformance"
	"github.com/aangwijaya/talent-scout-server/internal/domain"
	httpapi "github.com/aangwijaya/talent-scout-server/internal/http"
	"github.com/aangwijaya/talent-scout-server/internal/llm"
	"github.com/aangwijaya/talent-scout-server/internal/store"
)

const routerSuggestionJSON = `{"rubric":[
	{"key":"frontend_depth","label":"Frontend depth","description":"Svelte, React and rendering depth.","weight":3},
	{"key":"system_design","label":"System design","description":"Component boundaries and data flow.","weight":2},
	{"key":"communication","label":"Communication","description":"Clarity and collaboration.","weight":1}
]}`

func jobManageModels() config.ModelsConfig {
	return config.ModelsConfig{
		Default: config.Defaults{Temperature: 0.3, TimeoutSeconds: 30, MaxRetriesOnHTTPError: 1},
		Nodes: map[string]config.Node{
			"planner":          {Model: "deepseek/deepseek-v4.1-flash", MaxTokens: 800},
			"interviewer":      {Model: "z-ai/glm-5.3-flash", MaxTokens: 300, Stream: true},
			"judge":            {Model: "deepseek/deepseek-v4.1-flash", MaxTokens: 200},
			"scorer":           {Model: "deepseek/deepseek-v4.1-flash", MaxTokens: 1200},
			"rubric_suggester": {Model: "deepseek/deepseek-v4.1-flash", MaxTokens: 2000},
		},
		Prices: map[string]config.Price{
			"deepseek/deepseek-v4.1-flash": {In: 0.045, Out: 0.5},
			"z-ai/glm-5.3-flash":           {In: 0.15, Out: 0.5},
		},
	}
}

func jobManageRouter(t *testing.T, fake *llm.Fake) (http.Handler, *store.Memory, config.Config) {
	t.Helper()
	st := store.NewMemory()
	cfg := config.Config{
		Version:           "0.1.0",
		LLMMode:           "fake",
		EmailMode:         "log",
		PublicAppURL:      "https://talentscout-id.vercel.app",
		RecruiterPassword: "correct horse",
		AuthTokenSecret:   "0123456789abcdef0123456789abcdef",
		BFFSharedSecret:   "shared-secret",
	}
	router := httpapi.NewRouter(cfg, httpapi.Deps{
		Store:  st,
		LLM:    fake,
		Models: jobManageModels(),
	})
	return router, st, cfg
}

func serveJSON(router http.Handler, method, target, token, body string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func TestJobManagementMatchesContract(t *testing.T) {
	fake := llm.NewFake(map[string]string{"rubric_suggester": routerSuggestionJSON})
	router, _, _ := jobManageRouter(t, fake)
	token := login(t, router)
	validator := conformance.New(t)

	createBody := `{
		"title":"Senior Frontend Engineer",
		"description":"We are hiring a senior frontend engineer with deep Svelte and React experience.",
		"rubric":[
			{"key":"frontend_depth","label":"Frontend depth","description":"Svelte, React and rendering depth.","weight":3},
			{"key":"system_design","label":"System design","description":"Component boundaries and data flow.","weight":2},
			{"key":"communication","label":"Communication","description":"Clarity and collaboration.","weight":1}
		]
	}`
	createReq := httptest.NewRequest(http.MethodPost, "/api/v1/jobs", strings.NewReader(createBody))
	createReq.Header.Set("Authorization", "Bearer "+token)
	createRec := httptest.NewRecorder()
	router.ServeHTTP(createRec, createReq)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201 (body: %s)", createRec.Code, createRec.Body.String())
	}
	validator.ValidateResponse(t, createReq, createRec.Result())

	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(createRec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created job: %v", err)
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/"+created.ID, nil)
	getReq.Header.Set("Authorization", "Bearer "+token)
	getRec := httptest.NewRecorder()
	router.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("get status = %d, want 200 (body: %s)", getRec.Code, getRec.Body.String())
	}
	validator.ValidateResponse(t, getReq, getRec.Result())

	patchReq := httptest.NewRequest(http.MethodPatch, "/api/v1/jobs/"+created.ID, strings.NewReader(`{"title":"Staff Frontend Engineer"}`))
	patchReq.Header.Set("Authorization", "Bearer "+token)
	patchRec := httptest.NewRecorder()
	router.ServeHTTP(patchRec, patchReq)
	if patchRec.Code != http.StatusOK {
		t.Fatalf("patch status = %d, want 200 (body: %s)", patchRec.Code, patchRec.Body.String())
	}
	validator.ValidateResponse(t, patchReq, patchRec.Result())

	suggestReq := httptest.NewRequest(http.MethodPost, "/api/v1/jobs/rubric-suggestion", strings.NewReader(`{
		"title":"Senior Frontend Engineer",
		"description":"We are hiring a senior frontend engineer with deep Svelte and React experience."
	}`))
	suggestReq.Header.Set("Authorization", "Bearer "+token)
	suggestRec := httptest.NewRecorder()
	router.ServeHTTP(suggestRec, suggestReq)
	if suggestRec.Code != http.StatusOK {
		t.Fatalf("suggestion status = %d, want 200 (body: %s)", suggestRec.Code, suggestRec.Body.String())
	}
	validator.ValidateResponse(t, suggestReq, suggestRec.Result())

	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/jobs", nil)
	listReq.Header.Set("Authorization", "Bearer "+token)
	listRec := httptest.NewRecorder()
	router.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200 (body: %s)", listRec.Code, listRec.Body.String())
	}
	validator.ValidateResponse(t, listReq, listRec.Result())
}

func TestJobManagementRequiresRecruiter(t *testing.T) {
	fake := llm.NewFake(map[string]string{"rubric_suggester": routerSuggestionJSON})
	router, _, _ := jobManageRouter(t, fake)
	validator := conformance.New(t)

	tests := []struct {
		name   string
		method string
		target string
		body   string
	}{
		{name: "create", method: http.MethodPost, target: "/api/v1/jobs", body: `{"title":"Senior Frontend Engineer"}`},
		{name: "update", method: http.MethodPatch, target: "/api/v1/jobs/7c9e6679-0000-4000-8000-000000000099", body: `{"title":"Staff Frontend Engineer"}`},
		{name: "suggest rubric", method: http.MethodPost, target: "/api/v1/jobs/rubric-suggestion", body: `{"title":"Senior Frontend Engineer","description":"We are hiring a senior frontend engineer with deep Svelte and React experience."}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.target, strings.NewReader(tc.body))
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401 (body: %s)", rec.Code, rec.Body.String())
			}
			validator.ValidateResponse(t, req, rec.Result())
		})
	}
}

func TestRubricSuggestionRateLimitIsGlobal(t *testing.T) {
	fake := llm.NewFake(map[string]string{"rubric_suggester": routerSuggestionJSON})
	router, _, _ := jobManageRouter(t, fake)
	token := login(t, router)
	validator := conformance.New(t)

	body := `{"title":"Senior Frontend Engineer","description":"We are hiring a senior frontend engineer with deep Svelte and React experience."}`
	for i := 0; i < 10; i++ {
		headers := map[string]string{
			"X-BFF-Secret":    "shared-secret",
			"X-Forwarded-For": fmt.Sprintf("10.0.0.%d", i+1),
		}
		rec := serveJSON(router, http.MethodPost, "/api/v1/jobs/rubric-suggestion", token, body, headers)
		if rec.Code != http.StatusOK {
			t.Fatalf("suggestion %d status = %d, want 200 (body: %s)", i+1, rec.Code, rec.Body.String())
		}
	}

	headers := map[string]string{
		"X-BFF-Secret":    "shared-secret",
		"X-Forwarded-For": "10.0.0.250",
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/jobs/rubric-suggestion", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("11th status = %d, want 429 (body: %s)", rec.Code, rec.Body.String())
	}
	validator.ValidateResponse(t, req, rec.Result())
	if rec.Header().Get("Retry-After") == "" {
		t.Error("Retry-After header is missing")
	}
}

func TestCreateJobLimitContract(t *testing.T) {
	fake := llm.NewFake(map[string]string{"rubric_suggester": routerSuggestionJSON})
	router, st, _ := jobManageRouter(t, fake)
	token := login(t, router)
	validator := conformance.New(t)

	for i := 0; i < 20; i++ {
		if err := st.CreateJob(context.Background(), domain.Job{
			ID:          domain.NewID(),
			Title:       "Senior Frontend Engineer",
			Description: "We are hiring a senior frontend engineer with deep Svelte and React experience.",
			Rubric: []domain.RubricCriterion{
				{Key: "frontend_depth", Label: "Frontend depth", Description: "Svelte, React and rendering depth.", Weight: 3},
				{Key: "system_design", Label: "System design", Description: "Component boundaries and data flow.", Weight: 2},
				{Key: "communication", Label: "Communication", Description: "Clarity and collaboration.", Weight: 1},
			},
			Status: domain.JobStatusOpen,
		}); err != nil {
			t.Fatalf("seed job %d: %v", i, err)
		}
	}

	body := `{
		"title":"Senior Frontend Engineer",
		"description":"We are hiring a senior frontend engineer with deep Svelte and React experience.",
		"rubric":[
			{"key":"frontend_depth","label":"Frontend depth","description":"Svelte, React and rendering depth.","weight":3},
			{"key":"system_design","label":"System design","description":"Component boundaries and data flow.","weight":2},
			{"key":"communication","label":"Communication","description":"Clarity and collaboration.","weight":1}
		]
	}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/jobs", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body: %s)", rec.Code, rec.Body.String())
	}
	validator.ValidateResponse(t, req, rec.Result())
}

func TestRubricSuggestionInvalidOutputIs502(t *testing.T) {
	fake := llm.NewFake(map[string]string{"rubric_suggester": `{"rubric":[]}`})
	router, _, _ := jobManageRouter(t, fake)
	token := login(t, router)
	validator := conformance.New(t)

	body := `{"title":"Senior Frontend Engineer","description":"We are hiring a senior frontend engineer with deep Svelte and React experience."}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/jobs/rubric-suggestion", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502 (body: %s)", rec.Code, rec.Body.String())
	}
	validator.ValidateResponse(t, req, rec.Result())
	if calls := len(fake.Calls()); calls != 2 {
		t.Errorf("llm calls = %d, want 2", calls)
	}
}
