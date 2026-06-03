// SPDX-License-Identifier: MIT

// Package backup validates the §25.11 backup-and-restore API requests:
// the backup type, the schedule's cron expressions, and the retention
// policy. The BackupService that runs backups as Kubernetes Jobs uses
// these validators so a malformed request is rejected before any Job
// is created.
package backup

import (
	"errors"
	"fmt"

	"github.com/lennylabs/lenny/pkg/cron"
)

// Type is a §25.11 backup type.
type Type string

const (
	TypeFull       Type = "full"
	TypePostgres   Type = "postgres"
	TypeConfig     Type = "config"
	TypePreRestore Type = "pre-restore"
)

// ValidType reports whether s names one of the §25.11 backup types,
// including the "pre-restore" variant produced by the restore-execute
// lifecycle (§25.11 Pre-Restore Backup Lifecycle).
func ValidType(s string) bool {
	switch Type(s) {
	case TypeFull, TypePostgres, TypeConfig, TypePreRestore:
		return true
	default:
		return false
	}
}

// RequiresConfirm reports whether the §25.4 dry-run/confirm gate
// applies: §25.11 requires confirm:true for a full backup in
// production.
func RequiresConfirm(t Type, production bool) bool {
	return t == TypeFull && production
}

// CreatePreview is the §25.2 dry-run preview body POST /v1/admin/backups
// returns when a confirm-gated backup is requested without confirm:true.
// It mirrors the canonical preview object (resourcesAffected,
// estimatedDowntime, warnings) so an agent can inspect the blast radius
// before re-issuing with confirm. spec: §25.2 lines 287-300.
type CreatePreview struct {
	ResourcesAffected []string `json:"resourcesAffected"`
	EstimatedDowntime string   `json:"estimatedDowntime"`
	Warnings          []string `json:"warnings"`
}

// PreviewBackup builds the §25.2 dry-run preview for an on-demand backup
// of type t. A backup runs online against live stores, so the estimated
// downtime is zero; the affected resources are the components the backup
// would dump. spec: §25.2 lines 287-300, §25.11 line 3883.
func PreviewBackup(t Type) CreatePreview {
	comps := componentsFor(t)
	resources := make([]string, len(comps))
	for i, c := range comps {
		resources[i] = c.Name
	}
	return CreatePreview{
		ResourcesAffected: resources,
		EstimatedDowntime: "0s",
		Warnings: []string{
			"This triggers a " + string(t) + " backup. Re-run with confirm:true to apply.",
		},
	}
}

// Schedule is the §25.11 backup schedule: cron expressions for the full
// and postgres backups and whether scheduled backups run.
type Schedule struct {
	Full     string `json:"full"`
	Postgres string `json:"postgres"`
	Enabled  bool   `json:"enabled"`
}

// ValidateSchedule checks that each non-empty cron expression parses.
func ValidateSchedule(s Schedule) error {
	if s.Full != "" {
		if _, err := cron.Parse(s.Full); err != nil {
			return fmt.Errorf("full backup schedule %q: %w", s.Full, err)
		}
	}
	if s.Postgres != "" {
		if _, err := cron.Parse(s.Postgres); err != nil {
			return fmt.Errorf("postgres backup schedule %q: %w", s.Postgres, err)
		}
	}
	return nil
}

// RetentionPolicy is the §25.11 backup retention policy.
type RetentionPolicy struct {
	RetainDays    int `json:"retainDays"`
	RetainCount   int `json:"retainCount"`
	RetainMinFull int `json:"retainMinFull"`
}

// ValidateRetentionPolicy rejects a policy with a negative bound or one
// that would retain no backups at all.
func ValidateRetentionPolicy(p RetentionPolicy) error {
	if p.RetainDays < 0 || p.RetainCount < 0 || p.RetainMinFull < 0 {
		return errors.New("retention bounds must not be negative")
	}
	if p.RetainDays == 0 && p.RetainCount == 0 {
		return errors.New("retention policy must keep backups by day or by count")
	}
	return nil
}
