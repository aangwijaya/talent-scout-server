package handlers

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/aangwijaya/talent-scout-server/internal/auth"
	"github.com/aangwijaya/talent-scout-server/internal/config"
	"github.com/aangwijaya/talent-scout-server/internal/http/apierror"
	"github.com/aangwijaya/talent-scout-server/internal/store"
)

const maxLoginBody = 4 << 10

type Auth struct {
	cfg    config.Config
	tokens *auth.Manager
	store  store.Store
}

func NewAuth(cfg config.Config, tokens *auth.Manager, st store.Store) *Auth {
	if tokens == nil {
		tokens = auth.New(cfg.AuthTokenSecret)
	}
	return &Auth{cfg: cfg, tokens: tokens, store: st}
}

type loginRequest struct {
	Password string `json:"password"`
}

type loginResponse struct {
	AccessToken string    `json:"access_token"`
	ExpiresAt   time.Time `json:"expires_at"`
}

func (h *Auth) Login(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxLoginBody)
	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeUnauthorized(w)
		return
	}

	if h.cfg.RecruiterPassword == "" ||
		subtle.ConstantTimeCompare([]byte(req.Password), []byte(h.cfg.RecruiterPassword)) != 1 {
		writeUnauthorized(w)
		return
	}

	token, expiresAt, err := h.tokens.IssueAccessToken()
	if err != nil {
		apierror.Write(w, http.StatusInternalServerError, apierror.CodeInternalError, "could not issue access token", nil)
		return
	}
	apierror.JSON(w, http.StatusOK, loginResponse{AccessToken: token, ExpiresAt: expiresAt})
}

type sseTokenRequest struct {
	RunID string `json:"run_id"`
}

type sseTokenResponse struct {
	SSEToken  string    `json:"sse_token"`
	ExpiresAt time.Time `json:"expires_at"`
}

func (h *Auth) SSEToken(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxLoginBody)
	var req sseTokenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		apierror.Write(w, http.StatusBadRequest, apierror.CodeValidationFailed, "invalid JSON body", nil)
		return
	}
	if req.RunID == "" {
		apierror.Write(w, http.StatusBadRequest, apierror.CodeValidationFailed, "run_id is required", map[string]string{"field": "run_id"})
		return
	}
	if !uuidPattern.MatchString(req.RunID) {
		apierror.Write(w, http.StatusNotFound, apierror.CodeNotFound, "graph run not found", nil)
		return
	}
	if h.store == nil {
		apierror.Write(w, http.StatusServiceUnavailable, apierror.CodeDBUnavailable, "database is unavailable", nil)
		return
	}
	if _, err := h.store.GetGraphRun(r.Context(), req.RunID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			apierror.Write(w, http.StatusNotFound, apierror.CodeNotFound, "graph run not found", nil)
			return
		}
		apierror.Write(w, http.StatusInternalServerError, apierror.CodeInternalError, "could not load graph run", nil)
		return
	}

	token, expiresAt, err := h.tokens.IssueSSEToken(req.RunID)
	if err != nil {
		apierror.Write(w, http.StatusInternalServerError, apierror.CodeInternalError, "could not issue sse token", nil)
		return
	}
	apierror.JSON(w, http.StatusOK, sseTokenResponse{SSEToken: token, ExpiresAt: expiresAt})
}

func writeUnauthorized(w http.ResponseWriter) {
	apierror.Write(w, http.StatusUnauthorized, apierror.CodeUnauthorized, "invalid credentials", nil)
}
