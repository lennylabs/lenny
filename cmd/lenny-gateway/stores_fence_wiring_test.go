// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"errors"
	"flag"
	"os"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/transcriptstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// memoryPostureWiring runs buildPersistenceStores with every flag at its
// default, which selects the in-memory posture.
func memoryPostureWiring(t *testing.T) *gatewayWiring {
	t.Helper()
	savedCmdLine, savedArgs := flag.CommandLine, os.Args
	t.Cleanup(func() { flag.CommandLine, os.Args = savedCmdLine, savedArgs })
	flag.CommandLine = flag.NewFlagSet(savedArgs[0], flag.ContinueOnError)
	os.Args = []string{savedArgs[0]}
	w := &gatewayWiring{f: parseFlags()}
	w.buildPersistenceStores()
	return w
}

// TestMemoryPostureSessionStoreFencesAndCopies_spec_12_8 pins the memory
// branch of buildPersistenceStores: its session store copies a seeded
// source transcript through CreateWithTranscript, which fails when the
// branch omits the transcript copier, and refuses a Create for a tenant
// the branch's tenant store holds suspended, which fails when the branch
// omits the creation markers.
// spec: 12.8 (Compliance Interfaces: Lifecycle fence and drain), 15.1 (REST API)
func TestMemoryPostureSessionStoreFencesAndCopies_spec_12_8(t *testing.T) {
	w := memoryPostureWiring(t)
	ctx := context.Background()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := w.tenants.Create(ctx, tenantstore.Tenant{ID: "acme"}); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	if err := w.transcripts.Append(ctx, "acme", "sess_src",
		transcriptstore.Entry{Role: "user", Content: "first"},
		transcriptstore.Entry{Role: "assistant", Content: "second"}); err != nil {
		t.Fatalf("seed transcript: %v", err)
	}
	row := sessionstore.Session{
		ID: "sess_replay", TenantID: "acme", UserID: "alice@acme.com",
		State: session.StateCreated, CreatedAt: now, UpdatedAt: now,
	}
	if err := w.sessions.CreateWithTranscript(ctx, row, "sess_src"); err != nil {
		t.Fatalf("CreateWithTranscript: %v", err)
	}
	entries, err := w.transcripts.Get(ctx, "acme", "sess_replay")
	if err != nil || len(entries) != 2 || entries[0].Content != "first" || entries[1].Content != "second" {
		t.Fatalf("copied transcript = %v (%v), want the source's two entries in order", entries, err)
	}

	if _, err := w.tenants.Update(ctx, "acme", func(tn *tenantstore.Tenant) error {
		tn.Suspended = true
		return nil
	}); err != nil {
		t.Fatalf("suspend: %v", err)
	}
	row.ID = "sess_after_suspend"
	err = w.sessions.Create(ctx, row)
	var ref *sessionstore.CreationRefusal
	if !errors.As(err, &ref) || ref.Code != sessionstore.RefusalTenantSuspended {
		t.Fatalf("Create under a suspended tenant = %v, want TENANT_SUSPENDED", err)
	}
}
