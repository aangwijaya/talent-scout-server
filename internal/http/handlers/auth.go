package handlers

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"time"

	"github.com/aangwijaya/talent-scout-server/internal/auth"
	"github.com/aangwijaya/talent-scout-server/internal/config"
	"github.com/aangwijaya/talent-scout-server/internal/http/apierror"
)

const maxLoginBody = 4 << 10

type Auth struct {
	cfg    config.Config
	tokens *auth.Manager
}

func NewAuth(cfg config.Config, tokens *auth.Manager) *Auth {
	if tokens == nil {
		tokens = auth.New(cfg.AuthTokenSecret)
	}
	return &Auth{cfg: cfg, tokens: tokens}
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

func writeUnauthorized(w http.ResponseWriter) {
	apierror.Write(w, http.StatusUnauthorized, apierror.CodeUnauthorized, "invalid credentials", nil)
}
