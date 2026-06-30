// SPDX-License-Identifier: MIT

package sessionserver

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	pkgauth "github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/blobstore/artifactcatalog"
	"github.com/lennylabs/lenny/pkg/gateway/billingstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// isDeriveFailureRow reports whether row is a §7.1 derive-failure audit
// row (terminal `failed`, failureClass `derive_failure`). Per §15.1 line
// 661 these rows never materialised a workspace, transcript, logs, tree,
// usage data, or delivered messages, so every session subresource GET
// returns 404 RESOURCE_NOT_FOUND even though GET /v1/sessions/{id} itself
// returns 200 with the audit envelope. spec: §15.1 line 661; §7.1 derive
// rule 2. F-15.1.3.
func isDeriveFailureRow(row sessionstore.Session) bool {
	return row.State == session.StateFailed &&
		row.FailureClass == session.FailureClassDeriveFailure
}

// artifactPayload is the §15.1 GET /v1/sessions/{id}/artifacts wire row.
// It surfaces the §12.5 catalog fields a client needs to discover and
// dereference a session artifact (the `ref` is the `lenny-blob://` URI
// the §15.1 GET /v1/blobs/{ref} handler resolves). spec: §15.1; §12.5
// line 309. F-15.2.3 / F-15.1.3.
type artifactPayload struct {
	Ref       string `json:"ref"`
	PartID    string `json:"partId,omitempty"`
	Type      string `json:"type"`
	MimeType  string `json:"mimeType,omitempty"`
	SizeBytes int64  `json:"sizeBytes"`
	LegalHold bool   `json:"legalHold,omitempty"`
	CreatedAt string `json:"createdAt,omitempty"`
}

// handleListArtifacts implements GET /v1/sessions/{id}/artifacts per
// §15.1: the live §12.5 artifact catalog rows for one session. The
// listing is scoped to the caller's resolved tenant; soft-deleted and
// tombstoned rows are excluded so a client sees only dereferenceable
// artifacts. A session with no catalog wired or no rows returns an empty
// `{items: []}` envelope rather than an error. spec: §15.1 line 598;
// §12.5. F-15.2.3 / F-15.1.3.
func (s *Server) handleListArtifacts(w http.ResponseWriter, r *http.Request) {
	tenantID := s.resolveTenant(r)
	id := r.PathValue("id")

	// Confirm the session exists and is visible to the caller's tenant
	// before exposing its artifacts, mirroring the GET /v1/sessions/{id}
	// 404 contract so a cross-tenant probe cannot enumerate artifacts.
	row, err := s.store.Get(r.Context(), tenantID, id)
	if err != nil {
		if errors.Is(err, sessionstore.ErrNotFound) {
			s.writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "session not found", nil)
			return
		}
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	// spec: §15.1 line 661 — a derive_failure audit row never materialised
	// a workspace, so it owns no artifacts; the subresource GET 404s.
	if isDeriveFailureRow(row) {
		s.writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "session has no artifacts", nil)
		return
	}

	items := []artifactPayload{}
	if s.artifacts != nil {
		rows, err := s.artifacts.ListBySession(r.Context(), tenantID, id)
		if err != nil {
			s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
			return
		}
		for _, row := range rows {
			if row.State != artifactcatalog.StateLive {
				continue
			}
			items = append(items, toArtifactPayload(row))
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{"items": items})
}

// toArtifactPayload projects a §12.5 catalog row onto the wire row,
// defaulting an empty artifact-type tag to `workspace` (the migration
// 0063 default).
func toArtifactPayload(row artifactcatalog.Record) artifactPayload {
	kind := string(row.ArtifactType)
	if kind == "" {
		kind = string(artifactcatalog.ArtifactTypeWorkspace)
	}
	p := artifactPayload{
		Ref:       row.URI,
		PartID:    row.PartID,
		Type:      kind,
		MimeType:  row.MimeType,
		SizeBytes: row.SizeBytes,
		LegalHold: row.LegalHold,
	}
	if !row.CreatedAt.IsZero() {
		p.CreatedAt = row.CreatedAt.UTC().Format(time.RFC3339Nano)
	}
	return p
}

// handleSessionUsage implements GET /v1/sessions/{id}/usage per §15.1:
// the reconciled token + compute total drawn from the §11.2.1 billing
// ledger. Per §15.1 line 676 the total is tree-aggregated — when the
// session has a §8 delegation subtree the report folds in every
// descendant task's usage, not just this session's own ledger rows. A
// leaf session's subtree is itself, so the single-session total is
// unchanged. It requires the §10.2 view_usage permission (the same gate
// the tenant /v1/usage and /v1/metering/events reports run). A session
// with no billing events, or a gateway with metering disabled, returns a
// zero-valued report rather than an error. spec: §15.1 lines 676, 661;
// §11.2.1; §10.2 view_usage. F-15.1.31 / F-15.2.3 / F-15.1.3.
func (s *Server) handleSessionUsage(w http.ResponseWriter, r *http.Request) {
	principal, ok := getPrincipal(r)
	if ok && !pkgauth.RolesGrant(principal.Roles, pkgauth.PermViewUsage) {
		s.writeError(w, http.StatusForbidden, "FORBIDDEN",
			"session usage requires the view_usage permission", nil)
		return
	}

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
	// spec: §15.1 line 661 — a derive_failure audit row never accrued any
	// usage; the subresource GET 404s rather than reporting a zero total.
	if isDeriveFailureRow(row) {
		s.writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "session has no usage data", nil)
		return
	}

	// spec: §15.1 line 676 — sum the §11.2.1 ledger totals across the
	// session and every descendant in its delegation subtree. The subtree
	// is {id} for a leaf, so a non-delegating session reports its own
	// total unchanged. F-15.1.31.
	usage := billingstore.SessionUsage{}
	treeAggregated := false
	if s.billing != nil {
		ids := s.subtreeSessionIDs(r.Context(), tenantID, row)
		treeAggregated = len(ids) > 1
		for _, sid := range ids {
			u, err := s.billing.SessionTotals(r.Context(), tenantID, sid)
			if err != nil {
				s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
				return
			}
			usage.TokensInput += u.TokensInput
			usage.TokensOutput += u.TokensOutput
			usage.PodMinutes += u.PodMinutes
			usage.EventCount += u.EventCount
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"sessionId":      id,
		"tokensInput":    usage.TokensInput,
		"tokensOutput":   usage.TokensOutput,
		"podMinutes":     usage.PodMinutes,
		"eventCount":     usage.EventCount,
		"treeAggregated": treeAggregated,
	})
}

// handleSetupOutput implements GET /v1/sessions/{id}/setup-output per
// §15.1 (line 674): the §7.5 line 475 captured stdout/stderr/exit for
// each setup command the adapter ran, plus the §7.5 line 488 synthetic
// rejection-reason entries the gateway recorded when it rejected a
// command at admission. The same records already ride the single-session
// GET /v1/sessions/{id} envelope as `setupOutput`; this dedicated
// endpoint serves clients that want the setup output without fetching the
// whole session detail. The listing is tenant-scoped behind the same
// 404 contract as the session read, so a cross-tenant probe cannot
// confirm a foreign session id. A session that ran no setup commands and
// had none rejected returns an empty `{items: []}` envelope, mirroring
// GET /v1/sessions/{id}/artifacts. spec: §15.1 line 674; §7.5 lines 475,
// 488. F-15.1.3 / F-7.5.4 / F-7.5.11.
func (s *Server) handleSetupOutput(w http.ResponseWriter, r *http.Request) {
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
	// spec: §15.1 line 661 — a derive_failure audit row never ran setup,
	// so the subresource GET 404s rather than returning an empty list. A
	// normal session that ran no setup commands still returns 200 {items:
	// []}; the distinguishing factor is the derive_failure audit class.
	if isDeriveFailureRow(row) {
		s.writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "session has no setup output", nil)
		return
	}

	items := make([]SetupOutputEntry, 0, len(row.SetupOutput))
	for _, e := range row.SetupOutput {
		items = append(items, SetupOutputEntry{
			Cmd:             e.Cmd,
			ExitCode:        e.ExitCode,
			Stdout:          e.Stdout,
			Stderr:          e.Stderr,
			DurationMs:      e.DurationMs,
			Truncated:       e.Truncated,
			Rejected:        e.Rejected,
			RejectionReason: e.RejectionReason,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{"items": items})
}

// handleWorkspace implements GET /v1/sessions/{id}/workspace per §15.1
// (line 671): it streams the session's workspace snapshot tar.gz from
// the §4.5 object store. The snapshot ref is resolved from the session
// row (`WorkspaceSnapshot.Ref`), then read through the tenant-scoped
// blob-store view so a caller cannot reach a snapshot outside its own
// tenant. The §15.1 line 661 contract is honoured directly: a session
// that never produced a snapshot — including a failed-derive row that is
// terminal from birth — returns 404 RESOURCE_NOT_FOUND because no
// workspace ever existed. The bytes are streamed with the stored MIME
// type and a `Content-Disposition` naming the archive after the session.
// spec: §15.1 lines 671, 661; §4.5 line 311 (content-addressed snapshot).
// F-15.1.3.
func (s *Server) handleWorkspace(w http.ResponseWriter, r *http.Request) {
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

	// spec: §15.1 line 661 — a derive_failure audit row never materialised
	// a workspace; the download 404s. (The no-snapshot guard below also
	// covers this, but the explicit class check keeps the contract legible
	// and holds even if a stale ref were ever attached to such a row.)
	if isDeriveFailureRow(row) {
		s.writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND",
			"session has no workspace snapshot", nil)
		return
	}

	// spec: §15.1 line 661 — no snapshot ever written (never-ran or
	// failed-derive row) is a 404, not an empty stream.
	if row.WorkspaceSnapshot == nil || row.WorkspaceSnapshot.Ref == "" {
		s.writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND",
			"session has no workspace snapshot", nil)
		return
	}
	if s.blobs == nil {
		s.writeError(w, http.StatusServiceUnavailable, "BLOBSTORE_UNAVAILABLE",
			"gateway has no blob store configured", nil)
		return
	}

	uri, ok := parseSnapshotRef(row.WorkspaceSnapshot.Ref, tenantID)
	if !ok {
		s.writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND",
			"session has no resolvable workspace snapshot", nil)
		return
	}

	info, body, err := blobstore.NewTenantScoped(tenantID, s.blobs).Get(uri)
	if err != nil {
		if errors.Is(err, blobstore.ErrCrossTenant) {
			s.writeError(w, http.StatusForbidden, "FORBIDDEN",
				"caller has no read access to this workspace snapshot", nil)
			return
		}
		if errors.Is(err, blobstore.ErrNotFound) {
			s.writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND",
				"workspace snapshot not found or expired", nil)
			return
		}
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	defer body.Close()

	mime := info.MimeType
	if mime == "" {
		mime = "application/gzip"
	}
	w.Header().Set("Content-Type", mime)
	w.Header().Set("Content-Disposition", `attachment; filename="`+id+`-workspace.tar.gz"`)
	if hash := row.WorkspaceSnapshot.ContentHash; hash != "" {
		w.Header().Set("X-Lenny-Workspace-Content-Hash", hash)
	}
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, body)
}

// parseSnapshotRef resolves a stored workspace-snapshot reference into a
// blob-store URI, accepting both the `lenny-blob://` URI form and the
// path-style object key (`/{tenant}/{objectType}/{session}/{part}`) the
// gateway records for sealed and derived snapshots. The resolved URI's
// tenant must match the caller's tenant; a mismatch returns ok=false so
// the handler fails the read closed before touching the store. spec:
// §4.5 MinIO path `/{tenant_id}/{object_type}/{session_id}/...`.
func parseSnapshotRef(ref, tenantID string) (blobstore.URI, bool) {
	if strings.HasPrefix(ref, blobstore.Scheme+"://") {
		uri, err := blobstore.ParseURI(ref)
		if err != nil {
			return blobstore.URI{}, false
		}
		if uri.TenantID != tenantID {
			return blobstore.URI{}, false
		}
		return uri, true
	}
	parts := strings.SplitN(strings.TrimPrefix(ref, "/"), "/", 4)
	if len(parts) < 4 || parts[0] != tenantID {
		return blobstore.URI{}, false
	}
	return blobstore.URI{
		TenantID:   parts[0],
		ObjectType: blobstore.ObjectType(parts[1]),
		SessionID:  parts[2],
		PartID:     parts[3],
	}, true
}
