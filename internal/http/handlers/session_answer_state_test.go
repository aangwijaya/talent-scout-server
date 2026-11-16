package handlers_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/aangwijaya/talent-scout-server/internal/domain"
	"github.com/aangwijaya/talent-scout-server/internal/graph"
	"github.com/aangwijaya/talent-scout-server/internal/llm"
	"github.com/aangwijaya/talent-scout-server/internal/store"
)

func postAnswer(t *testing.T, f *chatFixture, content string) *httptest.ResponseRecorder {
	t.Helper()
	payload, err := json.Marshal(map[string]string{"content": content})
	if err != nil {
		t.Fatalf("marshal answer: %v", err)
	}
	rec := httptest.NewRecorder()
	f.handler.SendMessage(rec, chatRequest(http.MethodPost, f.raw, string(payload)))
	return rec
}

func waitRetryableError(t *testing.T, events <-chan domain.NodeEvent) {
	t.Helper()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for {
		select {
		case event := <-events:
			if event.Type != graph.EventError {
				continue
			}
			var payload graph.ErrorEvent
			if err := json.Unmarshal(event.Payload, &payload); err != nil {
				t.Fatalf("unmarshal error event: %v", err)
			}
			if payload.Retryable {
				return
			}
		case <-timer.C:
			t.Fatal("timed out waiting for a retryable error event")
		}
	}
}

// TestSendMessageWhileInterviewerStreamingIsInvalidState covers the
// message.completed gate: before the reply is published the input stays locked.
func TestSendMessageWhileInterviewerStreamingIsInvalidState(t *testing.T) {
	var gate *gatedEventStore
	f := newChatFixtureWrapped(t, false, func(base store.Store) store.Store {
		gate = newGatedEventStore(base, graph.EventNodeStarted, "interviewer", 1)
		return gate
	})

	rec := httptest.NewRecorder()
	f.handler.Start(rec, chatRequest(http.MethodPost, f.raw, ""))
	if rec.Code != http.StatusOK {
		t.Fatalf("start status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	waitGate(t, gate.entered)

	rec = postAnswer(t, f, "I optimized rendering.")
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 while the interviewer is still replying (body: %s)", rec.Code, rec.Body.String())
	}
	if code, _, _ := decodeError(t, rec); code != "invalid_state" {
		t.Errorf("code = %q, want invalid_state", code)
	}

	close(gate.release)
	waitManager(t, f.manager, f.session.ID)
}

// interviewerUnavailableClient fails the interviewer stream after one transport
// retry so the session ends up waiting for POST /retry.
type interviewerUnavailableClient struct{}

func (interviewerUnavailableClient) Chat(_ context.Context, req llm.Request) (llm.Response, error) {
	return llm.Response{Content: graph.FakeReplies[req.Node]}, nil
}

func (interviewerUnavailableClient) Stream(_ context.Context, req llm.Request, onDelta func(string) error) (llm.Response, error) {
	if req.Node == "interviewer" {
		if req.OnRetry != nil {
			req.OnRetry(1, llm.ErrUnavailable)
		}
		return llm.Response{}, fmt.Errorf("%w: upstream 503", llm.ErrUnavailable)
	}
	content := graph.FakeReplies[req.Node]
	if onDelta != nil {
		if err := onDelta(content); err != nil {
			return llm.Response{}, err
		}
	}
	return llm.Response{Content: content}, nil
}

// TestSendMessageWhileRetryPendingIsInvalidState covers the waitingRetry guard.
func TestSendMessageWhileRetryPendingIsInvalidState(t *testing.T) {
	f := newChatFixtureWithLLM(t, false, nil, interviewerUnavailableClient{})
	events, cancel := f.bus.SubscribeSession(f.session.ID)
	defer cancel()

	rec := httptest.NewRecorder()
	f.handler.Start(rec, chatRequest(http.MethodPost, f.raw, ""))
	if rec.Code != http.StatusOK {
		t.Fatalf("start status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	waitRetryableError(t, events)

	rec = postAnswer(t, f, "I optimized rendering.")
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 while a retry is pending (body: %s)", rec.Code, rec.Body.String())
	}
	if code, _, _ := decodeError(t, rec); code != "invalid_state" {
		t.Errorf("code = %q, want invalid_state", code)
	}
}

// TestSendMessageAfterSessionEndedIsGone covers the 410 cases: a finished
// session, an expired invite, and a session past its time limit.
func TestSendMessageAfterSessionEndedIsGone(t *testing.T) {
	t.Run("completed", func(t *testing.T) {
		f := newChatFixture(t, false)
		rec := httptest.NewRecorder()
		f.handler.Start(rec, chatRequest(http.MethodPost, f.raw, ""))
		if rec.Code != http.StatusOK {
			t.Fatalf("start status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
		}
		waitManager(t, f.manager, f.session.ID)
		if _, err := f.store.FinishSession(context.Background(), f.session.ID, domain.FinishReasonCompleted, time.Now().UTC()); err != nil {
			t.Fatalf("FinishSession: %v", err)
		}

		rec = postAnswer(t, f, "I optimized rendering.")
		if rec.Code != http.StatusGone {
			t.Fatalf("status = %d, want 410 (body: %s)", rec.Code, rec.Body.String())
		}
		if code, _, _ := decodeError(t, rec); code != "session_completed" {
			t.Errorf("code = %q, want session_completed", code)
		}
	})

	t.Run("expired invite", func(t *testing.T) {
		f := newChatFixture(t, false)
		sess, err := f.store.GetSession(context.Background(), f.session.ID)
		if err != nil {
			t.Fatalf("GetSession: %v", err)
		}
		sess.ExpiresAt = time.Now().Add(-time.Minute)
		if err := f.store.UpdateSession(context.Background(), sess); err != nil {
			t.Fatalf("UpdateSession: %v", err)
		}

		rec := postAnswer(t, f, "I optimized rendering.")
		if rec.Code != http.StatusGone {
			t.Fatalf("status = %d, want 410 (body: %s)", rec.Code, rec.Body.String())
		}
		if code, _, _ := decodeError(t, rec); code != "session_expired" {
			t.Errorf("code = %q, want session_expired", code)
		}
	})

	t.Run("time is up", func(t *testing.T) {
		f := newChatFixture(t, true)
		sess, err := f.store.GetSession(context.Background(), f.session.ID)
		if err != nil {
			t.Fatalf("GetSession: %v", err)
		}
		past := time.Now().Add(-20 * time.Minute)
		sess.StartedAt = &past
		if err := f.store.UpdateSession(context.Background(), sess); err != nil {
			t.Fatalf("UpdateSession: %v", err)
		}

		rec := postAnswer(t, f, "I optimized rendering.")
		if rec.Code != http.StatusGone {
			t.Fatalf("status = %d, want 410 (body: %s)", rec.Code, rec.Body.String())
		}
		if code, _, _ := decodeError(t, rec); code != "session_completed" {
			t.Errorf("code = %q, want session_completed", code)
		}
	})
}

// TestSendMessageWhenSessionEndedWhileWaitingIsGone covers the mapping for a
// finish that happens after the handler read an active session but before
// SubmitAnswer claimed the turn: the request must get 410, not 409.
func TestSendMessageWhenSessionEndedWhileWaitingIsGone(t *testing.T) {
	f := newChatFixture(t, false)
	rec := httptest.NewRecorder()
	f.handler.Start(rec, chatRequest(http.MethodPost, f.raw, ""))
	if rec.Code != http.StatusOK {
		t.Fatalf("start status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	waitManager(t, f.manager, f.session.ID)

	if handled := f.registry.NotifyFinished(f.session.ID, domain.FinishReasonTimeLimit); !handled {
		t.Fatal("the live manager did not handle the external finish")
	}

	rec = postAnswer(t, f, "I optimized rendering.")
	if rec.Code != http.StatusGone {
		t.Fatalf("status = %d, want 410 (body: %s)", rec.Code, rec.Body.String())
	}
	if code, _, _ := decodeError(t, rec); code != "session_completed" {
		t.Errorf("code = %q, want session_completed", code)
	}

	// The external finish scores asynchronously; wait for it so the test does
	// not leave a goroutine writing to the store after it returns.
	deadline := time.Now().Add(2 * time.Second)
	for {
		app, err := f.store.GetApplication(context.Background(), f.session.ApplicationID)
		if err == nil && app.Status == domain.ApplicationStatusReview {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("application status = %q, want review after external scoring", app.Status)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestConcurrentAnswersAcceptExactlyOne proves the awaiting claim is atomic:
// only the first POST after message.completed is accepted.
func TestConcurrentAnswersAcceptExactlyOne(t *testing.T) {
	// Pause the turn the accepted answer starts before it publishes a new
	// message.completed; otherwise the second POST may legitimately see a
	// fresh question and be accepted too.
	var gate *gatedEventStore
	f := newChatFixtureWrapped(t, false, func(base store.Store) store.Store {
		gate = newGatedEventStore(base, graph.EventNodeStarted, "interviewer", 2)
		return gate
	})
	rec := httptest.NewRecorder()
	f.handler.Start(rec, chatRequest(http.MethodPost, f.raw, ""))
	if rec.Code != http.StatusOK {
		t.Fatalf("start status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	waitManager(t, f.manager, f.session.ID)

	codes := make(chan int, 2)
	var wg sync.WaitGroup
	for _, content := range []string{"I optimized rendering.", "I split the state into a store."} {
		wg.Add(1)
		go func(content string) {
			defer wg.Done()
			payload, err := json.Marshal(map[string]string{"content": content})
			if err != nil {
				t.Errorf("marshal answer: %v", err)
				return
			}
			rec := httptest.NewRecorder()
			f.handler.SendMessage(rec, chatRequest(http.MethodPost, f.raw, string(payload)))
			codes <- rec.Code
		}(content)
	}
	wg.Wait()
	close(gate.release)
	close(codes)

	counts := map[int]int{}
	for code := range codes {
		counts[code]++
	}
	if counts[http.StatusAccepted] != 1 || counts[http.StatusConflict] != 1 {
		t.Fatalf("status counts = %v, want exactly one 202 and one 409", counts)
	}
	waitManager(t, f.manager, f.session.ID)
}
