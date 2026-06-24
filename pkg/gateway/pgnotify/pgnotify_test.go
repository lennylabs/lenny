// SPDX-License-Identifier: MIT

package pgnotify

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	embpostgres "github.com/lennylabs/lenny/tests/testinfra/embpg"
)

// spec: §4.9 line 1647 — a nil Bus (no Postgres fallback configured) is a
// safe no-op: Publish returns nil and Subscribe blocks until cancel.
// F-13.3.8.
func TestNilBusIsNoop_F1338(t *testing.T) {
	var b *Bus // nil
	if err := b.Publish(context.Background(), "ch", []byte("x")); err != nil {
		t.Errorf("nil Bus Publish returned %v, want nil", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		b.Subscribe(ctx, "ch", func([]byte) { t.Error("nil Bus delivered a payload") })
		close(done)
	}()
	select {
	case <-done:
		t.Fatal("nil Bus Subscribe returned before the context was cancelled")
	case <-time.After(30 * time.Millisecond):
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("nil Bus Subscribe did not return after cancel")
	}
}

// spec: §4.9 line 1647 — a real Postgres LISTEN/NOTIFY round-trip: a
// payload published via pg_notify reaches a LISTENing Subscribe loop.
// This is the actual fallback transport the credential deny-list uses
// when Redis is unavailable. F-13.3.8.
func TestListenNotifyRoundTrip_F1338(t *testing.T) {
	if testing.Short() {
		t.Skip("downloads the PostgreSQL bundle; skipped under -short")
	}
	pg := embpostgres.New(embpostgres.Config{
		DataDir:      t.TempDir(),
		Port:         0, // ephemeral; §17.4 forbids hardcoded ports and they collide under parallel tests
		Database:     "lenny",
		Username:     "lenny",
		Password:     "lenny",
		StartTimeout: 3 * time.Minute,
	})
	if err := pg.Start(); err != nil {
		t.Fatalf("embedded postgres Start: %v", err)
	}
	defer func() { _ = pg.Stop() }()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, pg.DSN())
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()

	bus := New(pool)
	got := make(chan string, 1)
	subCtx, subCancel := context.WithCancel(ctx)
	defer subCancel()
	go bus.Subscribe(subCtx, "lenny_credential_denylist", func(payload []byte) {
		select {
		case got <- string(payload):
		default:
		}
	})

	// Give the LISTEN connection a moment to establish before NOTIFY, so
	// the notification is not raised before the subscriber is attached.
	deadline := time.Now().Add(5 * time.Second)
	for {
		if err := bus.Publish(ctx, "lenny_credential_denylist", []byte(`{"source":"pool","credentialId":"key-2"}`)); err != nil {
			t.Fatalf("Publish: %v", err)
		}
		select {
		case payload := <-got:
			if payload != `{"source":"pool","credentialId":"key-2"}` {
				t.Errorf("delivered payload = %q, want the published key", payload)
			}
			return
		case <-time.After(150 * time.Millisecond):
			if time.Now().After(deadline) {
				t.Fatal("LISTEN/NOTIFY round-trip did not deliver within the deadline")
			}
		}
	}
}
