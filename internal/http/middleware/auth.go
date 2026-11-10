package middleware

import (
	"net/http"
	"strings"

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

func writeUnauthorized(w http.ResponseWriter) {
	apierror.Write(w, http.StatusUnauthorized, apierror.CodeUnauthorized, "invalid or missing access token", nil)
}
