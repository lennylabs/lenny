// SPDX-License-Identifier: MIT

// Package sessionserver implements the §15.1 REST session endpoints
// as an http.Handler. The handler is backed by a sessionstore.Store
// and uses pkg/api/v1/session.Validate to enforce the §15.1
// precondition table on every state-mutating endpoint.
//
// This is the minimal Lenny gateway: no auth, no Postgres, no
// Kubernetes. The tenant_id is taken from a development header
// (X-Lenny-Tenant-ID) or, when absent, defaults to "default" — the
// single-tenant mode from §10.2. Future phases swap in the OIDC
// middleware that produces a validated tenant via pkg/auth.
//
// The handler implements the §15.1 endpoints that drive the
// session lifecycle state machine (create, finalize, start,
// interrupt, terminate, resume, derive, delete, list, get).
// Upload, message-injection, derive-failure auditing, and the
// elicitation/respond / tool-call approve paths are deferred to the
// phases that ship workspace materialisation, the inter-session
// inbox, and the elicitation chain.
package sessionserver

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
)

// Server is the §15.1 session HTTP handler.
type Server struct {
	store           sessionstore.Store
	clock           func() time.Time
	idFn            func() string
	deriveAuditSink DeriveAuditSink
}

// Options configures the Server at construction.
type Options struct {
	// Clock overrides time.Now. Tests inject a fixed clock; production
	// leaves this nil.
	Clock func() time.Time

	// IDFunc overrides the session-id generator. Tests inject a
	// deterministic generator; production leaves this nil and the
	// server uses a crypto/rand-backed hex generator.
	IDFunc func() string

	// DeriveAuditSink, when set, receives the
	// `derive.isolation_downgrade` audit event per §7.1 derive rule 5
	// whenever a platform-admin exercises the
	// `allowIsolationDowngrade: true` override. Production wires this
	// to the §11.7 audit pipeline; nil disables the emission (and the
	// override still applies).
	DeriveAuditSink DeriveAuditSink
}

// New returns a Server bound to the supplied store.
func New(store sessionstore.Store, opts Options) *Server {
	s := &Server{
		store:           store,
		clock:           opts.Clock,
		idFn:            opts.IDFunc,
		deriveAuditSink: opts.DeriveAuditSink,
	}
	if s.clock == nil {
		s.clock = func() time.Time { return time.Now().UTC() }
	}
	if s.idFn == nil {
		s.idFn = randomSessionID
	}
	return s
}

// Handler returns the http.Handler that routes the §15.1 session
// endpoints.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/sessions", s.handleCreate)
	mux.HandleFunc("GET /v1/sessions", s.handleList)
	mux.HandleFunc("GET /v1/sessions/{id}", s.handleGet)
	mux.HandleFunc("DELETE /v1/sessions/{id}", s.handleDelete)
	mux.HandleFunc("POST /v1/sessions/{id}/finalize", s.handleTransition(session.EndpointFinalize, transitionFinalize))
	mux.HandleFunc("POST /v1/sessions/{id}/start", s.handleTransition(session.EndpointStart, transitionStart))
	mux.HandleFunc("POST /v1/sessions/{id}/interrupt", s.handleTransition(session.EndpointInterrupt, transitionInterrupt))
	mux.HandleFunc("POST /v1/sessions/{id}/terminate", s.handleTransition(session.EndpointTerminate, transitionTerminate))
	mux.HandleFunc("POST /v1/sessions/{id}/resume", s.handleTransition(session.EndpointResume, transitionResume))
	mux.HandleFunc("POST /v1/sessions/{id}/derive", s.handleDerive)
	return mux
}

// CreateSessionRequest is the §15.1 POST /v1/sessions body. The
// minimal gateway accepts only runtimeRef and userId; later phases
// extend this with workspacePlan, env, timeouts, etc.
type CreateSessionRequest struct {
	RuntimeRef string `json:"runtimeRef"`
	UserID     string `json:"userId"`
}

// SessionResponse is the §15.1 GET /v1/sessions/{id} envelope.
type SessionResponse struct {
	ID         string `json:"id"`
	TenantID   string `json:"tenantId"`
	UserID     string `json:"userId,omitempty"`
	RuntimeRef string `json:"runtimeRef,omitempty"`
	State      string `json:"state"`
	CreatedAt  string `json:"createdAt"`
	UpdatedAt  string `json:"updatedAt"`

	FailureClass string `json:"failureClass,omitempty"`
}

// errorEnvelope is the §15.1 error response shape.
type errorEnvelope struct {
	Error errorBody `json:"error"`
}

type errorBody struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

// handleCreate implements POST /v1/sessions. Returns 201 with the
// SessionResponse envelope on success.
func (s *Server) handleCreate(w http.ResponseWriter, r *http.Request) {
	tenantID := s.resolveTenant(r)

	var req CreateSessionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "request body is not valid JSON", nil)
		return
	}
	if req.RuntimeRef == "" {
		s.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "runtimeRef is required", map[string]any{"field": "runtimeRef"})
		return
	}

	row := sessionstore.Session{
		ID:         s.idFn(),
		TenantID:   tenantID,
		UserID:     req.UserID,
		RuntimeRef: req.RuntimeRef,
		State:      session.StateCreated,
		CreatedAt:  s.clock(),
	}
	row.UpdatedAt = row.CreatedAt
	if err := s.store.Create(r.Context(), row); err != nil {
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	s.writeSession(w, http.StatusCreated, row)
}

// handleGet implements GET /v1/sessions/{id}.
func (s *Server) handleGet(w http.ResponseWriter, r *http.Request) {
	tenantID := s.resolveTenant(r)
	id := r.PathValue("id")
	row, err := s.store.Get(r.Context(), tenantID, id)
	if err != nil {
		if errors.Is(err, sessionstore.ErrNotFound) {
			s.writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "session not found", nil)
			return
		}
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	s.writeSession(w, http.StatusOK, row)
}

// handleList implements GET /v1/sessions. Supports the §15.1 ?state=
// and ?runtime= filters in their basic form.
func (s *Server) handleList(w http.ResponseWriter, r *http.Request) {
	tenantID := s.resolveTenant(r)
	filter := sessionstore.ListFilter{
		State:        session.State(r.URL.Query().Get("state")),
		RuntimeRef:   r.URL.Query().Get("runtime"),
		FailureClass: session.FailureClass(r.URL.Query().Get("failureClass")),
	}
	rows, err := s.store.List(r.Context(), tenantID, filter)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	out := make([]SessionResponse, 0, len(rows))
	for _, row := range rows {
		out = append(out, toResponse(row))
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{"sessions": out})
}

// handleDelete implements DELETE /v1/sessions/{id} per §15.1: every
// non-terminal state transitions to cancelled.
func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request) {
	tenantID := s.resolveTenant(r)
	id := r.PathValue("id")
	row, err := s.store.Get(r.Context(), tenantID, id)
	if err != nil {
		if errors.Is(err, sessionstore.ErrNotFound) {
			s.writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "session not found", nil)
			return
		}
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if err := session.Validate(session.PreconditionRequest{
		Endpoint:     session.EndpointDelete,
		CurrentState: row.State,
	}); err != nil {
		s.writePreconditionError(w, err)
		return
	}
	updated, err := s.store.Update(r.Context(), tenantID, id, func(row *sessionstore.Session) error {
		row.State = session.StateCancelled
		return nil
	})
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	s.writeSession(w, http.StatusOK, updated)
}

// handleTransition is the shared handler shape for every
// state-mutating endpoint that does not carry a body (finalize,
// start, interrupt, terminate, resume). The supplied transition
// function captures the next state.
func (s *Server) handleTransition(endpoint session.Endpoint, transition func(*sessionstore.Session)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tenantID := s.resolveTenant(r)
		id := r.PathValue("id")
		row, err := s.store.Get(r.Context(), tenantID, id)
		if err != nil {
			if errors.Is(err, sessionstore.ErrNotFound) {
				s.writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "session not found", nil)
				return
			}
			s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
			return
		}
		if err := session.Validate(session.PreconditionRequest{
			Endpoint:     endpoint,
			CurrentState: row.State,
		}); err != nil {
			s.writePreconditionError(w, err)
			return
		}
		updated, err := s.store.Update(r.Context(), tenantID, id, func(row *sessionstore.Session) error {
			transition(row)
			return nil
		})
		if err != nil {
			s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
			return
		}
		s.writeSession(w, http.StatusOK, updated)
	}
}

// transitionFinalize: per §15.1, /finalize transitions
// created → finalizing → ready. The minimal gateway short-circuits
// the materialisation step and goes straight to ready.
func transitionFinalize(row *sessionstore.Session) { row.State = session.StateReady }

// transitionStart: per §15.1, /start transitions ready → starting →
// running. Short-circuits to running.
func transitionStart(row *sessionstore.Session) { row.State = session.StateRunning }

// transitionInterrupt: per §15.1, /interrupt transitions running →
// suspended.
func transitionInterrupt(row *sessionstore.Session) { row.State = session.StateSuspended }

// transitionTerminate: per §15.1, /terminate transitions any
// non-terminal → completed.
func transitionTerminate(row *sessionstore.Session) { row.State = session.StateCompleted }

// transitionResume: per §15.1, /resume transitions
// awaiting_client_action → resume_pending → running. The minimal
// gateway short-circuits to running.
func transitionResume(row *sessionstore.Session) { row.State = session.StateRunning }

// resolveTenant reads the dev-mode X-Lenny-Tenant-ID header, falling
// back to "default" per §10.2 single-tenant mode.
func (s *Server) resolveTenant(r *http.Request) string {
	if v := r.Header.Get("X-Lenny-Tenant-ID"); v != "" {
		return v
	}
	return "default"
}

// writeSession serialises a Session row as the §15.1 envelope and
// writes it with the supplied status code.
func (s *Server) writeSession(w http.ResponseWriter, code int, row sessionstore.Session) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(toResponse(row))
}

// writeError writes a §15.1 error envelope.
func (s *Server) writeError(w http.ResponseWriter, status int, code, message string, details map[string]any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorEnvelope{Error: errorBody{
		Code:    code,
		Message: message,
		Details: details,
	}})
}

// writePreconditionError maps a session.PreconditionError to the
// §15.1 INVALID_STATE_TRANSITION envelope.
func (s *Server) writePreconditionError(w http.ResponseWriter, err error) {
	var pe *session.PreconditionError
	if !errors.As(err, &pe) {
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	allowed := make([]string, 0, len(pe.AllowedStates))
	for _, st := range pe.AllowedStates {
		allowed = append(allowed, string(st))
	}
	s.writeError(w, pe.Code(), pe.ErrorCode(), pe.Error(), map[string]any{
		"currentState":  string(pe.CurrentState),
		"allowedStates": allowed,
	})
}

// toResponse converts a Session row into the §15.1 wire envelope.
func toResponse(row sessionstore.Session) SessionResponse {
	out := SessionResponse{
		ID:         row.ID,
		TenantID:   row.TenantID,
		UserID:     row.UserID,
		RuntimeRef: row.RuntimeRef,
		State:      string(row.State),
		CreatedAt:  row.CreatedAt.UTC().Format(time.RFC3339Nano),
		UpdatedAt:  row.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}
	if row.FailureClass != "" {
		out.FailureClass = string(row.FailureClass)
	}
	return out
}

// randomSessionID returns a 16-character lowercase-hex session id
// prefixed with "sess_". 8 random bytes → 128 bits of entropy, plenty
// for collision-free generation in the in-memory store.
func randomSessionID() string {
	var buf [8]byte
	_, _ = rand.Read(buf[:])
	return "sess_" + strings.ToLower(hex.EncodeToString(buf[:]))
}

// Now exposes the configured clock so callers that hold a reference
// to the Server can compose with the same time source. Useful for
// tests that need to verify timestamp behaviour.
func (s *Server) Now() time.Time { return s.clock() }

// Context-typed alias to satisfy go vet's pattern.
type ctxKey struct{}

func contextWithTenant(ctx context.Context, tenant string) context.Context {
	return context.WithValue(ctx, ctxKey{}, tenant)
}

func tenantFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(ctxKey{}).(string); ok {
		return v
	}
	return ""
}

var (
	_ = contextWithTenant
	_ = tenantFromContext
)
