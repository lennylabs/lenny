//go:build component

// SPDX-License-Identifier: MIT

package coordination_test

import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/coordination/coordfence"
	"github.com/lennylabs/lenny/pkg/gateway/coordination/coordination"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/storage/leasestore"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/coordfixture"
)

// storeGenerations is the coordfence.GenerationReader over the session store,
// the same adaptation the gateway wires in production.
type storeGenerations struct{ store sessionstore.Store }

func (g storeGenerations) CoordinationGeneration(ctx context.Context, tenantID, sessionID string) (int64, error) {
	row, err := g.store.Get(ctx, tenantID, sessionID)
	if err != nil {
		return 0, err
	}
	return row.CoordinationGeneration, nil
}

// spec: 10.1 (the coordinator handoff generation fence; a fence not strictly
// above the value the pod recorded is refused as coordinator_handoff_stale),
// 10.1.2 (step 1's compare-and-swap mints the post-handoff generation), 4.2
// (a newly created session row carries coordination_generation = 1 and the
// counter is never reset)
// diagnosis: the resume fence and the first crash takeover collide on one
//
//	generation. The resume path fences on the value it reads without
//	incrementing it, so a session row created below the §4.2 baseline is fenced
//	at the gateway's floor of 1 while its row still reads 0, and the first
//	takeover's compare-and-swap then mints 1 as well. The pod refuses that
//	takeover fence with FailedPrecondition and the coordinator_handoff_stale
//	detail, so a healthy handoff costs a sweep cycle of delay and a pair of
//	split-brain metric increments. A failure here means the session store's
//	Create no longer baselines the counter, or the fence stopped comparing
//	strictly.
func TestResumeFenceThenTakeoverAccepted_spec_10_1(t *testing.T) {
	t.Parallel()
	rd := containers.StartRedis(t, containers.RedisOptions{})
	leases := leasestore.New(rd.Client)
	ctx := context.Background()

	const tenant = "acme"
	sessID := uniq(t) + "-resume-takeover"

	// The row is created with CoordinationGeneration unset, so the store's
	// §4.2 baseline is what the resume fence reads.
	sessions := memstore.New()
	if err := sessions.Create(ctx, sessionstore.Session{
		ID: sessID, TenantID: tenant, State: session.StateRunning,
		RuntimeRef: "echo", PodAssignment: "pod-" + sessID,
	}); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	if got, _ := sessions.Get(ctx, tenant, sessID); got.CoordinationGeneration != 1 {
		t.Fatalf("seeded CoordinationGeneration = %d, want the §4.2 baseline of 1", got.CoordinationGeneration)
	}

	pod := coordfixture.StartPod(t, sessID)

	// The resume leg. The resuming replica fences the pod on the value it
	// reads from the row and does not increment it, driven through the
	// production Fencer so its floor of a non-positive row value is on the
	// path.
	fencer := coordfence.New(storeGenerations{store: sessions}, leases, "replica-1", nil, coordfence.Options{})
	relinquished, err := fencer.Fence(ctx, pod.Client, tenant, sessID)
	if err != nil || relinquished {
		t.Fatalf("resume fence: relinquished=%v err=%v, want an accepted fence", relinquished, err)
	}
	if got := pod.LastFenced(sessID); got != 1 {
		t.Fatalf("pod fenced generation after the resume fence = %d, want 1", got)
	}

	// The takeover leg. A crash takeover mints the post-handoff generation
	// through the compare-and-swap and fences the pod at it.
	sw := coordination.NewSweeper(staticLister{tenant}, sessions, leases, coordination.Options{
		ReplicaID: "replica-2",
		TTL:       30 * time.Second,
		Interval:  time.Hour,
	})
	gen := sw.RecordHandoff(ctx, tenant, sessID)
	if gen != 2 {
		t.Fatalf("post-handoff generation = %d, want 2 (strictly above the resume fence)", gen)
	}

	accepted, err := pod.Fence(ctx, sessID, gen)
	if status.Code(err) == codes.FailedPrecondition {
		t.Fatalf("the first takeover fence was refused as coordinator_handoff_stale: %v", err)
	}
	if err != nil {
		t.Fatalf("takeover fence: %v", err)
	}
	if !accepted {
		t.Fatal("the pod did not accept the first takeover fence, want it accepted")
	}
	if got := pod.LastFenced(sessID); got != 2 {
		t.Errorf("pod fenced generation after the takeover = %d, want 2", got)
	}
}
