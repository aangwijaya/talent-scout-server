package handlers

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/aangwijaya/talent-scout-server/internal/http/apierror"
	"github.com/aangwijaya/talent-scout-server/internal/sse"
	"github.com/aangwijaya/talent-scout-server/internal/store"
)

type GraphRuns struct {
	store store.Store
}

func NewGraphRuns(st store.Store) *GraphRuns {
	return &GraphRuns{store: st}
}

func (h *GraphRuns) Events(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		apierror.Write(w, http.StatusServiceUnavailable, apierror.CodeDBUnavailable, "database is unavailable", nil)
		return
	}
	if _, err := h.store.GetGraphRun(r.Context(), chi.URLParam(r, "runId")); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			apierror.Write(w, http.StatusNotFound, apierror.CodeNotFound, "graph run not found", nil)
			return
		}
		apierror.Write(w, http.StatusInternalServerError, apierror.CodeInternalError, "could not load graph run", nil)
		return
	}

	writer := sse.NewWriter(w, sse.DefaultWriteDeadline)
	_ = writer.Prepare()
}
