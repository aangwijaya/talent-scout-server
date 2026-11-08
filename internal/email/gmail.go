package email

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"mime"
	"net/http"
	"net/mail"
	"net/url"
	"strings"
	"time"
)

const (
	gmailTokenURL = "https://oauth2.googleapis.com/token"
	gmailSendURL  = "https://gmail.googleapis.com/gmail/v1/users/me/messages/send"
)

// Gmail sends invite links through the Gmail API (users.messages.send) using an
// OAuth2 refresh token. It never logs the tokens, the message body, or the link.
type Gmail struct {
	clientID     string
	clientSecret string
	refreshToken string
	from         string
	logger       *slog.Logger
	client       *http.Client
	tokenURL     string
	sendURL      string
}

func NewGmail(clientID, clientSecret, refreshToken, from string, logger *slog.Logger) *Gmail {
	return &Gmail{
		clientID:     clientID,
		clientSecret: clientSecret,
		refreshToken: refreshToken,
		from:         from,
		logger:       logger,
		client:       &http.Client{Timeout: 15 * time.Second},
		tokenURL:     gmailTokenURL,
		sendURL:      gmailSendURL,
	}
}

func (g *Gmail) SendInvite(ctx context.Context, to, name, link string) error {
	accessToken, err := g.accessToken(ctx)
	if err != nil {
		g.logger.Error("gmail token exchange failed", "error", err)
		return err
	}

	raw, err := g.buildMessage(to, name, link)
	if err != nil {
		return err
	}

	payload, err := json.Marshal(struct {
		Raw string `json:"raw"`
	}{Raw: base64.RawURLEncoding.EncodeToString(raw)})
	if err != nil {
		return fmt.Errorf("encode gmail message: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.sendURL, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("build gmail send request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := g.client.Do(req)
	if err != nil {
		g.logger.Error("gmail send failed", "error", err)
		return fmt.Errorf("gmail send: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		g.logger.Error("gmail send failed", "status", resp.StatusCode)
		return fmt.Errorf("gmail send: status %d", resp.StatusCode)
	}
	return nil
}

func (g *Gmail) accessToken(ctx context.Context) (string, error) {
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("client_id", g.clientID)
	form.Set("client_secret", g.clientSecret)
	form.Set("refresh_token", g.refreshToken)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("build gmail token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := g.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("gmail token exchange: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("gmail token exchange: status %d", resp.StatusCode)
	}

	var tok struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tok); err != nil {
		return "", fmt.Errorf("decode gmail token response: %w", err)
	}
	if tok.AccessToken == "" {
		return "", fmt.Errorf("gmail token response missing access_token")
	}
	return tok.AccessToken, nil
}

func (g *Gmail) buildMessage(to, name, link string) ([]byte, error) {
	from, err := mail.ParseAddress(g.from)
	if err != nil {
		return nil, fmt.Errorf("parse EMAIL_FROM: %w", err)
	}
	recipient, err := mail.ParseAddress(to)
	if err != nil {
		return nil, fmt.Errorf("parse recipient: %w", err)
	}

	subject := mime.QEncoding.Encode("utf-8", "Your Talent Scout screening link")
	body := fmt.Sprintf("Hi %s,\r\n\r\nYour screening link is ready:\r\n\r\n%s\r\n\r\nThis link expires in 7 days.\r\n", name, link)

	var b bytes.Buffer
	b.WriteString("From: " + from.String() + "\r\n")
	b.WriteString("To: " + recipient.String() + "\r\n")
	b.WriteString("Subject: " + subject + "\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=\"UTF-8\"\r\n")
	b.WriteString("\r\n")
	b.WriteString(body)
	return b.Bytes(), nil
}
