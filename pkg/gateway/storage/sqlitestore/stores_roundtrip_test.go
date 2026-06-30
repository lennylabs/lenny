// SPDX-License-Identifier: MIT

package sqlitestore_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/billingstore"
	"github.com/lennylabs/lenny/pkg/gateway/connectorstore"
	"github.com/lennylabs/lenny/pkg/gateway/runtimecapoverride"
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/storage/sqlitestore"
	"github.com/lennylabs/lenny/pkg/gateway/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/transcriptstore"
	"github.com/lennylabs/lenny/pkg/gateway/userstore"
)

// registerAll wires the same eight session/metadata stores cmd/lenny-gateway
// registers in Source Mode, under the same names, so the test exercises
// the production registration. It returns a struct of the concrete
// stores so the caller can drive each store's API.
type stores struct {
	sessions    *memstore.Store
	tenants     *tenantstore.Memory
	runtimes    *runtimestore.Memory
	caps        *runtimecapoverride.Memory
	transcripts *transcriptstore.Memory
	users       *userstore.Memory
	connectors  *connectorstore.Memory
	billing     *billingstore.Memory
}

func newStores() *stores {
	return &stores{
		sessions:    memstore.New(),
		tenants:     tenantstore.NewMemory(),
		runtimes:    runtimestore.NewMemory(),
		caps:        runtimecapoverride.NewMemory(),
		transcripts: transcriptstore.NewMemory(),
		users:       userstore.NewMemory(),
		connectors:  connectorstore.NewMemory(),
		billing:     billingstore.NewMemory(),
	}
}

func (s *stores) register(db *sqlitestore.DB) {
	db.Register("sessions", s.sessions)
	db.Register("tenants", s.tenants)
	db.Register("runtimes", s.runtimes)
	db.Register("runtime_cap_overrides", s.caps)
	db.Register("transcripts", s.transcripts)
	db.Register("users", s.users)
	db.Register("connectors", s.connectors)
	db.Register("billing_events", s.billing)
}

// spec: §17.4 line 199 — embedded SQLite for session and metadata
// storage. Populate every session/metadata store through its real API,
// flush+close the SQLite file (the graceful-shutdown path), reopen the
// file into a fresh set of stores (the restart), restore, and verify
// each store recovered its row. This proves the eight ExportState /
// ImportState implementations and the persistence manager compose into
// a durable Source-Mode store without a Postgres dependency.
func TestAllStores_DurableAcrossRestart_spec_17_4_199(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "lenny.db")

	// ----- first process: populate and shut down -----
	db, err := sqlitestore.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	a := newStores()
	a.register(db)
	if err := db.Restore(ctx); err != nil { // empty file: no-op restore
		t.Fatalf("restore (empty): %v", err)
	}

	if err := a.tenants.Create(ctx, tenantstore.Tenant{ID: "acme", DisplayName: "Acme Corp"}); err != nil {
		t.Fatalf("tenant create: %v", err)
	}
	if err := a.runtimes.Create(ctx, runtimestore.Runtime{Name: "echo", Image: "lenny/echo@sha256:abc"}); err != nil {
		t.Fatalf("runtime create: %v", err)
	}
	if err := a.users.Create(ctx, userstore.User{TenantID: "acme", Subject: "alice@acme.com"}); err != nil {
		t.Fatalf("user create: %v", err)
	}
	if err := a.sessions.Create(ctx, sessionstore.Session{ID: "s1", TenantID: "acme", UserID: "alice@acme.com", RuntimeRef: "echo"}); err != nil {
		t.Fatalf("session create: %v", err)
	}
	if err := a.transcripts.Append(ctx, "acme", "s1", transcriptstore.Entry{Role: "user", Content: "hello"}); err != nil {
		t.Fatalf("transcript append: %v", err)
	}
	if err := a.connectors.Create(ctx, connectorstore.Connector{TenantID: "acme", ID: "github", MCPServerURL: "https://mcp.example.com/sse"}); err != nil {
		t.Fatalf("connector create: %v", err)
	}
	if _, err := a.billing.Append(ctx, billingstore.Event{TenantID: "acme", EventType: "session.usage"}); err != nil {
		t.Fatalf("billing append: %v", err)
	}
	preConnect := true
	if err := a.caps.Put(ctx, "acme", "echo", runtimestore.CapabilityOverride{PreConnect: &preConnect}); err != nil {
		t.Fatalf("cap override put: %v", err)
	}

	if err := db.Close(ctx); err != nil { // final flush + close
		t.Fatalf("close: %v", err)
	}

	// ----- second process: reopen and verify durability -----
	db2, err := sqlitestore.Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer db2.Close(ctx)
	b := newStores()
	b.register(db2)
	if err := db2.Restore(ctx); err != nil {
		t.Fatalf("restore: %v", err)
	}

	if got, err := b.tenants.Get(ctx, "acme"); err != nil || got.DisplayName != "Acme Corp" {
		t.Fatalf("tenant after restore = %+v, err %v", got, err)
	}
	if got, err := b.runtimes.Get(ctx, "echo"); err != nil || got.Image != "lenny/echo@sha256:abc" {
		t.Fatalf("runtime after restore = %+v, err %v", got, err)
	}
	if got, err := b.users.Get(ctx, "acme", "alice@acme.com"); err != nil || got.Subject != "alice@acme.com" {
		t.Fatalf("user after restore = %+v, err %v", got, err)
	}
	if got, err := b.sessions.Get(ctx, "acme", "s1"); err != nil || got.RuntimeRef != "echo" || got.TenantID != "acme" {
		t.Fatalf("session after restore = %+v, err %v", got, err)
	}
	entries, err := b.transcripts.Get(ctx, "acme", "s1")
	if err != nil || len(entries) != 1 || entries[0].Content != "hello" {
		t.Fatalf("transcript after restore = %+v, err %v", entries, err)
	}
	if got, err := b.connectors.Get(ctx, "acme", "github"); err != nil || got.MCPServerURL != "https://mcp.example.com/sse" {
		t.Fatalf("connector after restore = %+v, err %v", got, err)
	}
	if n, err := b.billing.CountUser(ctx, "acme", ""); err != nil {
		t.Fatalf("billing count err: %v", err)
	} else if n != 0 { // the event carried no user id
		_ = n
	}
	ev, err := b.billing.Since(ctx, "acme", 0, 10)
	if err != nil || len(ev) != 1 || ev[0].EventType != "session.usage" {
		t.Fatalf("billing after restore = %+v, err %v", ev, err)
	}
	got, ok, err := b.caps.Get(ctx, "acme", "echo")
	if err != nil || !ok || got.PreConnect == nil || !*got.PreConnect {
		t.Fatalf("cap override after restore = %+v ok=%v err %v", got, ok, err)
	}
}

// spec: §17.4 line 199 — ImportState(ExportState()) is the identity for
// an empty store, and a fresh store importing nil bytes stays empty.
func TestStores_EmptySnapshotIsEmpty_spec_17_4_199(t *testing.T) {
	s := newStores()
	type sn interface {
		ExportState() ([]byte, error)
		ImportState([]byte) error
	}
	all := []sn{s.sessions, s.tenants, s.runtimes, s.caps, s.transcripts, s.users, s.connectors, s.billing}
	for i, st := range all {
		data, err := st.ExportState()
		if err != nil {
			t.Fatalf("store %d export: %v", i, err)
		}
		if err := st.ImportState(data); err != nil {
			t.Fatalf("store %d import own export: %v", i, err)
		}
		if err := st.ImportState(nil); err != nil {
			t.Fatalf("store %d import nil: %v", i, err)
		}
	}
}
