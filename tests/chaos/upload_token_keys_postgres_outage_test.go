//go:build chaos

// SPDX-License-Identifier: MIT

// Chaos coverage for the shared upload-token signing keys under a real
// Postgres outage. A gateway replica's ring reads platform_signing_keys
// through a fault proxy in front of a real Postgres container. While the
// proxy is cut, the replica keeps verifying with the keys it loaded, does
// not rotate, and keeps minting until its last successful reload is older
// than the overlap window; after that it refuses to mint. Once Postgres is
// reachable again, a refresh succeeds, minting resumes, and a due rotation
// is published.
package chaos_test

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/pkg/kms"
	"github.com/lennylabs/lenny/pkg/signingkeys"
	signingkeyspg "github.com/lennylabs/lenny/pkg/signingkeys/pgstore"
	"github.com/lennylabs/lenny/pkg/uploadtoken"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
	"github.com/lennylabs/lenny/tests/testinfra/tcpfault"
)

// outageClock is a settable clock.
type outageClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *outageClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *outageClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// spec: §7.1 (Normal Flow)
// diagnosis: A gateway replica whose Postgres became unreachable after
// start did not degrade as the shared signing keys require. Either it
// stopped verifying tokens it could still verify, it minted with a key
// after its last reload was older than the overlap window (peers may not
// hold that key), it reported a rotation while the store was unreachable,
// or it did not mint again and publish the due rotation once Postgres
// returned. See pkg/signingkeys/ring.go (Refresh, SigningKey).
func TestUploadTokenKeysUnderAPostgresOutage(t *testing.T) {
	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: filepath.Join(schematest.RepoRoot(t), "migrations"),
	})
	proxy, dsn := tcpfault.StartURL(t, pg.DSN)
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse proxied dsn: %v", err)
	}
	cfg.ConnConfig.ConnectTimeout = 2 * time.Second
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("proxied pool: %v", err)
	}
	t.Cleanup(pool.Close)
	store, err := signingkeyspg.New(pool)
	if err != nil {
		t.Fatal(err)
	}
	provider, err := kms.NewLocal(bytes.Repeat([]byte{0x41}, 32))
	if err != nil {
		t.Fatal(err)
	}
	sealer, err := signingkeys.NewEnvelopeSealer(provider, signingkeys.AliasUploadTokenSigning)
	if err != nil {
		t.Fatal(err)
	}
	clock := &outageClock{now: time.Now().UTC().Truncate(time.Microsecond)}
	ring, err := signingkeys.Open(context.Background(),
		signingkeys.UploadTokenPolicy(uploadtoken.DefaultRotationInterval, uploadtoken.DefaultOverlapWindow, uploadtoken.DefaultTTL),
		store, sealer, signingkeys.Options{StaleAfter: uploadtoken.DefaultOverlapWindow, Clock: clock.Now})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	keys := uploadtoken.NewSharedKeys(ring)
	issuer := uploadtoken.NewIssuer(keys, clock.Now)
	verifier := uploadtoken.NewVerifier(keys, nil, clock.Now)
	tok, err := issuer.Issue("sess-outage", 48*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	refresh := func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return ring.Refresh(ctx)
	}

	proxy.Cut()
	clock.Advance(uploadtoken.DefaultRotationInterval)
	if err := refresh(); err == nil {
		t.Fatal("Refresh succeeded with Postgres unreachable")
	}
	if _, err := verifier.Verify(tok, "sess-outage"); err != nil {
		t.Errorf("verification during the outage: %v, want nil", err)
	}
	if _, err := issuer.Issue("sess-late", time.Minute); !errors.Is(err, uploadtoken.ErrSigningUnavailable) {
		t.Errorf("mint with the last reload older than the overlap window: %v, want ErrSigningUnavailable", err)
	}

	proxy.Restore(t)
	var count int
	if err := pg.Pool.QueryRow(context.Background(), `SELECT count(*) FROM platform_signing_keys`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("keys written during the outage: %d rows, want the first key alone", count)
	}
	if err := refresh(); err != nil {
		t.Fatalf("Refresh after Postgres returned: %v", err)
	}
	if _, err := issuer.Issue("sess-after", time.Minute); err != nil {
		t.Errorf("mint after Postgres returned: %v", err)
	}
	if err := pg.Pool.QueryRow(context.Background(), `SELECT count(*) FROM platform_signing_keys`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Errorf("keys after the first refresh past the due rotation: %d rows, want 2", count)
	}
}
