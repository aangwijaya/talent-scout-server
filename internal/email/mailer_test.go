package email

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestLogMailerPrintsLink(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	m := NewLog(logger)

	if err := m.SendInvite(context.Background(), "budi@example.com", "Budi", "https://example.com/s/tok123"); err != nil {
		t.Fatalf("SendInvite: %v", err)
	}
	if !strings.Contains(buf.String(), "https://example.com/s/tok123") {
		t.Errorf("log output missing link: %s", buf.String())
	}
}

func TestGmailSendInviteSuccess(t *testing.T) {
	var tokenReqSeen string
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		tokenReqSeen = string(body)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"access_token":"access-123","token_type":"Bearer","expires_in":3600}`)
	}))
	defer tokenSrv.Close()

	var sendAuth, sendRaw string
	sendSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sendAuth = r.Header.Get("Authorization")
		var req struct {
			Raw string `json:"raw"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		sendRaw = req.Raw
		io.WriteString(w, `{"id":"msg1"}`)
	}))
	defer sendSrv.Close()

	g := NewGmail("cid", "secret", "refresh-token", "Talent Scout <you@gmail.com>", slog.New(slog.NewTextHandler(io.Discard, nil)))
	g.tokenURL = tokenSrv.URL
	g.sendURL = sendSrv.URL

	err := g.SendInvite(context.Background(), "budi@example.com", "Budi", "https://example.com/s/tok123")
	if err != nil {
		t.Fatalf("SendInvite: %v", err)
	}

	values, err := url.ParseQuery(tokenReqSeen)
	if err != nil {
		t.Fatalf("token request not form-encoded: %v", err)
	}
	if values.Get("grant_type") != "refresh_token" {
		t.Errorf("grant_type = %q", values.Get("grant_type"))
	}
	if values.Get("client_id") != "cid" || values.Get("client_secret") != "secret" || values.Get("refresh_token") != "refresh-token" {
		t.Errorf("oauth creds mismatch: %v", values)
	}
	if sendAuth != "Bearer access-123" {
		t.Errorf("send Authorization = %q", sendAuth)
	}

	raw, err := base64.RawURLEncoding.DecodeString(sendRaw)
	if err != nil {
		t.Fatalf("send raw is not base64url: %v", err)
	}
	message := string(raw)
	if !strings.Contains(message, "budi@example.com") {
		t.Errorf("message missing recipient: %s", message)
	}
	if !strings.Contains(message, "https://example.com/s/tok123") {
		t.Errorf("message missing link: %s", message)
	}
}

func TestGmailSendInviteFailure(t *testing.T) {
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"access_token":"access-123"}`)
	}))
	defer tokenSrv.Close()

	sendSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		io.WriteString(w, `{"error":{"code":500}}`)
	}))
	defer sendSrv.Close()

	g := NewGmail("cid", "secret", "refresh-token", "you@gmail.com", slog.New(slog.NewTextHandler(io.Discard, nil)))
	g.tokenURL = tokenSrv.URL
	g.sendURL = sendSrv.URL

	err := g.SendInvite(context.Background(), "budi@example.com", "Budi", "https://example.com/s/tok123")
	if err == nil {
		t.Fatal("SendInvite returned nil, want error on 500")
	}
}

func TestGmailSendInviteTokenFailure(t *testing.T) {
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, `{"error":"invalid_grant"}`)
	}))
	defer tokenSrv.Close()

	g := NewGmail("cid", "secret", "bad-refresh", "you@gmail.com", slog.New(slog.NewTextHandler(io.Discard, nil)))
	g.tokenURL = tokenSrv.URL
	g.sendURL = tokenSrv.URL

	if err := g.SendInvite(context.Background(), "budi@example.com", "Budi", "https://example.com/s/tok123"); err == nil {
		t.Fatal("SendInvite returned nil, want error on token exchange failure")
	}
}

func TestGmailSendFailureLogsNothingSensitive(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"access_token":"access-123"}`)
	}))
	defer tokenSrv.Close()

	sendSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		io.WriteString(w, `{"error":{"code":500}}`)
	}))
	defer sendSrv.Close()

	g := NewGmail("client-id", "client-secret", "refresh-secret", "you@gmail.com", logger)
	g.tokenURL = tokenSrv.URL
	g.sendURL = sendSrv.URL

	const link = "https://example.com/s/super-secret-token"
	if err := g.SendInvite(context.Background(), "budi@example.com", "Budi", link); err == nil {
		t.Fatal("SendInvite returned nil, want error")
	}

	out := buf.String()
	for _, secret := range []string{
		link,
		"super-secret-token",
		"client-id",
		"client-secret",
		"refresh-secret",
		"Hi Budi",
	} {
		if strings.Contains(out, secret) {
			t.Errorf("logger output leaked %q: %s", secret, out)
		}
	}
}
