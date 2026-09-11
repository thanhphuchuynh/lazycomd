package api

import (
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"sort"
	"strings"

	"github.com/thanhphuchuynh/lazycomd/internal/config"
	"github.com/thanhphuchuynh/lazycomd/internal/configw"
	"github.com/thanhphuchuynh/lazycomd/internal/manager"
)

// errBadRequest marks an error the client caused, for the 400 mapping.
var errBadRequest = errors.New("bad request")

// commandBody is a command plus the name it goes under.
type commandBody struct {
	Name string `json:"name"`
	config.Command
}

// commandConfig returns one command's full spec, including fields the TUI's
// form never shows — a form that knows four fields must not erase the rest.
func (s *Server) commandConfig(w http.ResponseWriter, r *http.Request) {
	cfg, err := s.reload()
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errBody(err))
		return
	}
	name := r.PathValue("name")
	cmd, ok := cfg.Commands[name]
	if !ok {
		writeJSON(w, http.StatusNotFound, errBody(fmt.Errorf("unknown command: %s", name)))
		return
	}
	writeJSON(w, http.StatusOK, cmd)
}

// projects lists the registered projects, so a client can resolve a
// namespaced name to a directory without reading the config file itself.
func (s *Server) projects(w http.ResponseWriter, _ *http.Request) {
	cfg, err := s.reload()
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errBody(err))
		return
	}
	out := cfg.Projects
	if out == nil {
		out = map[string]string{}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) createCommand(w http.ResponseWriter, r *http.Request) {
	if !s.canWrite(w) {
		return
	}
	var body commandBody
	if err := decodeOptional(r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, errBody(err))
		return
	}
	if body.Name == "" {
		writeJSON(w, http.StatusBadRequest, errBody(errors.New("name is required")))
		return
	}

	path, key, err := s.targetFile(body.Name)
	if err != nil {
		s.failWrite(w, err)
		return
	}
	if err := s.writers.File(path).Create(key, body.Command); err != nil {
		s.failWrite(w, err)
		return
	}
	s.afterWrite(w, body.Name, http.StatusCreated)
}

func (s *Server) updateCommand(w http.ResponseWriter, r *http.Request) {
	if !s.canWrite(w) {
		return
	}
	var body config.Command
	if err := decodeOptional(r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, errBody(err))
		return
	}
	name := r.PathValue("name")

	path, key, err := s.targetFile(name)
	if err != nil {
		s.failWrite(w, err)
		return
	}
	if err := s.writers.File(path).Update(key, body); err != nil {
		s.failWrite(w, err)
		return
	}
	s.afterWrite(w, name, http.StatusOK)
}

func (s *Server) deleteCommand(w http.ResponseWriter, r *http.Request) {
	if !s.canWrite(w) {
		return
	}
	name := r.PathValue("name")

	path, key, err := s.targetFile(name)
	if err != nil {
		s.failWrite(w, err)
		return
	}
	if err := s.writers.File(path).Delete(key); err != nil {
		s.failWrite(w, err)
		return
	}
	if _, err := s.applyReload(); err != nil {
		writeJSON(w, http.StatusInternalServerError, errBody(err))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// canWrite reports whether this daemon was given a writer registry, answering
// 501 when it was not.
func (s *Server) canWrite(w http.ResponseWriter) bool {
	if s.writers == nil {
		writeJSON(w, http.StatusNotImplemented, errBody(errors.New("this daemon was built without config writing")))
		return false
	}
	return true
}

// afterWrite reloads the daemon and answers with the command's new status.
func (s *Server) afterWrite(w http.ResponseWriter, name string, code int) {
	if _, err := s.applyReload(); err != nil {
		writeJSON(w, http.StatusInternalServerError, errBody(err))
		return
	}
	st, err := s.mgr.Status(name)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, code, s.enrich([]manager.Status{st})[0])
}

// targetFile resolves which file a write to name belongs in, and the key to
// use inside it: a bare name goes to the global config, and app:api goes to
// that project's lazycomd.yaml under the bare key api.
func (s *Server) targetFile(name string) (string, string, error) {
	ns, key, namespaced := strings.Cut(name, ":")
	if !namespaced {
		if s.cfgPath == "" {
			return "", "", fmt.Errorf("%w: this daemon has no config path", errBadRequest)
		}
		return s.cfgPath, name, nil
	}

	cfg, err := s.reload()
	if err != nil {
		return "", "", err
	}
	dir, ok := cfg.Projects[ns]
	if !ok {
		known := make([]string, 0, len(cfg.Projects))
		for p := range cfg.Projects {
			known = append(known, p)
		}
		sort.Strings(known)
		return "", "", fmt.Errorf("%w: unknown project %q (known: %s)", errBadRequest, ns, strings.Join(known, ", "))
	}
	return filepath.Join(dir, "lazycomd.yaml"), key, nil
}

// failWrite maps a writer error to its status code.
func (s *Server) failWrite(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, configw.ErrExists), errors.Is(err, configw.ErrChanged):
		writeJSON(w, http.StatusConflict, errBody(err))
	case errors.Is(err, configw.ErrNotFound):
		writeJSON(w, http.StatusNotFound, errBody(err))
	case errors.Is(err, configw.ErrInvalid), errors.Is(err, errBadRequest):
		writeJSON(w, http.StatusBadRequest, errBody(err))
	default:
		writeJSON(w, http.StatusInternalServerError, errBody(err))
	}
}
