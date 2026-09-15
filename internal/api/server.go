// Package api serves the lazycomd HTTP API. It never spawns a process; every
// lifecycle action goes through the manager.
package api

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/thanhphuchuynh/lazycomd/internal/config"
	"github.com/thanhphuchuynh/lazycomd/internal/configw"
	"github.com/thanhphuchuynh/lazycomd/internal/manager"
	"github.com/thanhphuchuynh/lazycomd/internal/probe"
)

// maxBody caps a request body; every body this API accepts is tiny.
const maxBody = 1 << 16

// Server holds the manager plus the config reloader the /v1/reload endpoint
// calls.
type Server struct {
	mgr     *manager.Manager
	token   string
	reload  func() (*config.Config, error)
	probe   *probe.Sampler
	cfgPath string
	writers *configw.Registry
}

// Handler is the unauthenticated handler for the unix socket, where file
// permissions are the authentication.
func (s *Server) Handler() http.Handler { return recoverMW(s.routes()) }

func (s *Server) routes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/healthz", s.healthz)
	mux.HandleFunc("GET /v1/commands", s.list)
	mux.HandleFunc("GET /v1/commands/{name}", s.get)
	mux.HandleFunc("POST /v1/commands/{name}/start", s.start)
	mux.HandleFunc("POST /v1/commands/{name}/stop", s.stop)
	mux.HandleFunc("POST /v1/commands/{name}/restart", s.restart)
	mux.HandleFunc("GET /v1/commands/{name}/logs", s.logs)
	mux.HandleFunc("GET /v1/commands/{name}/logs/stream", s.stream)
	mux.HandleFunc("POST /v1/reload", s.doReload)
	mux.HandleFunc("GET /v1/system", s.system)
	mux.HandleFunc("GET /v1/projects", s.projects)
	mux.HandleFunc("GET /v1/commands/{name}/config", s.commandConfig)
	mux.HandleFunc("POST /v1/commands", s.createCommand)
	mux.HandleFunc("PUT /v1/commands/{name}", s.updateCommand)
	mux.HandleFunc("DELETE /v1/commands/{name}", s.deleteCommand)
	return mux
}

func (s *Server) healthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) list(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.enrich(s.mgr.List()))
}

func (s *Server) get(w http.ResponseWriter, r *http.Request) {
	st, err := s.mgr.Status(r.PathValue("name"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.enrich([]manager.Status{st})[0])
}

type startBody struct {
	WithDeps bool `json:"with_deps"`
	// WaitSec blocks the response until the command is ready — its health URL
	// answers, or its port accepts — so a caller that has something to run
	// next does not have to poll for it. Zero returns as soon as it spawns.
	WaitSec float64 `json:"wait_sec,omitempty"`
}

func (s *Server) start(w http.ResponseWriter, r *http.Request) {
	var body startBody
	if err := decodeOptional(r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, errBody(err))
		return
	}
	name := r.PathValue("name")

	var err error
	if body.WithDeps {
		err = s.mgr.StartWithDeps(name)
	} else {
		err = s.mgr.Start(name)
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	if body.WaitSec > 0 {
		st, err := s.waitReady(r.Context(), name, time.Duration(body.WaitSec*float64(time.Second)))
		if err != nil {
			// The command started; it just is not ready. Say both, so a
			// caller can read the state it reached.
			writeJSON(w, http.StatusGatewayTimeout, map[string]any{"error": err.Error(), "status": st})
			return
		}
		writeJSON(w, http.StatusOK, s.enrich([]manager.Status{st})[0])
		return
	}
	s.get(w, r)
}

func (s *Server) stop(w http.ResponseWriter, r *http.Request) {
	if err := s.mgr.Stop(r.PathValue("name")); err != nil {
		s.fail(w, err)
		return
	}
	s.get(w, r)
}

func (s *Server) restart(w http.ResponseWriter, r *http.Request) {
	if err := s.mgr.Restart(r.PathValue("name")); err != nil {
		s.fail(w, err)
		return
	}
	s.get(w, r)
}

// fail maps a manager error to its HTTP status.
func (s *Server) fail(w http.ResponseWriter, err error) {
	code := http.StatusInternalServerError
	switch {
	case errors.Is(err, manager.ErrNotFound):
		code = http.StatusNotFound
	case errors.Is(err, manager.ErrWrongState):
		code = http.StatusConflict
	}
	writeJSON(w, code, errBody(err))
}

// decodeOptional decodes a JSON body that may be absent or empty.
func decodeOptional(r *http.Request, v any) error {
	if r.Body == nil {
		return nil
	}
	err := json.NewDecoder(io.LimitReader(r.Body, maxBody)).Decode(v)
	if err == nil || errors.Is(err, io.EOF) {
		return nil
	}
	return err
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func errBody(err error) map[string]string {
	return map[string]string{"error": err.Error()}
}

// recoverMW keeps a client-triggered panic from taking down a running stack.
func recoverMW(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				log.Printf("lazycomd: panic in %s %s: %v\n%s", r.Method, r.URL.Path, v, debug.Stack())
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
			}
		}()
		next.ServeHTTP(w, r)
	})
}
