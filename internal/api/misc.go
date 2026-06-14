package api

import (
	"net/http"

	"github.com/realgo/rgdevenv/internal/txn"
	"github.com/realgo/rgdevenv/internal/upstream"
)

func (h *Handler) registerMiscRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/cas", h.listCAs)
	mux.HandleFunc("GET /api/v1/status", h.status)
}

func (h *Handler) listCAs(w http.ResponseWriter, r *http.Request) {
	names, err := upstream.ListCAs(h.caDir)
	if err != nil {
		h.logger.Error("api: list cas", "error", err)
		h.writeErr(w, txn.Validation("ca_dir_error", "CA directory is unavailable"))
		return
	}
	if names == nil {
		names = []string{}
	}
	writeJSON(w, http.StatusOK, names)
}

type statusResp struct {
	Version         string `json:"version"`
	HTTPSPort       int    `json:"https_port"`
	HTTPPort        int    `json:"http_port"`
	ActiveListeners []int  `json:"active_listeners"`
	LoadBalancers   int    `json:"load_balancers"`
	Mappings        int    `json:"mappings"`
	Allocations     int    `json:"allocations"`
}

func (h *Handler) status(w http.ResponseWriter, r *http.Request) {
	snap := h.txn.Snapshot()
	mappings := 0
	for _, lb := range snap.LoadBalancers {
		mappings += len(lb.Mappings)
	}
	active := []int{}
	if h.activePorts != nil {
		active = h.activePorts()
	}
	writeJSON(w, http.StatusOK, statusResp{
		Version: h.version, HTTPSPort: h.httpsPort, HTTPPort: h.httpPort,
		ActiveListeners: active, LoadBalancers: len(snap.LoadBalancers),
		Mappings: mappings, Allocations: len(snap.PortAllocations),
	})
}
