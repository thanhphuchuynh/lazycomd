package api

import (
	"crypto/subtle"
	"net/http"
	"strings"
)

// AuthHandler is the token-protected handler for the optional TCP listener.
// An empty configured token rejects every request: the daemon refuses to
// start with listen: and no token_file, so reaching here with one is a bug.
func (s *Server) AuthHandler() http.Handler {
	return recoverMW(tokenMW(s.token, s.routes()))
}

func tokenMW(token string, next http.Handler) http.Handler {
	want := []byte(token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := []byte(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		if len(want) == 0 || subtle.ConstantTimeCompare(got, want) != 1 {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid token"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

// doReload re-reads the config from disk and applies the diff. A broken
// config is a 400 and leaves the running config untouched.
func (s *Server) doReload(w http.ResponseWriter, _ *http.Request) {
	cfg, err := s.reload()
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errBody(err))
		return
	}
	if err := s.mgr.Reload(cfg); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.mgr.List())
}
