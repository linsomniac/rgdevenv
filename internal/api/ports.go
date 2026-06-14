package api

import (
	"net/http"
	"strconv"

	"github.com/realgo/rgdevenv/internal/store"
	"github.com/realgo/rgdevenv/internal/txn"
)

func (h *Handler) registerPortRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/ports", h.listPorts)
	mux.HandleFunc("POST /api/v1/ports/allocate", h.allocatePort)
	mux.HandleFunc("DELETE /api/v1/ports/{port}", h.returnPort)
}

type portsResp struct {
	Start       int                    `json:"start"`
	End         int                    `json:"end"`
	Used        int                    `json:"used"`
	Free        int                    `json:"free"`
	Allocations []store.PortAllocation `json:"allocations"`
}

func (h *Handler) listPorts(w http.ResponseWriter, r *http.Request) {
	snap := h.txn.Snapshot()
	allocs := snap.PortAllocations
	if allocs == nil {
		allocs = []store.PortAllocation{}
	}
	total := h.poolEnd - h.poolStart + 1
	writeJSON(w, http.StatusOK, portsResp{
		Start: h.poolStart, End: h.poolEnd,
		Used: len(allocs), Free: total - len(allocs), Allocations: allocs,
	})
}

type allocReq struct {
	Owner string `json:"owner"`
	Label string `json:"label"`
}

func (h *Handler) allocatePort(w http.ResponseWriter, r *http.Request) {
	var req allocReq
	if err := decodeJSON(r, &req); err != nil {
		h.writeErr(w, err)
		return
	}
	_, a, err := h.txn.AllocatePort(req.Owner, req.Label)
	if err != nil {
		h.writeErr(w, err)
		return
	}
	h.audit(r, "allocate_port", strconv.Itoa(a.Port))
	writeJSON(w, http.StatusCreated, map[string]any{"id": a.ID, "port": a.Port})
}

func (h *Handler) returnPort(w http.ResponseWriter, r *http.Request) {
	port, err := strconv.Atoi(r.PathValue("port"))
	if err != nil {
		h.writeErr(w, txn.Validation("bad_port", "port path must be an integer"))
		return
	}
	if _, err := h.txn.ReturnPort(port); err != nil {
		h.writeErr(w, err)
		return
	}
	h.audit(r, "return_port", strconv.Itoa(port))
	w.WriteHeader(http.StatusNoContent)
}
