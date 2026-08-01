package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/heros-foreal/agentd/internal/telemetry"
)

// The P2.5 live run-monitoring surface (§8; PRD §6 FR19). It streams a run's per-node latency, cost,
// and token metrics as they arrive, reading run status from the run record (never derived) and node
// state (ok / failed / timed_out) from each node's reliability signal.
//
// Three routes, one read model:
//   GET /p25/monitor?run_id=…                    the view (self-contained HTML, embedded)
//   GET /api/v1/runs/{run_id}/monitor           one JSON snapshot (polling fallback, 404 for no run)
//   GET /api/v1/runs/{run_id}/monitor/stream    Server-Sent Events: a snapshot every ~500 ms to close

// MonitorSource is the read model the routes serve — telemetry.Monitor implements it. An interface so
// the API does not depend on a concrete monitor and a test can stub it.
type MonitorSource interface {
	Snapshot(runID string) (telemetry.RunMonitor, bool)
}

// MountMonitor registers the live-monitor routes. Call after New.
func (s *Server) MountMonitor(src MonitorSource) {
	s.monitor = src
	s.Mux.HandleFunc("GET /api/v1/runs/{run_id}/monitor", s.handleMonitorSnapshot)
	s.Mux.HandleFunc("GET /api/v1/runs/{run_id}/monitor/stream", s.handleMonitorStream)
}

func (s *Server) handleMonitorSnapshot(w http.ResponseWriter, r *http.Request) {
	if s.monitor == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "the live monitor is not mounted"})
		return
	}
	snap, ok := s.monitor.Snapshot(r.PathValue("run_id"))
	if !ok {
		// 404, distinct from an existing run with no nodes: the view's empty state depends on telling
		// "no such run" apart from "run exists, nothing yet".
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "no such run"})
		return
	}
	writeJSON(w, http.StatusOK, snap)
}

// handleMonitorStream streams snapshots over SSE until the run is terminal (or the client disconnects,
// or a safety cap elapses). This is the "as they arrive" half of task 8.1.
func (s *Server) handleMonitorStream(w http.ResponseWriter, r *http.Request) {
	if s.monitor == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "the live monitor is not mounted"})
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		// No streaming support behind this writer — the client falls back to polling.
		writeJSON(w, http.StatusNotImplemented, map[string]any{"error": "streaming unsupported"})
		return
	}
	runID := r.PathValue("run_id")
	if _, exists := s.monitor.Snapshot(runID); !exists {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "no such run"})
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Connection", "keep-alive")

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	// A safety cap so a run that never terminates does not hold the connection forever.
	deadline := time.After(15 * time.Minute)

	send := func() (terminal bool) {
		snap, ok := s.monitor.Snapshot(runID)
		if !ok {
			return true // the run vanished; close the stream
		}
		b, err := json.Marshal(snap)
		if err != nil {
			return true
		}
		_, _ = w.Write([]byte("data: "))
		_, _ = w.Write(b)
		_, _ = w.Write([]byte("\n\n"))
		flusher.Flush()
		return snap.Terminal
	}

	// Send an immediate first snapshot, then on each tick.
	if send() {
		return
	}
	for {
		select {
		case <-r.Context().Done():
			return
		case <-deadline:
			return
		case <-ticker.C:
			if send() {
				return
			}
		}
	}
}
