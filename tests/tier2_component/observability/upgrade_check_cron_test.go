// SPDX-License-Identifier: MIT

//go:build component

// Tier-2 component test that exercises the §25.8 platform_upgrade_check
// cron against real backing services: the production cron evaluator
// (pkg/ops/opsservice), the production upgrade Checker over a
// Postgres-backed release-channel cache (pkg/ops/upgradeservice/pgstore
// on an embedded Postgres), and a signed release channel served over
// httptest that flips between reachable (HTTP 200, Ed25519-signed) and
// unreachable (HTTP 503). Unit tests cover the happy and unreachable
// paths of the Checker in isolation; this test pins the three subsystems
// working together, so the hourly cron tick refreshing the durable cache
// on channel recovery fails here rather than shipping silently.

package observability_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/migrations"
	"github.com/lennylabs/lenny/pkg/ops/opsservice"
	"github.com/lennylabs/lenny/pkg/ops/upgradeservice"
	upgradepg "github.com/lennylabs/lenny/pkg/ops/upgradeservice/pgstore"
	"github.com/lennylabs/lenny/pkg/releasechannel"
	"github.com/lennylabs/lenny/tests/testinfra/clockstep"
	embpostgres "github.com/lennylabs/lenny/tests/testinfra/embpg"
)

// flipChannel is a release-channel endpoint that can be switched between
// reachable and unreachable at test time. When reachable it delegates to
// the production releasechannel.Publisher so the served body carries a
// real Ed25519 X-Lenny-Release-Signature; when down it returns HTTP 503,
// which the production HTTPSource maps to a transport failure that drives
// the §25.8 cache fallback.
type flipChannel struct {
	mu       sync.Mutex
	down     bool
	manifest releasechannel.Manifest
	signer   *releasechannel.Signer
}

func (f *flipChannel) set(down bool, m releasechannel.Manifest) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.down = down
	f.manifest = m
}

func (f *flipChannel) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	down := f.down
	m := f.manifest
	signer := f.signer
	f.mu.Unlock()

	if down {
		// §25.8: an unreachable channel. A non-200/404 status is a
		// transport failure for the HTTPSource, so the Checker falls back
		// to the cached response instead of trusting nothing.
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	pub, err := releasechannel.NewPublisher(releasechannel.PublisherOptions{
		Source: releasechannel.NewStaticSource(map[releasechannel.Channel]releasechannel.Manifest{
			releasechannel.ChannelStable: m,
		}),
		Signer: signer,
	})
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	pub.ServeHTTP(w, r)
}

// spec: §25.8 Upgrade Check — "Responses are cached in Postgres
// (platform_upgrade_check_cache.ttl_seconds, default 21600 — 6 hours).
// The check cron runs hourly even when the cache has not expired (to
// detect channel recovery after outages)." and "When the channel is
// unreachable, GET /v1/admin/platform/upgrade-check returns the cached
// response from platform_upgrade_check_cache with 'cached': true,
// 'cacheAge': '...'".
//
// diagnosis: a failure means the hourly platform_upgrade_check cron no
// longer drives the Postgres-backed release-channel cache correctly:
// either an outage tick wrongly overwrites (or fails to preserve) the
// cached row, the unreachable path stops serving cached=true, or the
// recovery tick fails to refresh the durable cache and clear the cached
// flag once the channel returns. Inspect pkg/ops/upgradeservice/check.go
// (cachedResult/Save), pkg/ops/upgradeservice/pgstore/checkcache.go, and
// the cron wiring in cmd/lenny-ops/deps.go (upgradeCheckJob) and
// pkg/ops/opsservice/cronloop.go.
func TestUpgradeCheckCronRefreshesCacheOnChannelRecovery(t *testing.T) {
	if testing.Short() {
		t.Skip("downloads the PostgreSQL bundle; skipped under -short")
	}

	// Embedded Postgres with migration 0124 applied so the real
	// platform_upgrade_check_cache table backs the cache.
	pg := embpostgres.New(embpostgres.Config{
		DataDir:      t.TempDir(),
		Port:         0, // ephemeral; hardcoded ports collide under parallel tests
		Database:     "lenny",
		Username:     "lenny",
		Password:     "lenny",
		StartTimeout: 3 * time.Minute,
	})
	if err := pg.Start(); err != nil {
		t.Fatalf("embedded postgres Start: %v", err)
	}
	t.Cleanup(func() { _ = pg.Stop() })

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)

	pool, err := pgxpool.New(ctx, pg.DSN())
	if err != nil {
		t.Fatalf("connect postgres: %v", err)
	}
	t.Cleanup(pool.Close)

	up, err := migrations.FS.ReadFile("0124_platform_upgrade.up.sql")
	if err != nil {
		t.Fatalf("read migration 0124: %v", err)
	}
	if _, err := pool.Exec(ctx, string(up)); err != nil {
		t.Fatalf("apply migration 0124: %v", err)
	}
	cache := upgradepg.NewCheckCache(pool)

	// A signed release channel: the production Publisher signs the served
	// manifest, the production HTTPSource verifies it against the same key.
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate keypair: %v", err)
	}
	signer, err := releasechannel.NewSigner(releasechannel.Key{ID: "test-key", Private: priv, Public: pub}, nil)
	if err != nil {
		t.Fatalf("NewSigner: %v", err)
	}
	verifier, err := releasechannel.NewVerifier(signer.PublicKeySet())
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}

	channel := &flipChannel{signer: signer}
	// The channel starts up, advertising a newer release than the running
	// 1.5.0 version.
	channel.set(false, releasechannel.Manifest{Version: "1.6.0", ReleaseNotes: "https://example/notes/1.6.0"})
	srv := httptest.NewServer(channel)
	t.Cleanup(srv.Close)

	source, err := releasechannel.NewHTTPSource(srv.URL, verifier, "1.5.0", srv.Client())
	if err != nil {
		t.Fatalf("NewHTTPSource: %v", err)
	}

	// Deterministic, advanceable clock shared by the cron evaluator and
	// the Checker so the hourly tick and the cacheAge are exact.
	origin := time.Date(2026, 6, 3, 12, 30, 0, 0, time.UTC)
	clk := clockstep.New(origin)

	checker := upgradeservice.NewChecker(upgradeservice.CheckerOptions{
		Source:         source,
		CurrentVersion: "1.5.0",
		Cache:          cache,
		Now:            clk.Now,
	})

	// The production platform_upgrade_check cron job: hourly, swallowing
	// the "no advertised release" signal. This mirrors upgradeCheckJob in
	// cmd/lenny-ops/deps.go, which lives in package main and cannot be
	// imported; every other subsystem here is the production code.
	job := opsservice.ScheduledJob{
		Name:       "platform-upgrade-check",
		Expression: "0 * * * *", // hourly, per §25.8 ("the check cron runs hourly")
		Run: func(ctx context.Context) error {
			if !checker.Enabled() {
				return nil
			}
			if _, err := checker.Check(ctx); err != nil {
				if err == releasechannel.ErrManifestNotFound {
					return nil
				}
				return err
			}
			return nil
		},
	}
	ev, err := opsservice.NewCronEvaluator(clk.Now, job)
	if err != nil {
		t.Fatalf("NewCronEvaluator: %v", err)
	}

	// Phase A: channel reachable. Cross the top of the hour so the hourly
	// cron fires once and populates the durable cache.
	clk.Advance(31 * time.Minute) // 13:01 — crosses 13:00
	wantCheckedA := clk.Now()
	if err := ev.Tick(ctx); err != nil {
		t.Fatalf("Phase A Tick: %v", err)
	}
	cachedA, ok, err := cache.Load(ctx)
	if err != nil || !ok {
		t.Fatalf("Phase A cache Load = (ok=%v, err=%v), want a populated cache", ok, err)
	}
	if cachedA.Manifest.Version != "1.6.0" {
		t.Fatalf("Phase A cached version = %q, want 1.6.0", cachedA.Manifest.Version)
	}
	if !cachedA.CheckedAt.Equal(wantCheckedA) {
		t.Fatalf("Phase A cache checkedAt = %v, want %v", cachedA.CheckedAt, wantCheckedA)
	}

	// Phase B: channel goes unreachable. The hourly cron still fires (the
	// TTL default is 6h, far longer than the 1h tick), but the outage tick
	// must not disturb the cached row, and a direct check must serve the
	// cached response with cached=true and an exact cacheAge.
	channel.set(true, releasechannel.Manifest{})
	clk.Advance(time.Hour) // 14:01 — crosses 14:00
	if err := ev.Tick(ctx); err != nil {
		t.Fatalf("Phase B Tick: %v", err)
	}
	cachedB, ok, err := cache.Load(ctx)
	if err != nil || !ok {
		t.Fatalf("Phase B cache Load = (ok=%v, err=%v)", ok, err)
	}
	if !cachedB.CheckedAt.Equal(wantCheckedA) {
		t.Fatalf("Phase B cache checkedAt = %v, want it unchanged at %v (an outage tick must not refresh the cache)",
			cachedB.CheckedAt, wantCheckedA)
	}
	resDown, err := checker.Check(ctx)
	if err != nil {
		t.Fatalf("Phase B Check during outage: %v", err)
	}
	if !resDown.Cached {
		t.Fatalf("Phase B result Cached = false, want true (an unreachable channel serves the cached response)")
	}
	if resDown.AvailableVersion != "1.6.0" {
		t.Fatalf("Phase B cached availableVersion = %q, want 1.6.0", resDown.AvailableVersion)
	}
	if resDown.CacheAge != "1h0m0s" {
		t.Fatalf("Phase B cacheAge = %q, want 1h0m0s", resDown.CacheAge)
	}

	// Phase C: channel recovers, now advertising 1.7.0. The next hourly
	// cron tick must refresh the Postgres cache (new checkedAt and new
	// version) and a subsequent check must clear the cached flag.
	channel.set(false, releasechannel.Manifest{Version: "1.7.0", ReleaseNotes: "https://example/notes/1.7.0"})
	clk.Advance(time.Hour) // 15:01 — crosses 15:00
	wantCheckedC := clk.Now()
	if err := ev.Tick(ctx); err != nil {
		t.Fatalf("Phase C Tick: %v", err)
	}
	cachedC, ok, err := cache.Load(ctx)
	if err != nil || !ok {
		t.Fatalf("Phase C cache Load = (ok=%v, err=%v)", ok, err)
	}
	if !cachedC.CheckedAt.Equal(wantCheckedC) {
		t.Fatalf("Phase C cache checkedAt = %v, want %v (the recovery tick must refresh the durable cache)",
			cachedC.CheckedAt, wantCheckedC)
	}
	if cachedC.Manifest.Version != "1.7.0" {
		t.Fatalf("Phase C cached version = %q, want 1.7.0 (the recovery tick must persist the newly advertised release)",
			cachedC.Manifest.Version)
	}
	resUp, err := checker.Check(ctx)
	if err != nil {
		t.Fatalf("Phase C Check after recovery: %v", err)
	}
	if resUp.Cached {
		t.Fatalf("Phase C result Cached = true, want false (a reachable channel serves the live result, clearing the cached flag)")
	}
	if resUp.AvailableVersion != "1.7.0" {
		t.Fatalf("Phase C availableVersion = %q, want 1.7.0", resUp.AvailableVersion)
	}
}
