// SPDX-License-Identifier: MIT

package upgradeservice_test

import (
	"context"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/ops/upgradeservice"
	"github.com/lennylabs/lenny/pkg/releasechannel"
)

// TestCheckWritesCacheOnSuccess_spec_25_8 covers §25.8 line 3414: a
// successful upgrade-check refreshes the durable cache.
func TestCheckWritesCacheOnSuccess_spec_25_8(t *testing.T) {
	cache := upgradeservice.NewMemCheckCache()
	chk := upgradeservice.NewChecker(upgradeservice.CheckerOptions{
		Source:         staticChannel("1.6.0"),
		CurrentVersion: "1.5.0",
		Cache:          cache,
	})
	if _, err := chk.Check(context.Background()); err != nil {
		t.Fatalf("Check: %v", err)
	}
	cached, ok, err := cache.Load(context.Background())
	if err != nil || !ok {
		t.Fatalf("cache not written: ok=%v err=%v", ok, err)
	}
	if cached.Manifest.Version != "1.6.0" || cached.CurrentVersion != "1.5.0" {
		t.Fatalf("cached check = %+v", cached)
	}
}

// TestCheckServesCacheWhenUnreachable_spec_25_8 covers §25.8 line 3413:
// an unreachable channel returns the cached response with cached=true and
// a cacheAge measured from the cached check time.
func TestCheckServesCacheWhenUnreachable_spec_25_8(t *testing.T) {
	now := time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)
	cache := upgradeservice.NewMemCheckCache()
	_ = cache.Save(context.Background(), upgradeservice.CachedCheck{
		CheckedAt:      now.Add(-90 * time.Second),
		CurrentVersion: "1.5.0",
		Manifest:       releasechannel.Manifest{Version: "1.6.0"},
	})
	var gauge []bool
	chk := upgradeservice.NewChecker(upgradeservice.CheckerOptions{
		Source:         releasechannel.NewStaticSource(nil), // unreachable: no manifest
		CurrentVersion: "1.5.0",
		Cache:          cache,
		Gauge:          func(a bool) { gauge = append(gauge, a) },
		Now:            func() time.Time { return now },
	})
	res, err := chk.Check(context.Background())
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !res.Cached || res.CacheAge != "1m30s" {
		t.Fatalf("expected cached result with age 1m30s, got %+v", res)
	}
	if res.AvailableVersion != "1.6.0" || !res.UpgradeAvailable {
		t.Fatalf("cached result not reconstructed: %+v", res)
	}
	if len(gauge) != 1 || gauge[0] != true {
		t.Fatalf("gauge = %v, want [true] from cached availability", gauge)
	}
}

// TestCheckUnreachableEmptyCache_spec_25_8 covers the first-check case:
// channel unreachable and cache empty returns the original error so the
// handler maps it to 503 UPGRADE_CHANNEL_UNREACHABLE.
func TestCheckUnreachableEmptyCache_spec_25_8(t *testing.T) {
	chk := upgradeservice.NewChecker(upgradeservice.CheckerOptions{
		Source:         releasechannel.NewStaticSource(nil),
		CurrentVersion: "1.5.0",
		Cache:          upgradeservice.NewMemCheckCache(),
	})
	if _, err := chk.Check(context.Background()); err != releasechannel.ErrManifestNotFound {
		t.Fatalf("expected ErrManifestNotFound on empty cache, got %v", err)
	}
}
