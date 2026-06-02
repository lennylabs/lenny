// SPDX-License-Identifier: MIT

package opsserver

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/lennylabs/lenny/pkg/ops/backup"
	"github.com/lennylabs/lenny/pkg/ops/conventions"
	"github.com/lennylabs/lenny/pkg/remediationlock"
)

// backupErrorMap maps each §25.11 canonical error code to its
// documented HTTP status and §25.2 category. A code absent from the map
// is treated as a 500 internal error.
var backupErrorMap = map[string]struct {
	status   int
	category conventions.ErrorCategory
}{
	backup.ErrCodeBackupNotFound:          {http.StatusNotFound, conventions.CategoryPermanent},
	backup.ErrCodeJobCreationFailed:       {http.StatusServiceUnavailable, conventions.CategoryTransient},
	backup.ErrCodeVerificationFailed:      {http.StatusUnprocessableEntity, conventions.CategoryPermanent},
	backup.ErrCodeRestoreIncompatible:     {http.StatusUnprocessableEntity, conventions.CategoryPermanent},
	backup.ErrCodeRestoreRequiresConfirm:  {http.StatusBadRequest, conventions.CategoryPolicy},
	backup.ErrCodeRestoreAcknowledge:      {http.StatusBadRequest, conventions.CategoryPolicy},
	backup.ErrCodeRestoreLockRequired:     {http.StatusConflict, conventions.CategoryPolicy},
	backup.ErrCodeRestoreNotFound:         {http.StatusNotFound, conventions.CategoryPermanent},
	backup.ErrCodeStorageUnreachable:      {http.StatusServiceUnavailable, conventions.CategoryTransient},
	backup.ErrCodeRemediationLockConflict: {http.StatusConflict, conventions.CategoryPolicy},
	backup.ErrCodeRestoreNotFailed:        {http.StatusConflict, conventions.CategoryPolicy},
	backup.ErrCodeJustificationRequired:   {http.StatusBadRequest, conventions.CategoryPolicy},
}

// writeBackupError maps a §25.11 BackupService error to the §25.2
// canonical error envelope and writes it. An error that is not a
// backup.Error is written as a 500.
func writeBackupError(w http.ResponseWriter, err error) {
	code := backup.CodeOf(err)
	if mapping, ok := backupErrorMap[code]; ok {
		conventions.WriteError(w, mapping.status, code, mapping.category, err.Error())
		return
	}
	conventions.WriteError(w, http.StatusInternalServerError, "INTERNAL",
		conventions.CategoryTransient, err.Error())
}

// registerBackupRoutes wires the §25.11 backup-and-restore endpoints
// onto the Server's mux. It is called by New when a BackupService is
// configured.
func (s *Server) registerBackupRoutes() {
	s.mux.HandleFunc("POST /v1/admin/backups", s.handleCreateBackup)
	s.mux.HandleFunc("GET /v1/admin/backups", s.handleListBackups)
	s.mux.HandleFunc("GET /v1/admin/backups/schedule", s.handleGetSchedule)
	s.mux.HandleFunc("PUT /v1/admin/backups/schedule", s.handleUpdateSchedule)
	s.mux.HandleFunc("GET /v1/admin/backups/policy", s.handleGetPolicy)
	s.mux.HandleFunc("PUT /v1/admin/backups/policy", s.handleUpdatePolicy)
	s.mux.HandleFunc("GET /v1/admin/backups/{id}", s.handleGetBackup)
	s.mux.HandleFunc("POST /v1/admin/backups/{id}/verify", s.handleVerifyBackup)
	s.mux.HandleFunc("GET /v1/admin/backup-jobs/{id}", s.handleGetBackupJob)
	s.mux.HandleFunc("POST /v1/admin/restore/preview", s.handleRestorePreview)
	s.mux.HandleFunc("GET /v1/admin/restore/safety-check", s.handleRestoreSafetyCheck)
	s.mux.HandleFunc("POST /v1/admin/restore/execute", s.handleRestoreExecute)
	s.mux.HandleFunc("GET /v1/admin/restore/{id}/status", s.handleRestoreStatus)
	s.mux.HandleFunc("POST /v1/admin/restore/resume", s.handleRestoreResume)
	s.mux.HandleFunc("POST /v1/admin/restore/{id}/confirm-legal-hold-ledger", s.handleConfirmLegalHoldLedger)
}

// backupUnavailable reports the §25.11 surface as unconfigured. It
// returns the spec-canonical TRANSIENT 503 BACKUP_STORAGE_UNREACHABLE
// (§25.11 Error Codes table line 4335) — the closest enumerated code
// for "backup dependency missing" when lenny-ops has no BackupService
// (deployment without Postgres or a Kubernetes connection). Using a
// catalogued code keeps the response within the spec-enumerated set
// agents can match.
func (s *Server) backupUnavailable(w http.ResponseWriter) {
	conventions.WriteError(w, http.StatusServiceUnavailable, backup.ErrCodeStorageUnreachable,
		conventions.CategoryTransient, "the backup subsystem is not configured")
}

// readJSONBody decodes the request body into v. An empty body is
// tolerated (v keeps its zero value); a malformed body is an error.
func readJSONBody(r *http.Request, v any) error {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

// callerIdentity returns the §25.11 started_by value for a mutating
// request. The §25.4 auth middleware authenticates the caller and
// attaches the verified principal, whose sub claim is authoritative.
// The X-Lenny-Caller header is only consulted when no principal is
// present (dev / embedded with no AuthConfig wired); "operator" is the
// final fallback.
func callerIdentity(r *http.Request) string {
	if p, ok := callerPrincipal(r); ok && p.Subject != "" {
		return p.Subject
	}
	if v := r.Header.Get("X-Lenny-Caller"); v != "" {
		return v
	}
	return "operator"
}

// handleCreateBackup serves POST /v1/admin/backups: trigger an
// on-demand backup.
func (s *Server) handleCreateBackup(w http.ResponseWriter, r *http.Request) {
	if s.backups == nil {
		s.backupUnavailable(w)
		return
	}
	var body struct {
		Type    string `json:"type"`
		Confirm bool   `json:"confirm"`
	}
	if err := readJSONBody(r, &body); err != nil {
		conventions.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			conventions.CategoryPermanent, "malformed request body")
		return
	}
	b, err := s.backups.CreateBackup(r.Context(), backup.BackupRequest{
		Type:       body.Type,
		Confirm:    body.Confirm,
		StartedBy:  callerIdentity(r),
		Production: s.production,
	})
	if err != nil {
		writeBackupError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, b)
}

// handleListBackups serves GET /v1/admin/backups: list backups.
func (s *Server) handleListBackups(w http.ResponseWriter, r *http.Request) {
	if s.backups == nil {
		s.backupUnavailable(w)
		return
	}
	q := r.URL.Query()
	params, err := conventions.ParsePageParams(q, "desc")
	if err != nil {
		conventions.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			conventions.CategoryPermanent, err.Error())
		return
	}
	// §25.11 lists default to 50 rather than the §25.4 generic 100.
	limit := params.Limit
	if q.Get("limit") == "" {
		limit = 50
	}
	page, err := s.backups.ListBackups(r.Context(), backup.BackupFilter{
		Type:   q.Get("type"),
		Status: q.Get("status"),
		Since:  params.Since,
		Until:  params.Until,
	}, params.Cursor, limit)
	if err != nil {
		writeBackupError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

// handleGetBackup serves GET /v1/admin/backups/{id}: backup details.
func (s *Server) handleGetBackup(w http.ResponseWriter, r *http.Request) {
	if s.backups == nil {
		s.backupUnavailable(w)
		return
	}
	b, err := s.backups.GetBackup(r.Context(), r.PathValue("id"))
	if err != nil {
		writeBackupError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, b)
}

// handleVerifyBackup serves POST /v1/admin/backups/{id}/verify: verify
// backup integrity.
func (s *Server) handleVerifyBackup(w http.ResponseWriter, r *http.Request) {
	if s.backups == nil {
		s.backupUnavailable(w)
		return
	}
	v, err := s.backups.VerifyBackup(r.Context(), r.PathValue("id"))
	if err != nil {
		writeBackupError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, v)
}

// handleGetBackupJob serves GET /v1/admin/backup-jobs/{id}: Kubernetes
// Job status for a running backup or restore operation.
func (s *Server) handleGetBackupJob(w http.ResponseWriter, r *http.Request) {
	if s.backups == nil {
		s.backupUnavailable(w)
		return
	}
	j, err := s.backups.GetJob(r.Context(), r.PathValue("id"))
	if err != nil {
		writeBackupError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, j)
}

// handleGetSchedule serves GET /v1/admin/backups/schedule.
func (s *Server) handleGetSchedule(w http.ResponseWriter, r *http.Request) {
	if s.backups == nil {
		s.backupUnavailable(w)
		return
	}
	sched, err := s.backups.GetSchedule(r.Context())
	if err != nil {
		writeBackupError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, sched)
}

// handleUpdateSchedule serves PUT /v1/admin/backups/schedule.
func (s *Server) handleUpdateSchedule(w http.ResponseWriter, r *http.Request) {
	if s.backups == nil {
		s.backupUnavailable(w)
		return
	}
	var body backup.BackupSchedule
	if err := readJSONBody(r, &body); err != nil {
		conventions.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			conventions.CategoryPermanent, "malformed request body")
		return
	}
	sched, err := s.backups.UpdateSchedule(r.Context(), body)
	if err != nil {
		writeBackupError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, sched)
}

// handleGetPolicy serves GET /v1/admin/backups/policy.
func (s *Server) handleGetPolicy(w http.ResponseWriter, r *http.Request) {
	if s.backups == nil {
		s.backupUnavailable(w)
		return
	}
	p, err := s.backups.GetPolicy(r.Context())
	if err != nil {
		writeBackupError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

// handleUpdatePolicy serves PUT /v1/admin/backups/policy.
func (s *Server) handleUpdatePolicy(w http.ResponseWriter, r *http.Request) {
	if s.backups == nil {
		s.backupUnavailable(w)
		return
	}
	var body backup.RetentionPolicy
	if err := readJSONBody(r, &body); err != nil {
		conventions.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			conventions.CategoryPermanent, "malformed request body")
		return
	}
	p, err := s.backups.UpdatePolicy(r.Context(), body)
	if err != nil {
		writeBackupError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

// handleRestorePreview serves POST /v1/admin/restore/preview: analyze
// restore impact without executing.
func (s *Server) handleRestorePreview(w http.ResponseWriter, r *http.Request) {
	if s.backups == nil {
		s.backupUnavailable(w)
		return
	}
	var body struct {
		BackupID string `json:"backupId"`
	}
	if err := readJSONBody(r, &body); err != nil {
		conventions.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			conventions.CategoryPermanent, "malformed request body")
		return
	}
	preview, err := s.backups.PreviewRestore(r.Context(), body.BackupID)
	if err != nil {
		writeBackupError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, preview)
}

// handleRestoreSafetyCheck serves GET /v1/admin/restore/safety-check:
// compare a backup against current state to estimate data loss.
func (s *Server) handleRestoreSafetyCheck(w http.ResponseWriter, r *http.Request) {
	if s.backups == nil {
		s.backupUnavailable(w)
		return
	}
	check, err := s.backups.SafetyCheckRestore(r.Context(), r.URL.Query().Get("backupId"))
	if err != nil {
		writeBackupError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, check)
}

// handleRestoreExecute serves POST /v1/admin/restore/execute: execute a
// restore. Without confirm:true the §25.4 dry-run preview is returned.
func (s *Server) handleRestoreExecute(w http.ResponseWriter, r *http.Request) {
	if s.backups == nil {
		s.backupUnavailable(w)
		return
	}
	var body struct {
		BackupID            string `json:"backupId"`
		Confirm             bool   `json:"confirm"`
		AcknowledgeDataLoss bool   `json:"acknowledgeDataLoss"`
	}
	if err := readJSONBody(r, &body); err != nil {
		conventions.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			conventions.CategoryPermanent, "malformed request body")
		return
	}
	result, err := s.backups.ExecuteRestore(r.Context(), backup.RestoreRequest{
		BackupID:            body.BackupID,
		Confirm:             body.Confirm,
		AcknowledgeDataLoss: body.AcknowledgeDataLoss,
		StartedBy:           callerIdentity(r),
	})
	if err != nil {
		writeBackupError(w, err)
		return
	}
	// A dry-run preview is a 200; an accepted restore is a 202.
	status := http.StatusAccepted
	if result.DryRun {
		status = http.StatusOK
	}
	writeJSON(w, status, result)
}

// handleRestoreStatus serves GET /v1/admin/restore/{id}/status: per-
// shard status of an in-flight or completed restore.
func (s *Server) handleRestoreStatus(w http.ResponseWriter, r *http.Request) {
	if s.backups == nil {
		s.backupUnavailable(w)
		return
	}
	state, err := s.backups.GetRestoreStatus(r.Context(), r.PathValue("id"))
	if err != nil {
		writeBackupError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, state)
}

// handleRestoreResume serves POST /v1/admin/restore/resume: resume a
// partially-completed restore.
func (s *Server) handleRestoreResume(w http.ResponseWriter, r *http.Request) {
	if s.backups == nil {
		s.backupUnavailable(w)
		return
	}
	restoreID := r.URL.Query().Get("restoreId")
	result, err := s.backups.ResumeRestore(r.Context(), restoreID)
	if err != nil {
		writeBackupError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, result)
}

// confirmLegalHoldLedgerRequest is the §25.11
// POST /v1/admin/restore/{id}/confirm-legal-hold-ledger body. The
// operator's identity is read from the X-Lenny-Caller header.
type confirmLegalHoldLedgerRequest struct {
	Justification string `json:"justification"`
}

// requirePlatformAdmin enforces the §25.11 platform-admin-only gate on
// the destructive recovery endpoints the spec narrows below the general
// §25.4 admin-API role gate. requireAdminRole admits platform-admin or
// tenant-admin on every lenny-ops endpoint; §25.11 line 3897 narrows
// confirm-legal-hold-ledger to platform-admin specifically (the same
// narrowing line 3898 applies to artifact-replication resume). It
// writes the §25.2 canonical 403 envelope and returns false when the
// caller is not a platform-admin.
//
// spec: §25.11 line 3897.
func (s *Server) requirePlatformAdmin(w http.ResponseWriter, r *http.Request) bool {
	if callerRole(r) == remediationlock.PlatformAdmin {
		return true
	}
	conventions.WriteError(w, http.StatusForbidden, "FORBIDDEN", conventions.CategoryAuth,
		"this operation requires the platform-admin role")
	return false
}

// handleConfirmLegalHoldLedger serves
// POST /v1/admin/restore/{id}/confirm-legal-hold-ledger. The endpoint
// records the §12.8 platform-admin confirmation that the legal-hold
// ledger is current after a gdpr.backup_reconcile_blocked stall. The
// synthetic watermark is persisted on the restore row so the
// post-restore reconciler accepts it as the authoritative
// ledgerLatestWriteAt on the next ResumeRestore. §25.11 line 3897
// requires the platform-admin role specifically.
func (s *Server) handleConfirmLegalHoldLedger(w http.ResponseWriter, r *http.Request) {
	if s.backups == nil {
		s.backupUnavailable(w)
		return
	}
	if !s.requirePlatformAdmin(w, r) {
		return
	}
	var body confirmLegalHoldLedgerRequest
	if err := readJSONBody(r, &body); err != nil {
		conventions.WriteError(w, http.StatusBadRequest, "BAD_REQUEST",
			conventions.CategoryPermanent, "decode body: "+err.Error())
		return
	}
	state, err := s.backups.ConfirmLegalHoldLedger(r.Context(),
		r.PathValue("id"), body.Justification, callerIdentity(r))
	if err != nil {
		writeBackupError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, state)
}
