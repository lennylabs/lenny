// SPDX-License-Identifier: MIT

package storerouter_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/pkg/storerouter"
)

// spec: §11.7 line 431 (CMP-058) — PlatformPostgresForRegion resolves a
// region-set platform-tenant audit write to that region's platform-Postgres
// from the storage.regions.<region>.postgresEndpoint map. The empty region
// falls back to the global PlatformPostgres pool (residency rule 2).
func TestPlatformPostgresForRegionResolvesConfiguredRegion_spec_11_7_431(t *testing.T) {
	global := fakePool(t)
	euPool := fakePool(t)
	usPool := fakePool(t)

	r, err := storerouter.NewSingleShardRouter(storerouter.Config{
		Postgres: global,
		PlatformRegions: map[string]*pgxpool.Pool{
			"eu-west-1": euPool,
			"us-east-1": usPool,
		},
	})
	if err != nil {
		t.Fatalf("new router: %v", err)
	}
	ctx := context.Background()

	// Empty region -> global platform Postgres (rule 2 fallback).
	got, err := r.PlatformPostgresForRegion(ctx, "")
	if err != nil {
		t.Fatalf("empty region: unexpected error %v", err)
	}
	if got != global {
		t.Errorf("empty region: got %p, want global %p", got, global)
	}

	// A configured region resolves to its own pool (rule 1).
	got, err = r.PlatformPostgresForRegion(ctx, "eu-west-1")
	if err != nil {
		t.Fatalf("eu-west-1: unexpected error %v", err)
	}
	if got != euPool {
		t.Errorf("eu-west-1: got %p, want %p", got, euPool)
	}
	got, err = r.PlatformPostgresForRegion(ctx, "us-east-1")
	if err != nil {
		t.Fatalf("us-east-1: unexpected error %v", err)
	}
	if got != usPool {
		t.Errorf("us-east-1: got %p, want %p", got, usPool)
	}
}

// spec: §11.7 line 433 (CMP-058 rule 3) — a region with no
// storage.regions.<region>.postgresEndpoint entry is unresolvable and
// PlatformPostgresForRegion fails closed with ErrPlatformRegionUnresolvable
// so the audit write maps to PLATFORM_AUDIT_REGION_UNRESOLVABLE.
func TestPlatformPostgresForRegionFailsClosedOnMissingEntry_spec_11_7_433(t *testing.T) {
	r, err := storerouter.NewSingleShardRouter(storerouter.Config{
		Postgres:        fakePool(t),
		PlatformRegions: map[string]*pgxpool.Pool{"eu-west-1": fakePool(t)},
	})
	if err != nil {
		t.Fatalf("new router: %v", err)
	}
	got, err := r.PlatformPostgresForRegion(context.Background(), "ap-south-1")
	if !errors.Is(err, storerouter.ErrPlatformRegionUnresolvable) {
		t.Fatalf("missing region: got err %v, want ErrPlatformRegionUnresolvable", err)
	}
	if got != nil {
		t.Errorf("missing region: got non-nil pool %p, want nil", got)
	}
}

// A single-region deployment with no PlatformRegions map fails closed for
// any region-set target tenant — the empty region (global) still resolves.
// spec: §11.7 lines 431-433.
func TestPlatformPostgresForRegionEmptyMapFailsClosedForRegion_spec_11_7_433(t *testing.T) {
	global := fakePool(t)
	r, err := storerouter.NewSingleShardRouter(storerouter.Config{Postgres: global})
	if err != nil {
		t.Fatalf("new router: %v", err)
	}
	ctx := context.Background()

	if got, err := r.PlatformPostgresForRegion(ctx, ""); err != nil || got != global {
		t.Errorf("empty region: got (%p, %v), want (%p, nil)", got, err, global)
	}
	if _, err := r.PlatformPostgresForRegion(ctx, "eu-west-1"); !errors.Is(err, storerouter.ErrPlatformRegionUnresolvable) {
		t.Errorf("region with empty map: got %v, want ErrPlatformRegionUnresolvable", err)
	}
}
