// SPDX-License-Identifier: MIT

package main

import (
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/pkg/gateway/auditstore"
	"github.com/lennylabs/lenny/pkg/ops/backup"
	"github.com/lennylabs/lenny/pkg/ops/opsaudit"
	"github.com/lennylabs/lenny/pkg/storerouter"
)

// buildBackupAuditSink builds the §16.7 backup terminal-state audit sink
// the lenny-backup Job pod writes through. The backup lifecycle's
// completed / failed / verified transitions happen in this Job rather
// than in the lenny-ops Service, so the durable §11.7 audit rows are
// committed here under the platform tenant, routed through the §12.3
// R-03 AuditShard() the gateway and lenny-ops also use. A nil pool — or
// a router that cannot be built — degrades to a log-only sink so the
// events stay observable and a degraded audit backend never halts the
// backup run.
//
// spec: §25.11 line 4343 (every backup transition is audited);
// §11.7 line 435 (platform-scoped audit rows route to the platform
// tenant); §16.7 (backup.completed, backup.failed, backup.verified).
func buildBackupAuditSink(pool *pgxpool.Pool) backup.AuditSink {
	if pool == nil {
		return logBackupAuditSink
	}
	router, err := storerouter.NewSingleShardRouter(storerouter.Config{Postgres: pool})
	if err != nil {
		log.Printf("lenny-backup: §11.7 platform audit: store router unavailable (%v); audit events logged only", err)
		return logBackupAuditSink
	}
	recorder := opsaudit.New(auditstore.New(router))
	return func(ev backup.AuditEvent) {
		outcome := ev.Outcome
		if outcome == "" {
			outcome = "success"
		}
		at := ev.At
		if at.IsZero() {
			at = time.Now().UTC()
		}
		payload := make(map[string]any, len(ev.Fields)+3)
		for k, v := range ev.Fields {
			payload[k] = v
		}
		payload["backupId"] = ev.BackupID
		payload["actor"] = ev.Actor
		payload["outcome"] = outcome
		if ev.Detail != "" {
			payload["detail"] = ev.Detail
		}
		recorder.Record(ev.Type, payload, at)
	}
}

// logBackupAuditSink is the degraded backup audit sink: it logs each
// §16.7 terminal-state event so the transitions stay observable when no
// durable audit store is wired (dev / Postgres-unreachable runs).
func logBackupAuditSink(ev backup.AuditEvent) {
	log.Printf("lenny-backup: §16.7 audit (log-only): type=%s backup=%s outcome=%s",
		ev.Type, ev.BackupID, ev.Outcome)
}
