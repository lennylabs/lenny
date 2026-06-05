// SPDX-License-Identifier: MIT

package main

import (
	"testing"

	"github.com/lennylabs/lenny/pkg/observability/audit"
	"github.com/lennylabs/lenny/pkg/ops/backup"
)

// spec: §25.11 line 4343, §16.7 — with no durable audit store (a nil
// pool) the Job degrades to the log-only sink rather than dropping the
// terminal-state events silently or failing the run.
func TestBuildBackupAuditSinkNilPoolDegradesToLog(t *testing.T) {
	sink := buildBackupAuditSink(nil)
	if sink == nil {
		t.Fatal("buildBackupAuditSink(nil) returned a nil sink; want the log-only fallback")
	}
	// The degraded sink must accept every terminal-state event without
	// panicking; it logs and returns.
	for _, ev := range []backup.AuditEvent{
		{Type: string(audit.EventBackupCompleted), BackupID: "bkp-1", Outcome: "success"},
		{Type: string(audit.EventBackupFailed), BackupID: "bkp-2", Outcome: "failed", Detail: "shard down"},
		{Type: string(audit.EventBackupVerified), BackupID: "bkp-3"},
	} {
		sink(ev)
	}
}
