package api

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/realgo/rgdevenv/internal/canon"
	"github.com/realgo/rgdevenv/internal/store"
	"github.com/realgo/rgdevenv/internal/txn"
)

func (h *Handler) registerMappingRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/lbs/{name}/mappings", h.createMapping)
	mux.HandleFunc("PUT /api/v1/lbs/{name}/mappings/{port}", h.putMapping)
	mux.HandleFunc("DELETE /api/v1/lbs/{name}/mappings/{port}", h.deleteMapping)
}

type mappingReq struct {
	ListenPort *int  `json:"listen_port"`
	ListenTLS  *bool `json:"listen_tls"`
	Upstream   *struct {
		Scheme string `json:"scheme"`
		Host   string `json:"host"`
		Port   int    `json:"port"`
		TLS    struct {
			Mode   string `json:"mode"`
			CAName string `json:"ca_name"`
		} `json:"tls"`
	} `json:"upstream"`
	Allocate bool   `json:"allocate"`
	Label    string `json:"label"`
}

// spec builds a txn.MappingSpec for the given listen port (defaults: tls=true).
func (req mappingReq) spec(port int) (txn.MappingSpec, error) {
	tls := true
	if req.ListenTLS != nil {
		tls = *req.ListenTLS
	}
	s := txn.MappingSpec{ListenPort: port, ListenTLS: tls, Allocate: req.Allocate, AllocLabel: req.Label}
	if !req.Allocate {
		if req.Upstream == nil {
			return s, txn.Validation("missing_upstream", "upstream is required unless allocate=true")
		}
		mode := req.Upstream.TLS.Mode
		if mode == "" {
			mode = "verify"
		}
		s.Upstream = store.Upstream{
			Scheme: req.Upstream.Scheme,
			Host:   req.Upstream.Host,
			Port:   req.Upstream.Port,
			TLS:    store.UpstreamTLS{Mode: mode, CAName: req.Upstream.TLS.CAName},
		}
	}
	return s, nil
}

func (h *Handler) createMapping(w http.ResponseWriter, r *http.Request) {
	var req mappingReq
	if err := decodeJSON(r, &req); err != nil {
		h.writeErr(w, err)
		return
	}
	port := 443
	if req.ListenPort != nil {
		port = *req.ListenPort
	}
	spec, err := req.spec(port)
	if err != nil {
		h.writeErr(w, err)
		return
	}
	st, err := h.txn.PutMapping(r.PathValue("name"), spec, false)
	if err != nil {
		h.writeErr(w, err)
		return
	}
	cn, _ := canon.Host(r.PathValue("name"))
	h.audit(r, "create_mapping", fmt.Sprintf("%s:%d", cn, port))
	writeJSON(w, http.StatusCreated, mappingInLB(st, cn, port))
}

func (h *Handler) putMapping(w http.ResponseWriter, r *http.Request) {
	port, err := strconv.Atoi(r.PathValue("port"))
	if err != nil {
		h.writeErr(w, txn.Validation("bad_port", "listen_port path must be an integer"))
		return
	}
	var req mappingReq
	if err := decodeJSON(r, &req); err != nil {
		h.writeErr(w, err)
		return
	}
	if req.ListenPort != nil && *req.ListenPort != port {
		h.writeErr(w, txn.Validation("port_mismatch", "body listen_port must equal the path listen_port"))
		return
	}
	spec, err := req.spec(port)
	if err != nil {
		h.writeErr(w, err)
		return
	}
	st, err := h.txn.PutMapping(r.PathValue("name"), spec, true)
	if err != nil {
		h.writeErr(w, err)
		return
	}
	cn, _ := canon.Host(r.PathValue("name"))
	h.audit(r, "replace_mapping", fmt.Sprintf("%s:%d", cn, port))
	writeJSON(w, http.StatusOK, mappingInLB(st, cn, port))
}

func (h *Handler) deleteMapping(w http.ResponseWriter, r *http.Request) {
	port, err := strconv.Atoi(r.PathValue("port"))
	if err != nil {
		h.writeErr(w, txn.Validation("bad_port", "listen_port path must be an integer"))
		return
	}
	if _, err := h.txn.DeleteMapping(r.PathValue("name"), port); err != nil {
		h.writeErr(w, err)
		return
	}
	cn, _ := canon.Host(r.PathValue("name"))
	h.audit(r, "delete_mapping", fmt.Sprintf("%s:%d", cn, port))
	w.WriteHeader(http.StatusNoContent)
}

func mappingInLB(st *store.State, name string, port int) *store.Mapping {
	lb := lbByName(st, name)
	if lb == nil {
		return nil
	}
	for i := range lb.Mappings {
		if lb.Mappings[i].ListenPort == port {
			return &lb.Mappings[i]
		}
	}
	return nil
}
