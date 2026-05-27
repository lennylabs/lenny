// SPDX-License-Identifier: MIT

// Package credentialserver implements the §15.1 `/v1/credentials`
// end-user credential endpoints over the §4.9 credential registry.
//
// Every endpoint is scoped to the authenticated caller: a user can
// only register, read, rotate, revoke, and delete their own
// credentials. The wire payload NEVER carries secret material per
// §15.1 ("no secret material returned").
package credentialserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	pkgcred "github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/credentialstore"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
)

// AuditSink receives §4.9.2 credential audit events. The server emits
// one event per successful user-credential mutation (register, rotate,
// revoke, delete). A nil sink disables emission; the operation still
// succeeds. Implementations must be non-blocking — the handler does not
// wait for delivery.
//
// spec: §4.9.2 (Credential Audit Events) — every credential lifecycle
// event is written to the §11.7 hash-chained EventStore.
type AuditSink interface {
	EmitCredentialEvent(ctx context.Context, eventType string, detail map[string]any)
}

// Server is the §15.1 /v1/credentials HTTP handler.
type Server struct {
	store credentialstore.Store
	audit AuditSink
}

// New returns a Server over the supplied credential store.
func New(store credentialstore.Store) *Server {
	return &Server{store: store}
}

// WithAudit wires the §4.9.2 credential audit sink. Without it the
// endpoints still mutate the store; they simply emit no audit event.
func (s *Server) WithAudit(sink AuditSink) *Server {
	s.audit = sink
	return s
}

// emit fires a §4.9.2 credential audit event when an AuditSink is wired.
func (s *Server) emit(ctx context.Context, eventType pkgcred.AuditEventType, detail map[string]any) {
	if s.audit == nil {
		return
	}
	s.audit.EmitCredentialEvent(ctx, eventType.String(), detail)
}

// Handler returns the http.Handler routing the credential endpoints.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/credentials", s.handleRegister)
	mux.HandleFunc("GET /v1/credentials", s.handleList)
	mux.HandleFunc("PUT /v1/credentials/{ref}", s.handleRotate)
	mux.HandleFunc("POST /v1/credentials/{ref}/revoke", s.handleRevoke)
	mux.HandleFunc("DELETE /v1/credentials/{ref}", s.handleDelete)
	return mux
}

// CredentialPayload is the §15.1 wire shape — secret-free.
type CredentialPayload struct {
	Ref         string `json:"ref"`
	Provider    string `json:"provider"`
	Environment string `json:"environment,omitempty"`
	Status      string `json:"status"`
	CreatedAt   string `json:"createdAt,omitempty"`
	RotatedAt   string `json:"rotatedAt,omitempty"`
	RevokedAt   string `json:"revokedAt,omitempty"`
	LastUsedAt  string `json:"lastUsedAt,omitempty"`
}

// RegisterRequest is the §15.1 POST /v1/credentials body.
//
// Environment is the §4.3 line 202 environment scoping field. An empty
// value selects the no-environment scope; a non-empty value scopes the
// credential to that §10.6 environment.
type RegisterRequest struct {
	Provider    string `json:"provider"`
	Environment string `json:"environment,omitempty"`
	Secret      string `json:"secret"`
}

// RotateRequest is the §15.1 PUT /v1/credentials/{ref} body.
type RotateRequest struct {
	Secret string `json:"secret"`
}

// RevokeRequest is the optional POST /v1/credentials/{ref}/revoke body.
// The reason is recorded in the §4.9.2 credential.user_revoked audit
// event. An empty or absent body is valid.
type RevokeRequest struct {
	Reason string `json:"reason,omitempty"`
}

func toPayload(c credentialstore.Credential) CredentialPayload {
	out := CredentialPayload{
		Ref:         c.Ref,
		Provider:    string(c.Provider),
		Environment: c.Environment,
		Status:      string(c.Status),
	}
	if !c.CreatedAt.IsZero() {
		out.CreatedAt = c.CreatedAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00")
	}
	if !c.RotatedAt.IsZero() {
		out.RotatedAt = c.RotatedAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00")
	}
	if !c.RevokedAt.IsZero() {
		out.RevokedAt = c.RevokedAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00")
	}
	if !c.LastUsedAt.IsZero() {
		out.LastUsedAt = c.LastUsedAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00")
	}
	return out
}

// caller resolves the authenticated (tenant, user) pair. Both must
// be present — credential endpoints are user-scoped.
func caller(r *http.Request) (tenant, user string, ok bool) {
	p, found := authmw.FromContext(r.Context())
	if !found || p.TenantID == "" || p.Subject == "" {
		return "", "", false
	}
	return p.TenantID, p.Subject, true
}

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	tenant, user, ok := caller(r)
	if !ok {
		// spec: §15.1 line 986 — UNAUTHORIZED is the canonical 401 code
		// for "missing or invalid auth credentials".
		writeErr(w, http.StatusUnauthorized, "UNAUTHORIZED", "credential endpoints require an authenticated user")
		return
	}
	var req RegisterRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "INVALID_REQUEST", "request body is not valid JSON")
		return
	}
	if req.Secret == "" {
		writeErr(w, http.StatusBadRequest, "VALIDATION_ERROR", "secret is required")
		return
	}
	provider := pkgcred.Provider(req.Provider)
	if !provider.IsValid() {
		writeErr(w, http.StatusBadRequest, "VALIDATION_ERROR", "provider is not a recognised §4.9 provider")
		return
	}
	c, err := s.store.Register(r.Context(), tenant, user, provider, req.Environment, req.Secret)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error())
		return
	}
	// spec: §4.9.2 — credential.registered (tenant_id, user_id, provider,
	// credential_ref).
	s.emit(r.Context(), pkgcred.AuditCredentialRegistered, map[string]any{
		"tenant_id":      tenant,
		"user_id":        user,
		"provider":       string(c.Provider),
		"credential_ref": c.Ref,
	})
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(toPayload(c))
}

func (s *Server) handleList(w http.ResponseWriter, r *http.Request) {
	tenant, user, ok := caller(r)
	if !ok {
		// spec: §15.1 line 986 — UNAUTHORIZED is the canonical 401 code
		// for "missing or invalid auth credentials".
		writeErr(w, http.StatusUnauthorized, "UNAUTHORIZED", "credential endpoints require an authenticated user")
		return
	}
	creds, err := s.store.List(r.Context(), tenant, user)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}
	out := make([]CredentialPayload, 0, len(creds))
	for _, c := range creds {
		out = append(out, toPayload(c))
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"credentials": out})
}

func (s *Server) handleRotate(w http.ResponseWriter, r *http.Request) {
	tenant, user, ok := caller(r)
	if !ok {
		// spec: §15.1 line 986 — UNAUTHORIZED is the canonical 401 code
		// for "missing or invalid auth credentials".
		writeErr(w, http.StatusUnauthorized, "UNAUTHORIZED", "credential endpoints require an authenticated user")
		return
	}
	ref := r.PathValue("ref")
	if !s.ownedBy(r, tenant, user, ref) {
		writeErr(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "credential not found")
		return
	}
	var req RotateRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "INVALID_REQUEST", "request body is not valid JSON")
		return
	}
	if req.Secret == "" {
		writeErr(w, http.StatusBadRequest, "VALIDATION_ERROR", "secret is required")
		return
	}
	c, err := s.store.Rotate(r.Context(), tenant, ref, req.Secret)
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	// spec: §4.9.2 — credential.rotated (tenant_id, user_id, provider,
	// credential_ref, active_leases_rotated). User-backed lease rotation
	// is not yet wired (F-4.9.8 rejects it as Unimplemented), so no active
	// lease is rotated by this path today; the count is reported as 0.
	s.emit(r.Context(), pkgcred.AuditCredentialRotated, map[string]any{
		"tenant_id":             tenant,
		"user_id":               user,
		"provider":              string(c.Provider),
		"credential_ref":        c.Ref,
		"active_leases_rotated": 0,
	})
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(toPayload(c))
}

func (s *Server) handleRevoke(w http.ResponseWriter, r *http.Request) {
	tenant, user, ok := caller(r)
	if !ok {
		// spec: §15.1 line 986 — UNAUTHORIZED is the canonical 401 code
		// for "missing or invalid auth credentials".
		writeErr(w, http.StatusUnauthorized, "UNAUTHORIZED", "credential endpoints require an authenticated user")
		return
	}
	ref := r.PathValue("ref")
	if !s.ownedBy(r, tenant, user, ref) {
		writeErr(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "credential not found")
		return
	}
	// The revoke body is optional; an absent or empty body is valid and
	// records an empty reason.
	var req RevokeRequest
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req)
	c, err := s.store.Revoke(r.Context(), tenant, ref)
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	// spec: §4.9.2 — credential.user_revoked (tenant_id, user_id,
	// provider, credential_ref, reason, active_leases_terminated). This
	// handler revokes the registry record; user-backed lease termination
	// at the session is not yet wired (F-4.9.15), so the count is 0.
	s.emit(r.Context(), pkgcred.AuditCredentialUserRevoked, map[string]any{
		"tenant_id":                tenant,
		"user_id":                  user,
		"provider":                 string(c.Provider),
		"credential_ref":           c.Ref,
		"reason":                   req.Reason,
		"active_leases_terminated": 0,
	})
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(toPayload(c))
}

func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request) {
	tenant, user, ok := caller(r)
	if !ok {
		// spec: §15.1 line 986 — UNAUTHORIZED is the canonical 401 code
		// for "missing or invalid auth credentials".
		writeErr(w, http.StatusUnauthorized, "UNAUTHORIZED", "credential endpoints require an authenticated user")
		return
	}
	ref := r.PathValue("ref")
	// Get the credential before deleting so the §4.9.2 audit event can
	// record its provider, and to enforce per-user ownership.
	c, err := s.store.Get(r.Context(), tenant, ref)
	if err != nil || c.UserID != user {
		writeErr(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "credential not found")
		return
	}
	if err := s.store.Delete(r.Context(), tenant, ref); err != nil {
		s.writeStoreErr(w, err)
		return
	}
	// spec: §4.9.2 — credential.deleted (tenant_id, user_id, provider,
	// credential_ref).
	s.emit(r.Context(), pkgcred.AuditCredentialDeleted, map[string]any{
		"tenant_id":      tenant,
		"user_id":        user,
		"provider":       string(c.Provider),
		"credential_ref": c.Ref,
	})
	w.WriteHeader(http.StatusNoContent)
}

// ownedBy confirms the credential exists in the tenant AND belongs
// to the calling user — a user must not rotate / revoke / delete
// another user's credential even within the same tenant.
func (s *Server) ownedBy(r *http.Request, tenant, user, ref string) bool {
	c, err := s.store.Get(r.Context(), tenant, ref)
	if err != nil {
		return false
	}
	return c.UserID == user
}

func (s *Server) writeStoreErr(w http.ResponseWriter, err error) {
	if errors.Is(err, credentialstore.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "credential not found")
		return
	}
	writeErr(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
}

func writeErr(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{"code": code, "message": message},
	})
}
