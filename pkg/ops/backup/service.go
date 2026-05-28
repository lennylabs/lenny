// SPDX-License-Identifier: MIT

package backup

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/lennylabs/lenny/pkg/backup/retention"
)

// §25.11 backup statuses recorded in the ops_backups row.
const (
	StatusPending            = "pending"
	StatusRunning            = "running"
	StatusCompleted          = "completed"
	StatusFailed             = "failed"
	StatusVerifying          = "verifying"
	StatusVerified           = "verified"
	StatusVerificationFailed = "verification_failed"
	StatusExpired            = "expired"
)

// §25.11 restore statuses recorded in the ops_restore_state row.
const (
	RestoreStatusRunning   = "running"
	RestoreStatusCompleted = "completed"
	RestoreStatusFailed    = "failed"
	RestoreStatusPaused    = "paused"
)

// §25.11 canonical error codes the BackupService returns. The HTTP
// layer maps each to its documented status code and category.
const (
	ErrCodeBackupNotFound          = "BACKUP_NOT_FOUND"
	ErrCodeJobCreationFailed       = "BACKUP_JOB_CREATION_FAILED"
	ErrCodeVerificationFailed      = "BACKUP_VERIFICATION_FAILED"
	ErrCodeRestoreIncompatible     = "RESTORE_INCOMPATIBLE"
	ErrCodeRestoreRequiresConfirm  = "RESTORE_REQUIRES_CONFIRM"
	ErrCodeRestoreAcknowledge      = "RESTORE_ACKNOWLEDGE_REQUIRED"
	ErrCodeRestoreLockRequired     = "RESTORE_LOCK_REQUIRED"
	ErrCodeRestoreNotFound         = "RESTORE_NOT_FOUND"
	ErrCodeStorageUnreachable      = "BACKUP_STORAGE_UNREACHABLE"
	ErrCodeRemediationLockConflict = "REMEDIATION_LOCK_CONFLICT"
	// ErrCodeRestoreNotFailed is returned when an operator invokes a
	// recovery action (ConfirmLegalHoldLedger) against a restore that
	// is not in the failed state. The endpoint is meant to recover from
	// a §12.8 backup_reconcile_blocked block; a running or completed
	// restore has nothing to confirm.
	ErrCodeRestoreNotFailed = "RESTORE_NOT_FAILED"
	// ErrCodeJustificationRequired is returned when an operator-driven
	// confirmation endpoint is called without a justification string.
	// §25.11 requires the operator's justification on every audited
	// override.
	ErrCodeJustificationRequired = "JUSTIFICATION_REQUIRED"
)

// scheduler is the §25.11 backup started_by value for a backup the
// leader-elected cron evaluator created rather than an admin caller.
const scheduler = "scheduler"

// Error is a §25.11 backup/restore failure carrying the canonical error
// code so the HTTP handler maps it to the documented status code
// without re-classifying the failure.
type Error struct {
	// Code is one of the §25.11 ErrCode* constants.
	Code string
	// Message is the human-readable detail.
	Message string
}

// Error implements error.
func (e *Error) Error() string {
	if e.Message == "" {
		return e.Code
	}
	return e.Code + ": " + e.Message
}

// codedError builds an *Error.
func codedError(code, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// CodeOf returns the §25.11 canonical error code carried by err, or the
// empty string when err is not a backup *Error.
func CodeOf(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

// Backup is the §25.11 backup record. The JSON tags match the §25.11
// Response Types block verbatim so the HTTP handler marshals it
// directly.
type Backup struct {
	ID          string            `json:"id"`
	Type        string            `json:"type"`
	Status      string            `json:"status"`
	StartedAt   time.Time         `json:"startedAt"`
	CompletedAt *time.Time        `json:"completedAt,omitempty"`
	SizeBytes   int64             `json:"sizeBytes,omitempty"`
	Duration    string            `json:"duration,omitempty"`
	StoragePath string            `json:"storagePath"`
	Checksum    string            `json:"checksum"`
	Components  []BackupComponent `json:"components"`
	StartedBy   string            `json:"startedBy"`
	OperationID string            `json:"operationId,omitempty"`
	JobID       string            `json:"jobId"`
	Error       string            `json:"error,omitempty"`

	// PlatformVersion and SchemaVersion are recorded for restore
	// compatibility checks (the ops_backups columns of the same name).
	PlatformVersion string     `json:"platformVersion,omitempty"`
	SchemaVersion   int        `json:"schemaVersion,omitempty"`
	ExpiresAt       *time.Time `json:"expiresAt,omitempty"`
}

// BackupComponent is one element of a backup: the §25.11 Response Types
// BackupComponent.
type BackupComponent struct {
	Name      string `json:"name"`
	Status    string `json:"status"`
	SizeBytes int64  `json:"sizeBytes"`
}

// retentionKind maps a backup record to its pkg/backup/retention Kind
// so the retention planner classifies it correctly. A pre-restore
// backup is a full backup tagged "pre-restore"; it is retained on the
// shorter preRestoreRetainDays schedule.
func (b Backup) retentionKind() retention.Kind {
	switch Type(b.Type) {
	case TypePreRestore:
		return retention.KindPreRestore
	case TypePostgres:
		return retention.KindPostgres
	default:
		return retention.KindFull
	}
}

// BackupRequest is the body of POST /v1/admin/backups.
type BackupRequest struct {
	// Type is one of the §25.11 backup types: full, postgres, or config.
	Type string `json:"type"`
	// Confirm is the §25.4 dry-run/confirm gate. A full backup in
	// production requires confirm:true.
	Confirm bool `json:"confirm"`
	// StartedBy identifies the caller (a service account, or "scheduler"
	// for a cron-driven backup). The HTTP handler fills it from the
	// authenticated identity.
	StartedBy string `json:"-"`
	// OperationID correlates the backup with a §25.2 operation envelope.
	OperationID string `json:"-"`
	// Production reports whether this deployment is production, which
	// gates the confirm requirement for a full backup.
	Production bool `json:"-"`
}

// BackupFilter is the GET /v1/admin/backups query filter.
type BackupFilter struct {
	// Type, when non-empty, restricts the listing to one backup type.
	Type string
	// Status, when non-empty, restricts the listing to one status.
	Status string
	// Since and Until bound the listing by started_at; a zero value
	// means the bound is not applied.
	Since time.Time
	Until time.Time
}

// BackupPage is the GET /v1/admin/backups response: a page of backups
// and the cursor for the next page.
type BackupPage struct {
	Backups    []Backup `json:"backups"`
	NextCursor string   `json:"nextCursor,omitempty"`
	HasMore    bool     `json:"hasMore"`
}

// BackupVerification is the POST /v1/admin/backups/{id}/verify result.
type BackupVerification struct {
	BackupID    string `json:"backupId"`
	JobID       string `json:"jobId"`
	Status      string `json:"status"`
	ChecksumOK  bool   `json:"checksumOk"`
	ArchiveOK   bool   `json:"archiveOk"`
	TestRestore bool   `json:"testRestore"`
	Detail      string `json:"detail,omitempty"`
}

// BackupJob is the GET /v1/admin/backup-jobs/{id} response: the
// Kubernetes Job status for a running backup or restore operation.
type BackupJob struct {
	JobID     string `json:"jobId"`
	BackupID  string `json:"backupId,omitempty"`
	Phase     string `json:"phase"`
	Active    int    `json:"active"`
	Succeeded int    `json:"succeeded"`
	Failed    int    `json:"failed"`
}

// BackupSchedule is the §25.11 backup schedule served and updated by
// the schedule endpoints. The JSON tags match the PUT body verbatim.
type BackupSchedule struct {
	Full     string `json:"full"`
	Postgres string `json:"postgres"`
	Enabled  bool   `json:"enabled"`
}

// schedule converts a BackupSchedule to the request-validation Schedule
// the package's ValidateSchedule operates on.
func (s BackupSchedule) schedule() Schedule {
	return Schedule{Full: s.Full, Postgres: s.Postgres, Enabled: s.Enabled}
}

// RestoreRequest is the body of POST /v1/admin/restore/execute.
type RestoreRequest struct {
	BackupID string `json:"backupId"`
	// Confirm gates the destructive restore: without it the endpoint
	// returns a dry-run preview.
	Confirm bool `json:"confirm"`
	// AcknowledgeDataLoss is required when the safety check reports the
	// restore is not safe (data written since the backup would be lost).
	AcknowledgeDataLoss bool `json:"acknowledgeDataLoss"`
	// StartedBy identifies the caller.
	StartedBy string `json:"-"`
	// OperationID correlates the restore with a §25.2 operation envelope.
	OperationID string `json:"-"`
}

// RestorePreview is the §25.11 Response Types RestorePreview: the
// analysis POST /v1/admin/restore/preview returns.
type RestorePreview struct {
	BackupID                      string   `json:"backupId"`
	BackupVersion                 string   `json:"backupVersion"`
	CurrentVersion                string   `json:"currentVersion"`
	Compatible                    bool     `json:"compatible"`
	IncompatibleReason            string   `json:"incompatibleReason,omitempty"`
	AffectedResources             []string `json:"affectedResources"`
	EstimatedDowntime             string   `json:"estimatedDowntime"`
	RequiresFullStop              bool     `json:"requiresFullStop"`
	Warnings                      []string `json:"warnings"`
	ArtifactReplicationLagSeconds int      `json:"artifactReplicationLagSeconds"`
	EstimatedOrphanArtifactRows   int      `json:"estimatedOrphanArtifactRows"`
}

// RestoreSafetyCheck is the GET /v1/admin/restore/safety-check
// response: the comparison of a backup against current state.
type RestoreSafetyCheck struct {
	BackupID          string           `json:"backupId"`
	BackupTakenAt     time.Time        `json:"backupTakenAt"`
	CurrentTime       time.Time        `json:"currentTime"`
	Safe              bool             `json:"safe"`
	DataLossEstimate  DataLossEstimate `json:"dataLossEstimate"`
	Compatibility     RestoreCompat    `json:"compatibility"`
	RecommendedAction string           `json:"recommendedAction"`
}

// DataLossEstimate is the §25.11 safety-check data-loss estimate.
type DataLossEstimate struct {
	MutationsSinceBackup int      `json:"mutationsSinceBackup"`
	SessionsAffected     int      `json:"sessionsAffected"`
	AuditEventsLost      int      `json:"auditEventsLost"`
	TablesWithDivergence []string `json:"tablesWithDivergence"`
}

// RestoreCompat is the §25.11 safety-check compatibility block.
type RestoreCompat struct {
	BackupSchemaVersion     int      `json:"backupSchemaVersion"`
	CurrentSchemaVersion    int      `json:"currentSchemaVersion"`
	BackupPlatformVersion   string   `json:"backupPlatformVersion"`
	CurrentPlatformVersion  string   `json:"currentPlatformVersion"`
	SchemaMigrationsBetween []string `json:"schemaMigrationsBetween"`
	Compatible              bool     `json:"compatible"`
	Warnings                []string `json:"warnings"`
}

// RestoreResult is the outcome of POST /v1/admin/restore/execute and
// /restore/resume.
type RestoreResult struct {
	RestoreID          string          `json:"restoreId"`
	BackupID           string          `json:"backupId"`
	Status             string          `json:"status"`
	JobID              string          `json:"jobId"`
	PreRestoreBackupID string          `json:"preRestoreBackupId"`
	DryRun             bool            `json:"dryRun,omitempty"`
	Preview            *RestorePreview `json:"preview,omitempty"`
}

// RestoreState is the §25.11 ops_restore_state row: the per-shard
// status GET /v1/admin/restore/{id}/status returns.
type RestoreState struct {
	ID                 string                `json:"id"`
	BackupID           string                `json:"backupId"`
	StartedAt          time.Time             `json:"startedAt"`
	CompletedAt        *time.Time            `json:"completedAt,omitempty"`
	Status             string                `json:"status"`
	ShardStates        map[string]ShardState `json:"shardStates"`
	StartedBy          string                `json:"startedBy"`
	OperationID        string                `json:"operationId,omitempty"`
	PreRestoreBackupID string                `json:"preRestoreBackupId"`
	FailedShard        string                `json:"failedShard,omitempty"`
	Error              string                `json:"error,omitempty"`

	// LedgerConfirmedAt is the §12.8 post-restore reconciler ledger
	// freshness watermark recorded by ConfirmLegalHoldLedger. When set,
	// the reconciler treats this timestamp as the authoritative
	// ledgerLatestWriteAt on retry. The freshness gate previously fired
	// because ledgerLatestWriteAt <= backupTakenAt; the operator
	// confirmation injects a synthetic watermark that the reconciler
	// accepts in lieu of the auto-derived one.
	LedgerConfirmedAt *time.Time `json:"ledgerConfirmedAt,omitempty"`
	// LedgerConfirmedBy is the platform-admin operator who confirmed
	// the ledger is current. Recorded for audit linkage.
	LedgerConfirmedBy string `json:"ledgerConfirmedBy,omitempty"`
	// LedgerConfirmedJustification carries the operator's justification
	// for the confirmation, kept on the row alongside the audit event.
	LedgerConfirmedJustification string `json:"ledgerConfirmedJustification,omitempty"`
}

// ShardState is one shard's restore status within a RestoreState.
type ShardState struct {
	Status      string     `json:"status"`
	StartedAt   *time.Time `json:"startedAt,omitempty"`
	CompletedAt *time.Time `json:"completedAt,omitempty"`
	Error       string     `json:"error,omitempty"`
}

// BackupService is the §25.11 Backup and Restore API surface. The
// interface is reproduced from the spec's Go Interface block; Service
// is the production implementation.
type BackupService interface {
	CreateBackup(ctx context.Context, req BackupRequest) (*Backup, error)
	ListBackups(ctx context.Context, filter BackupFilter, cursor string, limit int) (*BackupPage, error)
	GetBackup(ctx context.Context, id string) (*Backup, error)
	VerifyBackup(ctx context.Context, id string) (*BackupVerification, error)
	GetJob(ctx context.Context, jobID string) (*BackupJob, error)
	GetSchedule(ctx context.Context) (*BackupSchedule, error)
	UpdateSchedule(ctx context.Context, schedule BackupSchedule) (*BackupSchedule, error)
	GetPolicy(ctx context.Context) (*RetentionPolicy, error)
	UpdatePolicy(ctx context.Context, policy RetentionPolicy) (*RetentionPolicy, error)
	PreviewRestore(ctx context.Context, backupID string) (*RestorePreview, error)
	SafetyCheckRestore(ctx context.Context, backupID string) (*RestoreSafetyCheck, error)
	ExecuteRestore(ctx context.Context, req RestoreRequest) (*RestoreResult, error)
	GetRestoreStatus(ctx context.Context, restoreID string) (*RestoreState, error)
	ResumeRestore(ctx context.Context, restoreID string) (*RestoreResult, error)
	// ConfirmLegalHoldLedger records the §12.8 / §25.11 platform-admin
	// confirmation that the legal-hold ledger is current after a
	// gdpr.backup_reconcile_blocked stall (ledger restored in lockstep,
	// ledgerLatestWriteAt <= backupTakenAt). The synthetic watermark is
	// persisted on the restore row so the reconciler accepts it as the
	// authoritative ledgerLatestWriteAt on the next ResumeRestore. The
	// caller's identity and justification are recorded; a separate audit
	// emission records legal_hold.ledger_confirmed_current_at.
	ConfirmLegalHoldLedger(ctx context.Context, restoreID, justification, caller string) (*RestoreState, error)
}
