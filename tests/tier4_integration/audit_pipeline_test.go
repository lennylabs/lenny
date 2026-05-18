// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration test: the §11.7 audit pipeline end to end against
// the live stack. An audit event flows through the Postgres-backed
// hash chain → the OCSF translator state machine → the SIEM forwarder
// → an external SIEM sink. The pipeline is assembled from the
// gateway-resident components (pkg/gateway/auditstore, pkg/audit/ocsf,
// pkg/audit/siem) and driven against a real Postgres container and the
// tests/testinfra/stubs/siem fake SIEM endpoint.
//
// This file converts the TestAuditPipeline scaffold (formerly skipped
// in scaffolds_test.go) into a real integration test.
//
// SIEM wire-protocol seam: §11.7 mandates OCSF as the record format
// but does not pin a single SIEM transport for v1. The test exercises
// the reference siem.HTTPSink transport (newline/array OCSF JSON POST
// with an optional HMAC batch signature) against the documented SIEM
// stub. A deployer targeting a specific SIEM product implements
// siem.Sink for that product.
package tier4_integration_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/audit"
	"github.com/lennylabs/lenny/pkg/audit/integrity"
	"github.com/lennylabs/lenny/pkg/audit/ocsf"
	"github.com/lennylabs/lenny/pkg/audit/siem"
	"github.com/lennylabs/lenny/pkg/gateway/auditstore"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
	siemstub "github.com/lennylabs/lenny/tests/testinfra/stubs/siem"
	"github.com/lennylabs/lenny/tests/testinfra/wait"
)

// spec: 11.7
// diagnosis: the §11.7 audit pipeline must carry an event end to end:
// a durable append to the Postgres hash chain, OCSF translation at the
// egress boundary with the row's ocsf_translation_state advancing to
// succeeded, and delivery of the OCSF v1.1.0 record to the external
// SIEM. A break anywhere in auditstore → ocsf → siem fails this test.
func TestAuditPipeline(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// Live stack: a real Postgres container with the production
	// migrations, and the fake SIEM endpoint standing in for the
	// external immutable audit log.
	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: filepath.Join(schematest.RepoRoot(t), "migrations"),
	})
	sinkStub := siemstub.New(t)

	// Seed the tenant whose audit chain the pipeline writes.
	const tenant = "acme"
	if _, err := pg.Pool.Exec(ctx,
		`INSERT INTO tenants (id, genesis_nonce) VALUES ($1, '\x00')`, tenant); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}

	// Assemble the §11.7 pipeline: Postgres audit store → OCSF
	// translator → SIEM forwarder → SIEM sink.
	store := auditstore.New(pg.Pool)
	forwarder := siem.NewForwarder(
		siem.NewHTTPSink(siem.HTTPSinkOptions{Endpoint: sinkStub.URL()}),
		siem.DefaultForwarderConfig(),
		siem.NewCountingMetrics(),
	)
	translator := ocsf.NewTranslator(store, forwarder, ocsf.DefaultTranslationConfig(), ocsf.NewCountingMetrics())

	t.Run("startup SIEM connectivity validation succeeds against the live sink", func(t *testing.T) {
		// §11.7: at startup a test event is sent and the gateway
		// refuses to start until acknowledgement is received.
		if err := forwarder.ValidateConnectivity(ctx); err != nil {
			t.Fatalf("ValidateConnectivity against the SIEM stub: %v", err)
		}
		if !forwarder.Healthy() {
			t.Error("forwarder must report healthy after a successful connectivity probe")
		}
		sinkStub.Reset() // drop the probe so the assertions below count only audit events
	})

	t.Run("an audit event flows chain to OCSF translation to SIEM", func(t *testing.T) {
		// 1. Durable append to the Postgres hash chain.
		row, err := store.Append(ctx, tenant, "session.created",
			json.RawMessage(`{"user_id":"alice@acme.com","caller_kind":"human","session_id":"sess-1"}`),
			time.Now())
		if err != nil {
			t.Fatalf("audit append: %v", err)
		}
		if row.Seq != 1 {
			t.Fatalf("first audit row seq = %d, want 1", row.Seq)
		}

		// 2. The OCSF translator runs at the egress boundary and
		//    multicasts the translated record into the SIEM forwarder.
		res, err := translator.RunCycle(ctx)
		if err != nil {
			t.Fatalf("OCSF translator RunCycle: %v", err)
		}
		if res.Translated != 1 {
			t.Fatalf("translator translated %d rows, want 1", res.Translated)
		}

		// 3. The row's ocsf_translation_state advanced to succeeded.
		st, _, err := store.TranslationState(ctx, tenant, row.Seq)
		if err != nil {
			t.Fatalf("TranslationState: %v", err)
		}
		if st != audit.OCSFSucceeded {
			t.Errorf("ocsf_translation_state = %q, want succeeded", st)
		}

		// 4. The OCSF v1.1.0 record reached the SIEM.
		wait.For(t, 5*time.Second, "audit event reaches the SIEM", func() (bool, error) {
			return len(sinkStub.Events()) > 0, nil
		})
		events := sinkStub.Events()
		if len(events) == 0 {
			t.Fatal("no audit event reached the SIEM")
		}
		got := events[0]
		// The SIEM received an OCSF record, not a Lenny-internal row.
		if got["class_uid"] == nil {
			t.Errorf("SIEM event is not an OCSF record (no class_uid): %v", got)
		}
		md, _ := got["metadata"].(map[string]any)
		if md == nil {
			t.Fatalf("SIEM event has no metadata block: %v", got)
		}
		if md["version"] != ocsf.Version {
			t.Errorf("SIEM event metadata.version = %v, want %s", md["version"], ocsf.Version)
		}
		if md["tenant_uid"] != tenant {
			t.Errorf("SIEM event metadata.tenant_uid = %v, want %s", md["tenant_uid"], tenant)
		}
		// The §11.7 unmapped.lenny_chain extension surfaces the hash
		// chain so the SIEM consumer can verify it.
		unmapped, _ := got["unmapped"].(map[string]any)
		chain, _ := unmapped["lenny_chain"].(map[string]any)
		if chain == nil || chain["prev_hash"] == nil {
			t.Errorf("SIEM event lacks the unmapped.lenny_chain extension: %v", got)
		}
	})

	t.Run("the Postgres hash chain stays verifiable after OCSF egress", func(t *testing.T) {
		// §11.7: OCSF translation is a pure rendering step at the
		// egress boundary; it cannot affect chain integrity.
		if _, err := store.Append(ctx, tenant, "session.completed", json.RawMessage(`{}`), time.Now()); err != nil {
			t.Fatalf("second append: %v", err)
		}
		res, err := store.Verify(ctx, tenant)
		if err != nil {
			t.Fatalf("Verify: %v", err)
		}
		if res.Integrity != audit.ChainVerified {
			t.Errorf("chain integrity after OCSF egress = %q, want verified", res.Integrity)
		}
		// The startup chain-continuity check agrees.
		results, err := integrity.CheckChainContinuity(ctx, pg.Pool)
		if err != nil {
			t.Fatalf("CheckChainContinuity: %v", err)
		}
		if broken := integrity.FirstBroken(results); broken != nil {
			t.Errorf("continuity check found a broken chain: tenant %q, %s",
				broken.TenantID, broken.Result.Detail)
		}
	})

	t.Run("a translation-failure receipt keeps the SIEM stream flowing", func(t *testing.T) {
		// §11.7 dead-letter handling: an untranslatable event must not
		// halt the per-tenant SIEM stream. The translator emits a
		// translation-failure receipt (OCSF class 2004) in its place.
		before := len(sinkStub.Events())
		if _, err := store.Append(ctx, tenant, "permanently.unmapped.type", json.RawMessage(`{}`), time.Now()); err != nil {
			t.Fatalf("append unmapped: %v", err)
		}
		cfg := ocsf.DefaultTranslationConfig()
		cfg.MaxAttempts = 2
		dlTranslator := ocsf.NewTranslator(store, forwarder, cfg, ocsf.NewCountingMetrics())
		for i := 0; i < cfg.MaxAttempts; i++ {
			if _, err := dlTranslator.RunCycle(ctx); err != nil {
				t.Fatalf("dead-letter RunCycle %d: %v", i, err)
			}
		}
		wait.For(t, 5*time.Second, "dead-letter receipt reaches the SIEM", func() (bool, error) {
			return len(sinkStub.Events()) > before, nil
		})
		// The receipt that reached the SIEM is an OCSF Application
		// Security Finding (class 2004).
		var sawFinding bool
		for _, e := range sinkStub.Events()[before:] {
			if cu, ok := e["class_uid"].(float64); ok && int(cu) == ocsf.ClassAppSecurityFinding {
				sawFinding = true
			}
		}
		if !sawFinding {
			t.Error("the dead-letter receipt (OCSF class 2004) did not reach the SIEM")
		}
	})
}
