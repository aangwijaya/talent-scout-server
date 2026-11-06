package apierror

import (
	"encoding/json"
	"log/slog"
	"net/http"
)

const (
	CodeValidationFailed      = "validation_failed"
	CodeInvalidFile           = "invalid_file"
	CodeUnauthorized          = "unauthorized"
	CodeNotFound              = "not_found"
	CodeSessionNotFound       = "session_not_found"
	CodeAlreadyApplied        = "already_applied"
	CodeSessionAlreadyStarted = "session_already_started"
	CodeInvalidState          = "invalid_state"
	CodeSessionExpired        = "session_expired"
	CodeSessionCompleted      = "session_completed"
	CodeFileTooLarge          = "file_too_large"
	CodeRateLimited           = "rate_limited"
	CodeInternalError         = "internal_error"
	CodeLLMUnavailable        = "llm_unavailable"
	CodeDBUnavailable         = "db_unavailable"
)

type body struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Details any    `json:"details"`
}

type envelope struct {
	Error body `json:"error"`
}

func Write(w http.ResponseWriter, status int, code, message string, details any) {
	JSON(w, status, envelope{Error: body{Code: code, Message: message, Details: details}})
}

func JSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("write json response", "error", err)
	}
}
