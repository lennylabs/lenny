// SPDX-License-Identifier: MIT

// Package retention implements the §25 lenny-ops backup retention
// policy: given a set of backups and the configured policy, it
// partitions them into the backups to keep and the backups to prune.
// The package is pure — no storage or scheduling I/O — so the
// lenny-ops backup reconciler and its tests share one implementation.
package retention

import (
	"sort"
	"time"
)

// Kind classifies a backup for retention purposes.
type Kind string

const (
	// KindFull is a full platform backup.
	KindFull Kind = "full"
	// KindPostgres is a Postgres-only snapshot.
	KindPostgres Kind = "postgres"
	// KindPreRestore is a pre-restore safety backup, retained on the
	// shorter preRestoreRetainDays schedule.
	KindPreRestore Kind = "pre-restore"
)

// Backup is one backup record evaluated by the retention policy.
type Backup struct {
	ID        string
	Kind      Kind
	CreatedAt time.Time
}

// Policy is the §25 backups.retention configuration.
type Policy struct {
	// RetainDays prunes a regular backup older than this many days.
	RetainDays int
	// RetainCount keeps at most this many regular backups.
	RetainCount int
	// RetainMinFull always keeps at least this many full backups,
	// regardless of age or count.
	RetainMinFull int
	// PreRestoreRetainDays prunes a pre-restore backup older than this
	// many days. Pre-restore backups are cleaned aggressively and are
	// not subject to RetainCount or RetainMinFull.
	PreRestoreRetainDays int
}

// Plan partitions backups into the set to keep and the set to prune
// per the §25 retention policy, evaluated as of now. A regular backup
// (full or postgres) is kept when it is both within RetainDays and
// among the RetainCount newest regular backups; RetainMinFull then
// keeps the newest full backups that the base rules would otherwise
// prune. A pre-restore backup is kept only while it is within
// PreRestoreRetainDays. keep and prune are each ordered newest-first.
func Plan(backups []Backup, policy Policy, now time.Time) (keep, prune []Backup) {
	ordered := make([]Backup, len(backups))
	copy(ordered, backups)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].CreatedAt.Equal(ordered[j].CreatedAt) {
			return ordered[i].ID < ordered[j].ID
		}
		return ordered[i].CreatedAt.After(ordered[j].CreatedAt)
	})

	regularKept := 0
	fullKept := 0
	for _, b := range ordered {
		if b.Kind == KindPreRestore {
			if withinDays(b.CreatedAt, policy.PreRestoreRetainDays, now) {
				keep = append(keep, b)
			} else {
				prune = append(prune, b)
			}
			continue
		}
		withinCount := regularKept < policy.RetainCount
		withinAge := withinDays(b.CreatedAt, policy.RetainDays, now)
		if withinCount && withinAge {
			keep = append(keep, b)
			regularKept++
			if b.Kind == KindFull {
				fullKept++
			}
			continue
		}
		// The base rules would prune b; the minimum-full floor still
		// keeps a full backup the platform must be able to restore from.
		if b.Kind == KindFull && fullKept < policy.RetainMinFull {
			keep = append(keep, b)
			fullKept++
			continue
		}
		prune = append(prune, b)
	}
	return keep, prune
}

// withinDays reports whether t is no older than days days before now.
func withinDays(t time.Time, days int, now time.Time) bool {
	return !t.Before(now.AddDate(0, 0, -days))
}
