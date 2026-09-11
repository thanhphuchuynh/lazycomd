package api

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	defaultTail = 200
	maxTail     = 10000
	pingEvery   = 30 * time.Second
)

func (s *Server) logs(w http.ResponseWriter, r *http.Request) {
	tail := defaultTail
	if q := r.URL.Query().Get("tail"); q != "" {
		n, err := strconv.Atoi(q)
		if err != nil || n < 0 || n > maxTail {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": fmt.Sprintf("tail must be an integer between 0 and %d", maxTail),
			})
			return
		}
		tail = n
	}
	b, err := s.mgr.Logs(r.PathValue("name"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, b.Tail(tail))
}

// stream sends live output as SSE. One data: line per output line, plus a
// comment ping so an idle connection stays open through proxies.
func (s *Server) stream(w http.ResponseWriter, r *http.Request) {
	b, err := s.mgr.Logs(r.PathValue("name"))
	if err != nil {
		s.fail(w, err)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "streaming unsupported"})
		return
	}

	ch, cancel := b.Subscribe()
	defer cancel()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	ping := time.NewTicker(pingEvery)
	defer ping.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ping.C:
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		case chunk, open := <-ch:
			if !open {
				return
			}
			for _, line := range strings.Split(strings.TrimSuffix(string(chunk), "\n"), "\n") {
				fmt.Fprintf(w, "data: %s\n\n", line)
			}
			flusher.Flush()
		}
	}
}
