// Package httpapi exposes a small JSON REST surface for the inspector web UI.
// It reads projections directly from storage and delegates mutations to the
// control-plane service so metrics and validation stay in one place.
package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"

	commonv1 "github.com/AymanYouss/chronos-engine/api/gen/chronos/v1"
	"github.com/AymanYouss/chronos-engine/internal/server"
	"github.com/AymanYouss/chronos-engine/internal/storage"
)

// API wires the REST handlers.
type API struct {
	store     storage.Store
	svc       *server.Service
	assetsDir string
}

// New builds the API. assetsDir, when non-empty, serves the built web UI.
func New(store storage.Store, svc *server.Service, assetsDir string) *API {
	return &API{store: store, svc: svc, assetsDir: assetsDir}
}

// Handler returns the fully-configured HTTP handler.
func (a *API) Handler() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(30 * time.Second))
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins: []string{"*"},
		AllowedMethods: []string{"GET", "POST", "OPTIONS"},
		AllowedHeaders: []string{"Content-Type"},
	}))

	r.Get("/healthz", a.health)
	r.Route("/api", func(r chi.Router) {
		r.Get("/stats", a.stats)
		r.Get("/workflows", a.listWorkflows)
		r.Post("/workflows", a.startWorkflow)
		r.Get("/workflows/{workflowID}/{runID}", a.describeWorkflow)
		r.Get("/workflows/{workflowID}/{runID}/history", a.getHistory)
		r.Post("/workflows/{workflowID}/{runID}/signal", a.signalWorkflow)
	})

	if a.assetsDir != "" {
		a.mountUI(r)
	}
	return r
}

func (a *API) health(w http.ResponseWriter, r *http.Request) {
	if err := a.store.Ping(r.Context()); err != nil {
		writeError(w, http.StatusServiceUnavailable, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (a *API) stats(w http.ResponseWriter, r *http.Request) {
	counts, err := a.store.CountByStatus(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	out := map[string]int64{
		"running": 0, "completed": 0, "failed": 0, "timedOut": 0, "terminated": 0, "total": 0,
	}
	for status, n := range counts {
		out[statusName(status)] += n
		out["total"] += n
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *API) listWorkflows(w http.ResponseWriter, r *http.Request) {
	status := parseStatus(r.URL.Query().Get("status"))
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	execs, err := a.store.ListWorkflows(r.Context(), status, limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	out := make([]executionDTO, 0, len(execs))
	for _, e := range execs {
		out = append(out, toExecutionDTO(e))
	}
	writeJSON(w, http.StatusOK, map[string]any{"workflows": out})
}

func (a *API) describeWorkflow(w http.ResponseWriter, r *http.Request) {
	exec, err := a.store.DescribeWorkflow(r.Context(), chi.URLParam(r, "workflowID"), chi.URLParam(r, "runID"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toExecutionDTO(exec))
}

func (a *API) getHistory(w http.ResponseWriter, r *http.Request) {
	events, err := a.store.GetHistory(r.Context(), chi.URLParam(r, "workflowID"), chi.URLParam(r, "runID"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	out := make([]eventDTO, 0, len(events))
	for _, e := range events {
		out = append(out, toEventDTO(e))
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": out})
}

type startRequest struct {
	WorkflowID   string `json:"workflowId"`
	WorkflowType string `json:"workflowType"`
	TaskQueue    string `json:"taskQueue"`
	Input        string `json:"input"`
}

func (a *API) startWorkflow(w http.ResponseWriter, r *http.Request) {
	var req startRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if req.TaskQueue == "" {
		req.TaskQueue = "default"
	}
	resp, err := a.svc.StartWorkflow(r.Context(), &commonv1.StartWorkflowRequest{
		WorkflowId:   req.WorkflowID,
		WorkflowType: req.WorkflowType,
		TaskQueue:    req.TaskQueue,
		Input:        []byte(req.Input),
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"workflowId":     resp.WorkflowId,
		"runId":          resp.RunId,
		"alreadyStarted": resp.AlreadyStarted,
	})
}

type signalRequest struct {
	SignalName string `json:"signalName"`
	Input      string `json:"input"`
}

func (a *API) signalWorkflow(w http.ResponseWriter, r *http.Request) {
	var req signalRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	_, err := a.svc.SignalWorkflow(r.Context(), &commonv1.SignalWorkflowRequest{
		WorkflowId: chi.URLParam(r, "workflowID"),
		RunId:      chi.URLParam(r, "runID"),
		SignalName: req.SignalName,
		Input:      []byte(req.Input),
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "signaled"})
}

// mountUI serves the built single-page app with history-mode fallback.
func (a *API) mountUI(r chi.Router) {
	fs := http.FileServer(http.Dir(a.assetsDir))
	r.Handle("/assets/*", fs)
	r.Get("/*", func(w http.ResponseWriter, req *http.Request) {
		path := filepath.Join(a.assetsDir, filepath.Clean(req.URL.Path))
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			fs.ServeHTTP(w, req)
			return
		}
		http.ServeFile(w, req, filepath.Join(a.assetsDir, "index.html"))
	})
}

func writeStoreError(w http.ResponseWriter, err error) {
	if errors.Is(err, storage.ErrNotFound) {
		writeError(w, http.StatusNotFound, err)
		return
	}
	writeError(w, http.StatusInternalServerError, err)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, err error) {
	writeJSON(w, code, map[string]string{"error": err.Error()})
}
