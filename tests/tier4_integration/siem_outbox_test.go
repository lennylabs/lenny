// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration test: the §12.3 SIEM outbox / change-data-capture
// forwarder against a live Postgres container and the SIEM stub. The
// forwarder tails committed audit_log rows past the per-tenant delivery
// high-water mark in siem_delivery_state, delivers each to the SIEM
// after Postgres has durably committed it, and advances the mark only
// after acknowledgement. This exercises the real PendingForward /
// Checkpoint / DeliveryLag SQL (pkg/gateway/auditstore/outbox.go) that
// the unit tests cover only through a fake DeliveryStore.
package tier4_integration_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/audit/siem"
	"github.com/lennylabs/lenny/pkg/gateway/auditstore"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
	siemstub "github.com/lennylabs/lenny/tests/testinfra/stubs/siem"
)

type recordingLagGauge struct {
	mu   sync.Mutex
	last float64
}

func (g *recordingLagGauge) SetSIEMDeliveryLagSeconds(s float64) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.last = s
}

func (g *recordingLagGauge) value() float64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.last
}

// acmeEvents counts SIEM stub events whose OCSF metadata.tenant_uid is
// the acme tenant, so the platform-chain cross_tenant_read feedback the
// forwarder emits for its own __all__ reads does not perturb the count.
func acmeEvents(stub *siemstub.Stub) int {
	n := 0
	for _, e := range stub.Events() {
		md, _ := e["metadata"].(map[string]any)
		if md != nil && md["tenant_uid"] == "acme" {
			n++
		}
	}
	return n
}

// spec: §12.3 line 97 — the outbox forwarder reads committed rows past
// the siem_delivery_state high-water mark, delivers them, and advances
// the mark only after acknowledgement; a re-run delivers no duplicate.
// diagnosis: a failure means the SIEM outbox forwarder advances the
// high-water mark before acknowledgement or re-delivers acknowledged
// rows, so audit events would be dropped or duplicated to the SIEM sink.
func TestSIEMOutboxForwarder(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: filepath.Join(schematest.RepoRoot(t), "migrations"),
	})
	stub := siemstub.New(t)

	const tenant = "acme"
	if _, err := pg.Pool.Exec(ctx,
		`INSERT INTO tenants (id, genesis_nonce) VALUES ($1, '\x00')`, tenant); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}

	store := auditstore.New(pg.Router(t))
	forwarder := siem.NewForwarder(
		siem.NewHTTPSink(siem.HTTPSinkOptions{Endpoint: stub.URL()}),
		siem.DefaultForwarderConfig(),
		siem.NewCountingMetrics(),
	)
	if err := forwarder.ValidateConnectivity(ctx); err != nil {
		t.Fatalf("ValidateConnectivity: %v", err)
	}
	stub.Reset()

	// Three committed audit rows on the acme chain.
	const n = 3
	for i := 0; i < n; i++ {
		if _, err := store.Append(ctx, tenant, "session.created",
			json.RawMessage(`{"user_id":"alice@acme.com","caller_kind":"human","session_id":"s"}`),
			time.Now()); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}

	gauge := &recordingLagGauge{}
	ob := siem.NewOutbox(store, forwarder, siem.OutboxConfig{}, gauge)

	if _, err := ob.RunCycle(ctx); err != nil {
		t.Fatalf("RunCycle: %v", err)
	}

	if got := acmeEvents(stub); got != n {
		t.Fatalf("SIEM received %d acme events, want %d", got, n)
	}

	// siem_delivery_state advanced to the highest acme sequence.
	var acked int64
	if err := pg.Pool.QueryRow(ctx,
		`SELECT last_acked_sequence FROM siem_delivery_state WHERE tenant_id = $1`, tenant).
		Scan(&acked); err != nil {
		t.Fatalf("read siem_delivery_state: %v", err)
	}
	if acked != n {
		t.Errorf("last_acked_sequence = %d, want %d", acked, n)
	}

	// A second cycle re-reads from the high-water mark: no acme row is
	// re-delivered (no duplication on restart).
	if _, err := ob.RunCycle(ctx); err != nil {
		t.Fatalf("second RunCycle: %v", err)
	}
	if got := acmeEvents(stub); got != n {
		t.Errorf("after re-run SIEM has %d acme events, want %d (no duplication)", got, n)
	}

	// DeliveryLag is a finite, non-negative reading once the acme tail
	// is acknowledged.
	lag, err := store.DeliveryLag(ctx)
	if err != nil {
		t.Fatalf("DeliveryLag: %v", err)
	}
	if lag < 0 {
		t.Errorf("DeliveryLag = %v, want >= 0", lag)
	}
	if gauge.value() < 0 {
		t.Errorf("lag gauge = %v, want >= 0", gauge.value())
	}
}
