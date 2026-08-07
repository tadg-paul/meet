// ABOUTME: HTTP surface for the per-room timer (issue #15): current-state JSON,
// ABOUTME: an SSE event stream for participants, and a moderator control POST.

package server

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// timerRoute extracts the room from a "<room>/timer..." path. Same single-
// segment rule as moderatorRoute, whose generic suffix matching it reuses.
func timerRoute(path, suffix string) (string, bool) {
	return moderatorRoute(path, suffix)
}

type timerControlRequest struct {
	Action string `json:"action"`
	Config *struct {
		Total        int `json:"total"`
		WarnPercent  int `json:"warnPercent"`
		GracePercent int `json:"gracePercent"`
	} `json:"config"`
}

// handleTimer serves the current timer state (GET) and applies control actions
// (POST). Control requires a valid moderator token scoped to the room.
func (s *Server) handleTimer(w http.ResponseWriter, r *http.Request, room string) {
	switch r.Method {
	case http.MethodGet:
		s.writeJSON(w, http.StatusOK, s.timers.StateFor(room))
	case http.MethodPost:
		if !s.hasValidModeratorJWT(room, r) {
			http.Error(w, "moderator authorization required", http.StatusUnauthorized)
			return
		}
		var req timerControlRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		var cfg *TimerConfig
		if req.Config != nil {
			cfg = &TimerConfig{
				Total:        req.Config.Total,
				WarnPercent:  req.Config.WarnPercent,
				GracePercent: req.Config.GracePercent,
			}
		}
		view, err := s.timers.Apply(room, req.Action, cfg)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		s.writeJSON(w, http.StatusOK, view)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleTimerEvents streams timer state to a participant over SSE. The current
// state is sent on connect; each state change is sent as it happens.
func (s *Server) handleTimerEvents(w http.ResponseWriter, r *http.Request, room string) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	ch := s.timers.Subscribe(room)
	defer s.timers.Unsubscribe(room, ch)

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case view := <-ch:
			data, err := json.Marshal(view)
			if err != nil {
				s.logger.Error("timer sse marshal failed", "error", err)
				continue
			}
			if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
				return // client gone
			}
			flusher.Flush()
		}
	}
}

func (s *Server) writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		s.logger.Error("timer json encode failed", "error", err)
	}
}
