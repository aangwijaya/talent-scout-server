package middleware

import (
	"net/http"
	"regexp"
)

var corsPath = regexp.MustCompile(`^/api/v1/(sessions|graph-runs)/[^/]+/events$`)

func CORS(origins []string) func(http.Handler) http.Handler {
	allowed := make(map[string]struct{}, len(origins))
	for _, origin := range origins {
		allowed[origin] = struct{}{}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !corsPath.MatchString(r.URL.Path) {
				next.ServeHTTP(w, r)
				return
			}

			w.Header().Add("Vary", "Origin")

			origin := r.Header.Get("Origin")
			methodAllowed := r.Method == http.MethodGet || r.Method == http.MethodOptions
			if _, ok := allowed[origin]; ok && methodAllowed {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "Last-Event-ID")
			}

			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
