package api

import (
	"net/http"

	"github.com/thanhphuchuynh/lazycomd/internal/manager"
	"github.com/thanhphuchuynh/lazycomd/internal/probe"
)

// system serves the last sample of the machine's state. Always 200: partial
// failure is normal and is reported inside the body, not by a status code.
func (s *Server) system(w http.ResponseWriter, _ *http.Request) {
	if s.probe == nil {
		writeJSON(w, http.StatusOK, probe.EmptySnapshot())
		return
	}
	writeJSON(w, http.StatusOK, s.probe.Snapshot())
}

// enrich fills the probe-sourced fields the manager never sets.
func (s *Server) enrich(list []manager.Status) []manager.Status {
	if s.probe == nil || len(list) == 0 {
		return list
	}
	snap := s.probe.Snapshot()
	for i := range list {
		if v, ok := snap.Vitals[list[i].Name]; ok {
			list[i].CPU, list[i].MemMB = v.CPU, v.MemMB
		}
		if h, ok := snap.Health[list[i].Name]; ok {
			list[i].Health = &manager.HealthView{
				URL:       h.URL,
				OK:        h.OK,
				Status:    h.Status,
				LatencyMS: h.LatencyMS,
				Error:     h.Error,
			}
		}
	}
	return list
}
