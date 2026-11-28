package graph

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/aangwijaya/talent-scout-server/internal/domain"
	"github.com/aangwijaya/talent-scout-server/internal/llm"
	"github.com/aangwijaya/talent-scout-server/internal/prompts"
	"github.com/aangwijaya/talent-scout-server/internal/store"
)

func TestComputeFluencyMetrics(t *testing.T) {
	spoken := func(id string) domain.Message {
		return domain.Message{
			ID: id, Role: domain.MessageRoleApplicant, Kind: domain.MessageKindAnswer,
			Entry: &domain.MessageEntry{Mode: domain.MessageEntryModeSpoken},
		}
	}
	typed := func(id string) domain.Message {
		return domain.Message{
			ID: id, Role: domain.MessageRoleApplicant, Kind: domain.MessageKindAnswer,
			Entry: &domain.MessageEntry{Mode: domain.MessageEntryModeTyped},
		}
	}
	upload := func(messageID, language string, confidence float64, durationMS int, words ...domain.TranscriptWord) store.AudioUpload {
		return store.AudioUpload{
			ID: messageID + "-audio", SessionID: "s", ObjectPath: "o", ContentType: "audio/webm",
			MessageID: &messageID, DurationMS: durationMS, Language: language, Confidence: confidence,
			Words: words, Counted: true,
		}
	}
	word := func(text string, start, end float64) domain.TranscriptWord {
		return domain.TranscriptWord{Word: text, Start: start, End: end}
	}

	cases := []struct {
		name     string
		messages []domain.Message
		uploads  []store.AudioUpload
		want     domain.Fluency
	}{
		{
			name:     "no pauses",
			messages: []domain.Message{spoken("a")},
			uploads: []store.AudioUpload{upload("a", "en", 0.9, 1500,
				word("I", 0, 0.5), word("built", 0.5, 1.0), word("this", 1.0, 1.5))},
			want: domain.Fluency{
				WordsPerMinute: 120, PauseRatio: 0, FillerRate: 0,
				TranscriptConfidence: 0.9, Languages: []string{"en"}, SpokenAnswers: 1,
			},
		},
		{
			name:     "all pauses",
			messages: []domain.Message{spoken("a")},
			uploads: []store.AudioUpload{upload("a", "en", 0.8, 3500,
				word("I", 0, 0.5), word("built", 1.5, 2.0), word("this", 3.0, 3.5))},
			want: domain.Fluency{
				WordsPerMinute: 51.43, PauseRatio: 0.57, FillerRate: 0,
				TranscriptConfidence: 0.8, Languages: []string{"en"}, SpokenAnswers: 1,
			},
		},
		{
			name:     "no fillers",
			messages: []domain.Message{spoken("a")},
			uploads: []store.AudioUpload{upload("a", "en", 0.9, 1500,
				word("I", 0, 0.5), word("built", 0.5, 1.0), word("this", 1.0, 1.5))},
			want: domain.Fluency{
				WordsPerMinute: 120, PauseRatio: 0, FillerRate: 0,
				TranscriptConfidence: 0.9, Languages: []string{"en"}, SpokenAnswers: 1,
			},
		},
		{
			name:     "all fillers",
			messages: []domain.Message{spoken("a")},
			uploads: []store.AudioUpload{upload("a", "en", 0.7, 1500,
				word("Um,", 0, 0.5), word("uh", 0.5, 1.0), word("hmmm", 1.0, 1.5))},
			want: domain.Fluency{
				WordsPerMinute: 120, PauseRatio: 0, FillerRate: 1,
				TranscriptConfidence: 0.7, Languages: []string{"en"}, SpokenAnswers: 1,
			},
		},
		{
			name:     "gap under the pause threshold",
			messages: []domain.Message{spoken("a")},
			uploads: []store.AudioUpload{upload("a", "en", 0.9, 1900,
				word("I", 0, 0.5), word("built", 0.9, 1.4), word("this", 1.4, 1.9))},
			want: domain.Fluency{
				WordsPerMinute: 94.74, PauseRatio: 0, FillerRate: 0,
				TranscriptConfidence: 0.9, Languages: []string{"en"}, SpokenAnswers: 1,
			},
		},
		{
			name:     "gap over the pause threshold",
			messages: []domain.Message{spoken("a")},
			uploads: []store.AudioUpload{upload("a", "en", 0.9, 2100,
				word("I", 0, 0.5), word("built", 1.1, 1.6), word("this", 1.6, 2.1))},
			want: domain.Fluency{
				WordsPerMinute: 85.71, PauseRatio: 0.29, FillerRate: 0,
				TranscriptConfidence: 0.9, Languages: []string{"en"}, SpokenAnswers: 1,
			},
		},
		{
			name:     "mixed languages",
			messages: []domain.Message{spoken("a"), spoken("b")},
			uploads: []store.AudioUpload{
				upload("a", "en", 0.8, 1000, word("Hello", 0, 0.5), word("there", 0.5, 1.0)),
				upload("b", "id", 0.6, 3000, word("Selamat", 0, 0.5), word("pagi", 0.5, 1.0)),
			},
			want: domain.Fluency{
				WordsPerMinute: 120, PauseRatio: 0, FillerRate: 0,
				TranscriptConfidence: 0.65, Languages: []string{"en", "id"}, SpokenAnswers: 2,
			},
		},
		{
			name:     "no spoken answers",
			messages: []domain.Message{typed("a"), typed("b")},
			want:     domain.Fluency{Languages: []string{}, SpokenAnswers: 0, TypedAnswers: 2},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := computeFluency(tc.messages, tc.uploads)
			if math.Abs(got.WordsPerMinute-tc.want.WordsPerMinute) > 0.01 {
				t.Errorf("words_per_minute = %v, want %v", got.WordsPerMinute, tc.want.WordsPerMinute)
			}
			if math.Abs(got.PauseRatio-tc.want.PauseRatio) > 0.01 {
				t.Errorf("pause_ratio = %v, want %v", got.PauseRatio, tc.want.PauseRatio)
			}
			if math.Abs(got.FillerRate-tc.want.FillerRate) > 0.01 {
				t.Errorf("filler_rate = %v, want %v", got.FillerRate, tc.want.FillerRate)
			}
			if math.Abs(got.TranscriptConfidence-tc.want.TranscriptConfidence) > 0.001 {
				t.Errorf("transcript_confidence = %v, want %v", got.TranscriptConfidence, tc.want.TranscriptConfidence)
			}
			if strings.Join(got.Languages, ",") != strings.Join(tc.want.Languages, ",") {
				t.Errorf("languages = %v, want %v", got.Languages, tc.want.Languages)
			}
			if got.SpokenAnswers != tc.want.SpokenAnswers || got.TypedAnswers != tc.want.TypedAnswers {
				t.Errorf("answers = %d spoken/%d typed, want %d/%d", got.SpokenAnswers, got.TypedAnswers, tc.want.SpokenAnswers, tc.want.TypedAnswers)
			}
		})
	}
}

type fluencyAnswer struct {
	text       string
	spoken     bool
	language   string
	confidence float64
	words      []domain.TranscriptWord
}

type fluencyScenario struct {
	store   *store.Memory
	session domain.Session
	client  *stubClient
	events  []domain.NodeEvent
}

func timing(text string, start, end float64) domain.TranscriptWord {
	return domain.TranscriptWord{Word: text, Start: start, End: end}
}

// runFluencyScenario drives the three-topic fake interview. Spoken answers are
// uploaded and attached before sending, so the run has word timings at scoring.
func runFluencyScenario(t *testing.T, voice bool, answers []fluencyAnswer, reply string, fluencyErr error) fluencyScenario {
	t.Helper()
	return runFluencyScenarioWithStore(t, voice, answers, reply, fluencyErr, nil)
}

// runFluencyScenarioWithStore is runFluencyScenario with an optional wrapper
// around the store the session manager uses. The fixture keeps the underlying
// memory store for assertions.
func runFluencyScenarioWithStore(t *testing.T, voice bool, answers []fluencyAnswer, reply string, fluencyErr error, wrap func(store.Store) store.Store) fluencyScenario {
	t.Helper()
	st, sess := seedGraphSession(t)
	if voice {
		sess.Voice = true
		if err := st.UpdateSession(context.Background(), sess); err != nil {
			t.Fatalf("UpdateSession(voice): %v", err)
		}
	}
	client := &stubClient{
		stream: func(req llm.Request, onDelta func(string) error) (llm.Response, error) {
			content := FakeReplies[req.Node]
			if onDelta != nil {
				if err := onDelta(content); err != nil {
					return llm.Response{}, err
				}
			}
			return llm.Response{Content: content}, nil
		},
		chat: func(req llm.Request) (llm.Response, error) {
			switch req.Node {
			case "judge":
				return sufficientJudgeReply()
			case "fluency":
				if fluencyErr != nil {
					return llm.Response{}, fluencyErr
				}
				return llm.Response{Content: reply}, nil
			default:
				return llm.Response{Content: FakeReplies[req.Node]}, nil
			}
		},
	}
	var managerStore store.Store = st
	if wrap != nil {
		managerStore = wrap(st)
	}
	manager := scoringManager(t, managerStore, client, 15)
	if err := manager.Start(context.Background(), sess); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitIdle(t, manager, sess.ID)
	for _, answer := range answers {
		if answer.spoken {
			duration := 0
			if len(answer.words) > 0 {
				duration = int(math.Round(answer.words[len(answer.words)-1].End * 1000))
			}
			upload := store.AudioUpload{
				ID: domain.NewID(), SessionID: sess.ID, ObjectPath: "sessions/" + sess.ID + "/audio.webm",
				ContentType: "audio/webm", DurationMS: duration, Transcript: answer.text,
				Language: answer.language, Confidence: answer.confidence, Words: answer.words,
				Counted: true, Stored: true, UploadedAt: time.Now().UTC(),
			}
			if err := st.CreateAudioUpload(context.Background(), upload); err != nil {
				t.Fatalf("CreateAudioUpload: %v", err)
			}
			entry := &domain.MessageEntry{Mode: domain.MessageEntryModeSpoken}
			if _, err := manager.SubmitAnswerWithAudio(context.Background(), sess, answer.text, entry, upload.ID); err != nil {
				t.Fatalf("SubmitAnswerWithAudio: %v", err)
			}
		} else {
			entry := &domain.MessageEntry{Mode: domain.MessageEntryModeTyped, FallbackReason: domain.MessageEntryFallbackChoseText}
			if _, err := manager.SubmitAnswerWithEntry(context.Background(), sess, answer.text, entry); err != nil {
				t.Fatalf("SubmitAnswerWithEntry: %v", err)
			}
		}
		waitIdle(t, manager, sess.ID)
	}
	return fluencyScenario{store: st, session: sess, client: client, events: sessionEvents(t, st, sess.ID)}
}

func spokenAnswers() []fluencyAnswer {
	return []fluencyAnswer{
		{
			text: "I moved the state into a separate store so each page stayed small and easy to test.", spoken: true, language: "en", confidence: 0.9,
			words: []domain.TranscriptWord{timing("I", 0, 0.2), timing("moved", 0.2, 0.6), timing("the", 0.7, 0.9), timing("state", 0.9, 1.4), timing("into", 1.5, 1.8), timing("a", 1.8, 2.0), timing("separate", 2.0, 2.6), timing("store", 2.6, 3.1)},
		},
		{
			text: "Props were crossing four layers, and I explained the tradeoff to the team before we changed it.", spoken: true, language: "en", confidence: 0.8,
			words: []domain.TranscriptWord{timing("Props", 0, 0.3), timing("were", 0.3, 0.6), timing("crossing", 0.6, 1.1), timing("four", 1.2, 1.5), timing("layers", 1.5, 2.0)},
		},
		{
			text: "I tested accessibility carefully.", spoken: true, language: "id", confidence: 0.7,
			words: []domain.TranscriptWord{timing("I", 0, 0.2), timing("tested", 0.2, 0.7), timing("accessibility", 0.7, 1.4)},
		},
	}
}

func TestFluencyTextSessionIsSkippedAndLeavesNull(t *testing.T) {
	scenario := runFluencyScenario(t, false, []fluencyAnswer{
		{text: "answer one"}, {text: "answer two"}, {text: "answer three"},
	}, FakeReplies["fluency"], nil)

	app, err := scenario.store.GetApplication(context.Background(), scenario.session.ApplicationID)
	if err != nil {
		t.Fatalf("GetApplication: %v", err)
	}
	if app.Fluency != nil {
		t.Errorf("fluency = %+v, want null for a text session", app.Fluency)
	}

	var skipped, started bool
	var order []string
	for _, event := range scenario.events {
		if event.Type == EventNodeStarted && event.NodeID != nil && *event.NodeID == "fluency" {
			started = true
		}
		if event.Type == EventNodeSkipped && event.NodeID != nil && *event.NodeID == "fluency" {
			var payload NodeSkipped
			if err := json.Unmarshal(event.Payload, &payload); err != nil {
				t.Fatalf("unmarshal node.skipped: %v", err)
			}
			if payload.Reason != "text_session" {
				t.Errorf("fluency skip reason = %q, want text_session", payload.Reason)
			}
			skipped = true
		}
		if event.Type == EventEdgeTraversed {
			var payload EdgeTraversed
			if err := json.Unmarshal(event.Payload, &payload); err != nil {
				t.Fatalf("unmarshal edge: %v", err)
			}
			order = append(order, payload.From+">"+payload.To)
		}
	}
	if !skipped || started {
		t.Errorf("fluency skipped=%v started=%v, want skipped and not started", skipped, started)
	}
	index := func(want string) int {
		for i, got := range order {
			if got == want {
				return i
			}
		}
		return -1
	}
	authToFluency := index("authenticity>fluency")
	fluencyToGate := index("fluency>recruiter_gate")
	if authToFluency < 0 || fluencyToGate < 0 || authToFluency > fluencyToGate {
		t.Errorf("edges = %v, want authenticity→fluency then fluency→recruiter_gate", order)
	}
	for _, edge := range order {
		if edge == "authenticity>recruiter_gate" {
			t.Errorf("run still traverses authenticity→recruiter_gate directly: %v", order)
		}
	}

	var runStarted RunStarted
	for _, event := range scenario.events {
		if event.Type == EventRunStarted {
			if err := json.Unmarshal(event.Payload, &runStarted); err != nil {
				t.Fatalf("unmarshal run.started: %v", err)
			}
		}
	}
	var sawAuthFluency, sawFluencyGate, sawDirect bool
	for _, edge := range runStarted.Edges {
		switch {
		case edge.From == "authenticity" && edge.To == "fluency":
			sawAuthFluency = true
		case edge.From == "fluency" && edge.To == "recruiter_gate":
			sawFluencyGate = true
		case edge.From == "authenticity" && edge.To == "recruiter_gate":
			sawDirect = true
		}
	}
	if !sawAuthFluency || !sawFluencyGate || sawDirect {
		t.Errorf("run.started edges = %+v, want authenticity→fluency→recruiter_gate", runStarted.Edges)
	}
}

func TestFluencyVoiceSessionStoresCodeMetricsNotModelOnes(t *testing.T) {
	answers := spokenAnswers()
	reply := `{"summary":"Steady pace with a few short pauses.","words_per_minute":9999,"pause_ratio":0.99}`
	scenario := runFluencyScenario(t, true, answers, reply, nil)

	app, err := scenario.store.GetApplication(context.Background(), scenario.session.ApplicationID)
	if err != nil {
		t.Fatalf("GetApplication: %v", err)
	}
	if app.Fluency == nil {
		t.Fatal("fluency is null, want the stored assessment")
	}
	messages, err := scenario.store.ListMessagesBySession(context.Background(), scenario.session.ID, false)
	if err != nil {
		t.Fatalf("ListMessagesBySession: %v", err)
	}
	uploads, err := scenario.store.ListAudioUploadsBySession(context.Background(), scenario.session.ID)
	if err != nil {
		t.Fatalf("ListAudioUploadsBySession: %v", err)
	}
	want := computeFluency(messages, uploads)
	if app.Fluency.WordsPerMinute != want.WordsPerMinute || app.Fluency.PauseRatio != want.PauseRatio ||
		app.Fluency.FillerRate != want.FillerRate || app.Fluency.TranscriptConfidence != want.TranscriptConfidence ||
		app.Fluency.SpokenAnswers != want.SpokenAnswers || app.Fluency.TypedAnswers != want.TypedAnswers ||
		strings.Join(app.Fluency.Languages, ",") != strings.Join(want.Languages, ",") {
		t.Errorf("stored fluency = %+v, want the code-computed %+v (never the model's numbers)", app.Fluency, want)
	}
	if app.Fluency.WordsPerMinute == 9999 || app.Fluency.PauseRatio == 0.99 {
		t.Errorf("stored fluency took the model's metrics: %+v", app.Fluency)
	}
	if strings.TrimSpace(app.Fluency.Summary) == "" {
		t.Error("summary is empty, want the model's short note")
	}
	if app.Fluency.AssessedAt.IsZero() {
		t.Error("assessed_at is zero")
	}

	var fluencyCall bool
	for _, call := range scenario.client.Calls() {
		if call.Node != "fluency" {
			continue
		}
		fluencyCall = true
		input := call.Messages[len(call.Messages)-1].Content
		outside := input
		for _, answer := range answers {
			wrapped := prompts.WrapUntrusted("spoken_transcript", answer.text)
			if !strings.Contains(input, wrapped) {
				t.Errorf("fluency input does not wrap transcript %q as untrusted data:\n%s", answer.text, input)
			}
			outside = strings.ReplaceAll(outside, wrapped, "")
		}
		for _, answer := range answers {
			if strings.Contains(outside, answer.text) {
				t.Errorf("transcript %q appears outside its untrusted block:\n%s", answer.text, input)
			}
		}
		if !strings.Contains(input, "words_per_minute:") {
			t.Errorf("fluency input does not include the code-computed metrics:\n%s", input)
		}
	}
	if !fluencyCall {
		t.Fatal("fluency node made no LLM call in a voice session")
	}

	var completed bool
	for _, event := range scenario.events {
		if event.Type == EventNodeCompleted && event.NodeID != nil && *event.NodeID == "fluency" {
			completed = true
		}
	}
	if !completed {
		t.Error("no node.completed(fluency) recorded")
	}
}

func TestFluencyVoiceSessionWithNoSpokenAnswersStillRuns(t *testing.T) {
	scenario := runFluencyScenario(t, true, []fluencyAnswer{
		{text: "answer one"}, {text: "answer two"}, {text: "answer three"},
	}, FakeReplies["fluency"], nil)

	app, err := scenario.store.GetApplication(context.Background(), scenario.session.ApplicationID)
	if err != nil {
		t.Fatalf("GetApplication: %v", err)
	}
	if app.Fluency == nil {
		t.Fatal("fluency is null, want an assessment with zero spoken answers")
	}
	if app.Fluency.SpokenAnswers != 0 || app.Fluency.TypedAnswers != 3 {
		t.Errorf("answers = %d spoken/%d typed, want 0/3", app.Fluency.SpokenAnswers, app.Fluency.TypedAnswers)
	}
	if app.Fluency.WordsPerMinute != 0 || app.Fluency.PauseRatio != 0 {
		t.Errorf("pace/pause = %v/%v, want zero when there is no speech", app.Fluency.WordsPerMinute, app.Fluency.PauseRatio)
	}
	for _, call := range scenario.client.Calls() {
		if call.Node != "fluency" {
			continue
		}
		input := call.Messages[len(call.Messages)-1].Content
		if !strings.Contains(input, "spoken_answers: 0") {
			t.Errorf("fluency input does not state that there were no spoken answers:\n%s", input)
		}
		if !strings.Contains(strings.ToLower(input), "every answer was typed") {
			t.Errorf("fluency input does not tell the model every answer was typed:\n%s", input)
		}
	}
}

func TestFluencyFailureLeavesRunSucceededAndNull(t *testing.T) {
	scenario := runFluencyScenario(t, true, spokenAnswers(), "", errors.New("fluency transport failed"))

	app, err := scenario.store.GetApplication(context.Background(), scenario.session.ApplicationID)
	if err != nil {
		t.Fatalf("GetApplication: %v", err)
	}
	if app.Fluency != nil {
		t.Errorf("fluency = %+v, want null after a failed check", app.Fluency)
	}
	if app.Status != domain.ApplicationStatusReview {
		t.Errorf("application status = %q, want review", app.Status)
	}
	run, err := scenario.store.GetGraphRunBySession(context.Background(), scenario.session.ID)
	if err != nil {
		t.Fatalf("GetGraphRunBySession: %v", err)
	}
	if run.Status != domain.GraphRunStatusSucceeded {
		t.Errorf("run status = %q, want succeeded", run.Status)
	}

	var failed, willRetry, completed bool
	for _, event := range scenario.events {
		if event.Type == EventNodeFailed && event.NodeID != nil && *event.NodeID == "fluency" {
			var payload NodeFailed
			if err := json.Unmarshal(event.Payload, &payload); err != nil {
				t.Fatalf("unmarshal node.failed: %v", err)
			}
			failed = true
			willRetry = payload.WillRetry
		}
		if event.Type == EventRunCompleted {
			completed = true
		}
		if event.Type == EventRunFailed {
			t.Error("run.failed emitted; an advisory fluency failure must not fail the run")
		}
	}
	if !failed {
		t.Error("no node.failed(fluency) recorded")
	}
	if willRetry {
		t.Error("fluency failure recorded will_retry=true, want false after the retry")
	}
	if !completed {
		t.Error("no run.completed recorded")
	}
}

func TestFluencyDoesNotChangeScores(t *testing.T) {
	answers := spokenAnswers()
	withFluency := runFluencyScenario(t, true, answers, FakeReplies["fluency"], nil)
	withoutFluency := runFluencyScenario(t, false, answers, FakeReplies["fluency"], nil)

	withScores := scorerScores(t, withFluency.store, withFluency.session.ApplicationID)
	withoutScores := scorerScores(t, withoutFluency.store, withoutFluency.session.ApplicationID)
	if len(withScores) != len(withoutScores) {
		t.Fatalf("scores = %d with fluency and %d without", len(withScores), len(withoutScores))
	}
	for key, withScore := range withScores {
		withoutScore, ok := withoutScores[key]
		if !ok {
			t.Fatalf("score for %q missing without fluency", key)
		}
		if withScore.Status != withoutScore.Status || scoreValue(withScore) != scoreValue(withoutScore) {
			t.Errorf("%s = %v/%s with fluency and %v/%s without", key, scoreValue(withScore), withScore.Status, scoreValue(withoutScore), withoutScore.Status)
		}
	}

	app, err := withFluency.store.GetApplication(context.Background(), withFluency.session.ApplicationID)
	if err != nil {
		t.Fatalf("GetApplication: %v", err)
	}
	job, err := withFluency.store.GetJob(context.Background(), app.JobID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if weightedOverall(withScores, job.Rubric) != weightedOverall(withoutScores, job.Rubric) {
		t.Errorf("overall score changed: %v with fluency, %v without", weightedOverall(withScores, job.Rubric), weightedOverall(withoutScores, job.Rubric))
	}

	for _, scenario := range []fluencyScenario{withFluency, withoutFluency} {
		for _, call := range scenario.client.Calls() {
			if call.Node != "scorer" {
				continue
			}
			for _, message := range call.Messages {
				for _, banned := range []string{"words_per_minute", "pause_ratio", "filler_rate", "fluency"} {
					if strings.Contains(message.Content, banned) {
						t.Errorf("scorer request contains %q; fluency must not reach the scorer", banned)
					}
				}
			}
		}
	}
}

// failingUploadStore fails the session's upload load, as a store outage would.
type failingUploadStore struct {
	store.Store
}

func (s *failingUploadStore) ListAudioUploadsBySession(context.Context, string) ([]store.AudioUpload, error) {
	return nil, errors.New("upload store unavailable")
}

// An upload-load error is advisory only: the run still succeeds, the application
// reaches review, and fluency stays null for both text and voice sessions.
func TestFluencyUploadLoadErrorLeavesRunSucceededAndNull(t *testing.T) {
	tests := []struct {
		name    string
		voice   bool
		answers []fluencyAnswer
	}{
		{
			name:  "text session",
			voice: false,
			answers: []fluencyAnswer{
				{text: "answer one"}, {text: "answer two"}, {text: "answer three"},
			},
		},
		{
			name:    "voice session",
			voice:   true,
			answers: spokenAnswers(),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			wrap := func(base store.Store) store.Store {
				return &failingUploadStore{Store: base}
			}
			scenario := runFluencyScenarioWithStore(t, tc.voice, tc.answers, FakeReplies["fluency"], nil, wrap)

			app, err := scenario.store.GetApplication(context.Background(), scenario.session.ApplicationID)
			if err != nil {
				t.Fatalf("GetApplication: %v", err)
			}
			if app.Fluency != nil {
				t.Errorf("fluency = %+v, want null after the upload load failed", app.Fluency)
			}
			if app.Status != domain.ApplicationStatusReview {
				t.Errorf("application status = %q, want review", app.Status)
			}
			run, err := scenario.store.GetGraphRunBySession(context.Background(), scenario.session.ID)
			if err != nil {
				t.Fatalf("GetGraphRunBySession: %v", err)
			}
			if run.Status != domain.GraphRunStatusSucceeded {
				t.Errorf("run status = %q, want succeeded", run.Status)
			}

			var started, failed bool
			for _, event := range scenario.events {
				if event.NodeID == nil || *event.NodeID != "fluency" {
					continue
				}
				switch event.Type {
				case EventNodeStarted:
					started = true
				case EventNodeFailed:
					var payload NodeFailed
					if err := json.Unmarshal(event.Payload, &payload); err != nil {
						t.Fatalf("unmarshal node.failed: %v", err)
					}
					failed = true
					if payload.Error != "llm_error" || payload.WillRetry {
						t.Errorf("node.failed = %+v, want llm_error with will_retry=false", payload)
					}
				}
			}
			if tc.voice {
				if !started || !failed {
					t.Errorf("fluency started=%v failed=%v, want both", started, failed)
				}
			} else if started || failed {
				t.Errorf("fluency started=%v failed=%v, want the text-session skip only", started, failed)
			}
		})
	}
}
