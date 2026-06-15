package api

import (
	"net/http"

	"github.com/realgo/rgdevenv/internal/canon"
	"github.com/realgo/rgdevenv/internal/store"
	"github.com/realgo/rgdevenv/internal/txn"
)

func (h *Handler) registerLBRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/lbs", h.listLBs)
	mux.HandleFunc("POST /api/v1/lbs", h.createLB)
	mux.HandleFunc("GET /api/v1/lbs/{name}", h.getLB)
	mux.HandleFunc("PATCH /api/v1/lbs/{name}", h.patchLB)
	mux.HandleFunc("DELETE /api/v1/lbs/{name}", h.deleteLB)
}

func (h *Handler) listLBs(w http.ResponseWriter, r *http.Request) {
	snap := h.txn.Snapshot()
	out := make([]lbView, 0, len(snap.LoadBalancers))
	for _, lb := range snap.LoadBalancers {
		out = append(out, h.toLBView(lb))
	}
	writeJSON(w, http.StatusOK, out)
}

type createLBReq struct {
	Name  string `json:"name"`
	Label string `json:"label"`
}

func (h *Handler) createLB(w http.ResponseWriter, r *http.Request) {
	var req createLBReq
	if err := decodeJSON(r, &req); err != nil {
		h.writeErr(w, err)
		return
	}
	if req.Name == "" {
		h.writeErr(w, txn.Validation("missing_name", "name is required"))
		return
	}
	st, err := h.txn.CreateLB(req.Name, req.Label)
	if err != nil {
		h.writeErr(w, err)
		return
	}
	cn, _ := canon.Host(req.Name)
	h.audit(r, "create_lb", cn)
	writeJSON(w, http.StatusCreated, h.toLBView(*lbByName(st, cn)))
}

func (h *Handler) getLB(w http.ResponseWriter, r *http.Request) {
	cn, err := canon.Host(r.PathValue("name"))
	if err != nil {
		h.writeErr(w, txn.Validation("invalid_hostname", err.Error()))
		return
	}
	lb := lbByName(h.txn.Snapshot(), cn)
	if lb == nil {
		h.writeErr(w, txn.NotFound("lb_not_found", "load balancer not found"))
		return
	}
	writeJSON(w, http.StatusOK, h.toLBView(*lb))
}

type patchLBReq struct {
	Label string `json:"label"`
}

func (h *Handler) patchLB(w http.ResponseWriter, r *http.Request) {
	var req patchLBReq
	if err := decodeJSON(r, &req); err != nil {
		h.writeErr(w, err)
		return
	}
	st, err := h.txn.UpdateLBLabel(r.PathValue("name"), req.Label)
	if err != nil {
		h.writeErr(w, err)
		return
	}
	cn, _ := canon.Host(r.PathValue("name"))
	h.audit(r, "update_lb", cn)
	writeJSON(w, http.StatusOK, h.toLBView(*lbByName(st, cn)))
}

func (h *Handler) deleteLB(w http.ResponseWriter, r *http.Request) {
	if _, err := h.txn.DeleteLB(r.PathValue("name")); err != nil {
		h.writeErr(w, err)
		return
	}
	cn, _ := canon.Host(r.PathValue("name"))
	h.audit(r, "delete_lb", cn)
	w.WriteHeader(http.StatusNoContent)
}

func lbByName(st *store.State, name string) *store.LoadBalancer {
	for i := range st.LoadBalancers {
		if st.LoadBalancers[i].Name == name {
			return &st.LoadBalancers[i]
		}
	}
	return nil
}
