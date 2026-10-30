// SPDX-License-Identifier: MIT

package main

import (
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/pkg/controller/poolscaling"
)

// spec: 4.6.2 (PoolScalingController (Pool Configuration)), 10.7 (Experiment
// Primitives)
//
// TestPoolSourceCarriesExperimentVariantLifecycle pins that the dedicated
// PoolScalingController workload reads its pool set through the §10.7
// experiment variant-pool decoration over the §5.2 pool registry. This binary
// is the only PoolScalingController a chart install runs; lenny-controller
// registers no in-process copy. A plain PoolStoreSource here would leave every
// experiment's variant pool unsized and every concluded variant pool
// undeleted. The stores are never queried: only the source composition is
// inspected, so a zero pool suffices.
func TestPoolSourceCarriesExperimentVariantLifecycle(t *testing.T) {
	src := buildPoolSource(&pgxpool.Pool{}, "lenny-agents")

	evs, ok := src.(*poolscaling.ExperimentVariantSource)
	if !ok {
		t.Fatalf("pool source is %T, want *poolscaling.ExperimentVariantSource (§10.7 variant lifecycle)", src)
	}
	if evs.Experiments == nil {
		t.Error("ExperimentVariantSource.Experiments is nil; the source would pass through without the §10.7 lifecycle")
	}
	if evs.BasePools == nil {
		t.Error("ExperimentVariantSource.BasePools is nil; the §4.6.2 base-pool adjustment would always be skipped")
	}
	inner, ok := evs.Inner.(*poolscaling.PoolStoreSource)
	if !ok {
		t.Fatalf("inner source is %T, want *poolscaling.PoolStoreSource over the §5.2 pool registry", evs.Inner)
	}
	if inner.Namespace != "lenny-agents" {
		t.Errorf("inner source namespace = %q, want %q (the --agent-namespace the derived CRDs land in)", inner.Namespace, "lenny-agents")
	}
	if inner.Store == nil {
		t.Error("inner PoolStoreSource has no pool registry store")
	}
}
