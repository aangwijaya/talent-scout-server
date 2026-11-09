// Package sse writes Server-Sent Events for the session and graph-run streams.
package sse

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/aangwijaya/talent-scout-server/internal/domain"
	"github.com/aangwijaya/talent-scout-server/internal/graph"
)

// DefaultWriteDeadline bounds one event write so a connected but stalled client
// cannot freeze the runner: after the deadline the write fails, the handler
// cancels its subscription, and Emit unblocks.
const DefaultWriteDeadline = 10 * time.Second

// HeartbeatInterval is the `: ping` cadence from EVENTS.md §2.
const HeartbeatInterval = 15 * time.Second

// Writer serialises events onto an http.ResponseWriter.
type Writer struct {
	w             http.ResponseWriter
	rc            *http.ResponseController
	writeDeadline time.Duration
}

// NewWriter returns a writer that sets a write deadline before each write.
func NewWriter(w http.ResponseWriter, writeDeadline time.Duration) *Writer {
	if writeDeadline <= 0 {
		writeDeadline = DefaultWriteDeadline
	}
	return &Writer{w: w, rc: http.NewResponseController(w), writeDeadline: writeDeadline}
}

// Prepare sends the SSE response headers and flushes them so the client sees an
// open stream immediately.
func (w *Writer) Prepare() error {
	w.w.Header().Set("Content-Type", "text/event-stream")
	w.w.Header().Set("Cache-Control", "no-cache")
	w.w.Header().Set("X-Accel-Buffering", "no")
	w.w.WriteHeader(http.StatusOK)
	return w.rc.Flush()
}

// WriteEvent writes one SSE frame. Events with seq 0 (message.delta) deliberately
// omit the id line so they cannot rewind Last-Event-ID.
func (w *Writer) WriteEvent(event domain.NodeEvent) error {
	if err := w.setDeadline(); err != nil {
		return err
	}
	ts := event.CreatedAt
	if ts.IsZero() {
		ts = time.Now().UTC()
	}
	envelope := struct {
		RunID   string          `json:"run_id"`
		Seq     int64           `json:"seq"`
		TS      string          `json:"ts"`
		NodeID  *string         `json:"node_id"`
		Attempt *int            `json:"attempt"`
		Data    json.RawMessage `json:"data"`
	}{
		RunID:   event.RunID,
		Seq:     event.Seq,
		TS:      ts.UTC().Format(time.RFC3339),
		NodeID:  event.NodeID,
		Attempt: event.Attempt,
		Data:    event.Payload,
	}
	data, err := json.Marshal(envelope)
	if err != nil {
		return err
	}

	var b strings.Builder
	if event.Seq > 0 {
		fmt.Fprintf(&b, "id: %d\n", event.Seq)
	}
	fmt.Fprintf(&b, "event: %s\n", event.Type)
	b.WriteString("data: ")
	b.Write(data)
	b.WriteString("\n\n")

	if _, err := io.WriteString(w.w, b.String()); err != nil {
		return err
	}
	return w.rc.Flush()
}

// Heartbeat writes a `: ping` comment line (not an event).
func (w *Writer) Heartbeat() error {
	if err := w.setDeadline(); err != nil {
		return err
	}
	if _, err := io.WriteString(w.w, ": ping\n\n"); err != nil {
		return err
	}
	return w.rc.Flush()
}

func (w *Writer) setDeadline() error {
	err := w.rc.SetWriteDeadline(time.Now().Add(w.writeDeadline))
	if errors.Is(err, http.ErrNotSupported) {
		return nil
	}
	return err
}

// IsSessionEvent applies the EVENTS.md §2 session replay filter.
func IsSessionEvent(eventType string) bool {
	return strings.HasPrefix(eventType, "session.") ||
		eventType == graph.EventMessageCompleted ||
		eventType == graph.EventError
}
