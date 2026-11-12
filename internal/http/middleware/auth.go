package middleware

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/aangwijaya/talent-scout-server/internal/auth"
	"github.com/aangwijaya/talent-scout-server/internal/http/apierror"
)

// Bearer accepts the recruiter access token only from the Authorization
// header. Query-string credentials (such as ?access_token=) are never read.
func Bearer(tokens *auth.Manager) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if tokens == nil {
				writeUnauthorized(w)
				return
			}
			scheme, token, ok := strings.Cut(r.Header.Get("Authorization"), " ")
			token = strings.TrimSpace(token)
			if !ok || !strings.EqualFold(scheme, "Bearer") || token == "" {
				writeUnauthorized(w)
				return
			}
			if _, err := tokens.VerifyAccessToken(token); err != nil {
				writeUnauthorized(w)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// SSEToken accepts the short-lived stream token only from ?sse_token=, bound
// to the runId path parameter. The recruiter access token is not accepted.
func SSEToken(tokens *auth.Manager) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if tokens == nil {
				writeUnauthorized(w)
				return
			}
			token := r.URL.Query().Get("sse_token")
			runID := chi.URLParam(r, "runId")
			if token == "" || runID == "" {
				writeUnauthorized(w)
				return
			}
			if _, err := tokens.VerifySSEToken(token, runID); err != nil {
				writeUnauthorized(w)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func writeUnauthorized(w http.ResponseWriter) {
	apierror.Write(w, http.StatusUnauthorized, apierror.CodeUnauthorized, "invalid or missing access token", nil)
}
