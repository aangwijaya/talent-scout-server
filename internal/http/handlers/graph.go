package handlers

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/aangwijaya/talent-scout-server/internal/domain"
	"github.com/aangwijaya/talent-scout-server/internal/graph"
	"github.com/aangwijaya/talent-scout-server/internal/http/apierror"
	"github.com/aangwijaya/talent-scout-server/internal/sse"
	"github.com/aangwijaya/talent-scout-server/internal/store"
)

type GraphRuns struct {
	store             store.Store
	bus               *graph.Bus
	writeDeadline     time.Duration
	heartbeatInterval time.Duration
}

func NewGraphRuns(st store.Store) *GraphRuns {
	return &GraphRuns{
		store:             st,
		writeDeadline:     sse.DefaultWriteDeadline,
		heartbeatInterval: sse.HeartbeatInterval,
	}
}

// SetBus attaches the live event bus used by the run stream.
func (h *GraphRuns) SetBus(bus *graph.Bus) {
	h.bus = bus
}

// Get returns the run snapshot: the static graph shape plus every node's last
// status/attempt/model derived from the persisted node_events.
func (h *GraphRuns) Get(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		apierror.Write(w, http.StatusServiceUnavailable, apierror.CodeDBUnavailable, "database is unavailable", nil)
		return
	}
	runID := chi.URLParam(r, "runId")
	if !uuidPattern.MatchString(runID) {
		apierror.Write(w, http.StatusNotFound, apierror.CodeNotFound, "graph run not found", nil)
		return
	}
	run, err := h.store.GetGraphRun(r.Context(), runID)
	if err != nil {
		h.writeRunLookupError(w, err)
		return
	}
	events, err := h.store.ListNodeEvents(r.Context(), run.ID, 0)
	if err != nil {
		apierror.Write(w, http.StatusInternalServerError, apierror.CodeInternalError, "could not load graph run events", nil)
		return
	}
	apierror.JSON(w, http.StatusOK, graph.Snapshot(run, events))
}

// Events is the recruiter run stream: replay node_events after Last-Event-ID or
// since_seq, then stream live events and heartbeats until the run ends. The
// subscription is cancelled on any failed write so a stalled browser cannot
// hold a publisher; it reconnects with since_seq and replays the gap.
func (h *GraphRuns) Events(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		apierror.Write(w, http.StatusServiceUnavailable, apierror.CodeDBUnavailable, "database is unavailable", nil)
		return
	}
	runID := chi.URLParam(r, "runId")
	if !uuidPattern.MatchString(runID) {
		apierror.Write(w, http.StatusNotFound, apierror.CodeNotFound, "graph run not found", nil)
		return
	}
	run, err := h.store.GetGraphRun(r.Context(), runID)
	if err != nil {
		h.writeRunLookupError(w, err)
		return
	}
	if h.bus == nil {
		apierror.Write(w, http.StatusInternalServerError, apierror.CodeInternalError, "events are unavailable", nil)
		return
	}

	live, cancel := h.bus.Subscribe(runID)
	defer cancel()

	writer := sse.NewWriter(w, h.writeDeadline)
	if err := writer.Prepare(); err != nil {
		return
	}

	lastSeq := eventSince(r)
	events, err := h.store.ListNodeEvents(r.Context(), runID, lastSeq)
	if err != nil {
		slog.Default().Error("run stream replay failed", "run_id", runID, "error", err)
		return
	}
	for _, event := range events {
		if event.Seq <= lastSeq || !sse.IsRunEvent(event.Type) {
			continue
		}
		if err := writer.WriteEvent(event); err != nil {
			return
		}
		lastSeq = event.Seq
		if isRunTerminal(event.Type) {
			return
		}
	}

	if run.Status != domain.GraphRunStatusRunning {
		return
	}

	heartbeat := time.NewTicker(h.heartbeatInterval)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-heartbeat.C:
			if err := writer.Heartbeat(); err != nil {
				return
			}
		case event := <-live:
			if !sse.IsRunEvent(event.Type) {
				continue
			}
			if event.Seq > 0 && event.Seq <= lastSeq {
				continue
			}
			if err := writer.WriteEvent(event); err != nil {
				return
			}
			if event.Seq > lastSeq {
				lastSeq = event.Seq
			}
			if isRunTerminal(event.Type) {
				return
			}
		}
	}
}

func (h *GraphRuns) writeRunLookupError(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrNotFound) {
		apierror.Write(w, http.StatusNotFound, apierror.CodeNotFound, "graph run not found", nil)
		return
	}
	apierror.Write(w, http.StatusInternalServerError, apierror.CodeInternalError, "could not load graph run", nil)
}

func isRunTerminal(eventType string) bool {
	return eventType == graph.EventRunCompleted || eventType == graph.EventRunFailed
}
