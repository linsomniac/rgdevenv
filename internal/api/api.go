// Package api implements the rgdevenv management REST API (§12). All /api/v1/*
// routes are bearer-authenticated and rate-limited; /healthz is open.
package api

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/realgo/rgdevenv/internal/auth"
	"github.com/realgo/rgdevenv/internal/txn"
)

// Deps are the dependencies for the management handler.
type Deps struct {
	Txn         *txn.Manager
	Auth        *auth.Authenticator
	Limiter     *auth.RateLimiter
	CADir       string
	Version     string
	HTTPSPort   int
	HTTPPort    int
	PoolStart   int
	PoolEnd     int
	ActivePorts func() []int
	Logger      *slog.Logger
}

// Handler is the management-plane http.Handler.
type Handler struct {
	txn         *txn.Manager
	auth        *auth.Authenticator
	limiter     *auth.RateLimiter
	caDir       string
	version     string
	httpsPort   int
	httpPort    int
	poolStart   int
	poolEnd     int
	activePorts func() []int
	logger      *slog.Logger
	mux         http.Handler
}

// New builds the management handler with all routes wired.
func New(d Deps) *Handler {
	if d.Logger == nil {
		d.Logger = discardLogger()
	}
	h := &Handler{
		txn: d.Txn, auth: d.Auth, limiter: d.Limiter, caDir: d.CADir,
		version: d.Version, httpsPort: d.HTTPSPort, httpPort: d.HTTPPort,
		poolStart: d.PoolStart, poolEnd: d.PoolEnd,
		activePorts: d.ActivePorts, logger: d.Logger,
	}
	h.mux = h.buildMux()
	return h
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) { h.mux.ServeHTTP(w, r) }

func (h *Handler) buildMux() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", h.healthz)

	api := http.NewServeMux()
	// AIDEV-NOTE: route registration is added by later tasks (lbs, mappings,
	// ports, cas, status). Keep all /api/v1/* routes on THIS sub-mux so the auth
	// middleware below covers every one of them.
	h.registerLBRoutes(api)
	h.registerMappingRoutes(api)
	h.registerPortRoutes(api)
	h.registerMiscRoutes(api)
	// <register-api-routes>

	mux.Handle("/api/v1/", h.authMiddleware(api))
	// AIDEV-TODO(phase2b): mount the static login shell + dashboard at "/".
	return mux
}

func (h *Handler) healthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// --- JSON / error helpers ---

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

type errorBody struct {
	Error string `json:"error"`
	Code  string `json:"code"`
}

// writeErr maps a (possibly txn-typed) error to an HTTP status + JSON body (§12).
func (h *Handler) writeErr(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	code, msg := "internal", "internal error"
	var te *txn.Error
	if errors.As(err, &te) {
		code, msg = te.Code, te.Msg
		switch {
		case errors.Is(err, txn.ErrValidation):
			status = http.StatusBadRequest
		case errors.Is(err, txn.ErrConflict):
			status = http.StatusConflict
		case errors.Is(err, txn.ErrNotFound):
			status = http.StatusNotFound
		}
	}
	// AIDEV-NOTE: log any 5xx — a non-txn error OR a txn.Error whose kind isn't
	// mapped above (a reminder to add a case when introducing a new sentinel).
	if status == http.StatusInternalServerError {
		h.logger.Error("api: 5xx", "error", err)
	}
	writeJSON(w, status, errorBody{Error: msg, Code: code})
}

// decodeJSON reads a JSON body with unknown-field rejection.
func decodeJSON(r *http.Request, v any) error {
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return txn.Validation("bad_json", "invalid request body: "+err.Error())
	}
	return nil
}

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }
