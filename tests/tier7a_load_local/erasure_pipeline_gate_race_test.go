// SPDX-License-Identifier: MIT

//go:build load_local

// Tier-7a coverage for the §12.8 store-deletion gate on the §7.1
// post-terminal window. A fake seal writes an object under a drained
// session's prefix and then records the pod release, while erasure runner
// ticks race it over a fake blob store. The gate holds the store deletions
// until the release is recorded, so no object of the erased user survives a
// completed job.
//
// spec: §12.8 (Compliance Interfaces), §7.1 (Post-terminal window).
package tier7a_load_local_test

import (
	"context"
	"io"
	"log/slog"
	"math/rand"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/environment/userstore"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/storage/erasure"
	"github.com/lennylabs/lenny/pkg/gateway/storage/erasurejob"
)

// gateBlobs is a fake blob store keyed by object name; every object of a
// session lives under "<tenant>/<user>/<session>/".
type gateBlobs struct {
	mu      sync.Mutex
	objects map[string]bool
}

func (b *gateBlobs) put(key string) {
	b.mu.Lock()
	b.objects[key] = true
	b.mu.Unlock()
}

// deleteUser deletes every object under the user's prefix.
func (b *gateBlobs) deleteUser(tenantID, userID string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := 0
	for k := range b.objects {
		if strings.HasPrefix(k, tenantID+"/"+userID+"/") {
			delete(b.objects, k)
			n++
		}
	}
	return n
}

// userObjects lists the objects left under the user's prefix.
func (b *gateBlobs) userObjects(tenantID, userID string) []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []string
	for k := range b.objects {
		if strings.HasPrefix(k, tenantID+"/"+userID+"/") {
			out = append(out, k)
		}
	}
	return out
}

// gateRunner builds an erasure runner whose store deletion removes the
// user's objects and whose release gate is admin.ReleasePending over the
// session store with the given cap.
func gateRunner(jobs erasurejob.Store, sessions sessionstore.Store, blobs *gateBlobs, capD time.Duration) *erasurejob.Runner {
	orch := erasure.New(erasure.Config{UserScoped: []erasure.Eraser{{
		Name: "artifacts",
		DeleteByUser: func(_ context.Context, tenantID, userID string) (int, error) {
			return blobs.deleteUser(tenantID, userID), nil
		},
	}}})
	return erasurejob.NewRunner(jobs, orch, nil).
		WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))).
		WithSessionDrain(func(context.Context, string, string) error { return nil }).
		WithHoldCheck(func(context.Context, string, string) ([]erasurejob.Hold, error) { return nil, nil }).
		WithReleaseGate(func(ctx context.Context, tenantID, userID string) ([]string, error) {
			return admin.ReleasePending(ctx, sessions, tenantID, userID, time.Now(), capD)
		}).
		WithCompletionReceipt(func(context.Context, erasurejob.Job) error { return nil })
}

// TestErasureWaitsForThePostTerminalRelease_spec_12_8 races a fake seal of a
// drained session against erasure runner ticks in randomized interleavings.
// The seal uploads an object under the session's prefix and then records the
// release; the runner deletes the user's objects once the gate admits it. No
// object of the erased user survives a completed job.
//
// spec: 12.8 (Compliance Interfaces), 7.1 (Post-terminal window)
// diagnosis: a failure means the erasure job deleted the user's stores while
// a drained session was still in its post-terminal window, so an object its
// seal uploaded afterwards survived the completed erasure; the release gate
// is missing, unwired, or not run before DeleteByUser.
func TestErasureWaitsForThePostTerminalRelease_spec_12_8(t *testing.T) {
	seed := time.Now().UnixNano()
	t.Logf("seed %d", seed)
	master := rand.New(rand.NewSource(seed))
	ctx := context.Background()
	for round := 0; round < 100; round++ {
		rng := rand.New(rand.NewSource(master.Int63()))
		sessions := memstore.New()
		if err := sessions.Create(ctx, sessionstore.Session{
			ID: "sess-sealing", TenantID: "acme", UserID: "alice", State: session.StateCompleted,
			PodAssignment: "pod-a", TerminatedAt: time.Now(),
		}); err != nil {
			t.Fatalf("seed session: %v", err)
		}
		users := userstore.NewMemory()
		if err := users.Create(ctx, userstore.User{TenantID: "acme", Subject: "alice"}); err != nil {
			t.Fatalf("seed user: %v", err)
		}
		jobs := erasurejob.NewMemory(erasurejob.WithRestrictionWriter(users))
		blobs := &gateBlobs{objects: map[string]bool{"acme/alice/sess-sealing/checkpoint-1": true}}
		runner := gateRunner(jobs, sessions, blobs, time.Hour)
		job, err := runner.Start(ctx, "acme", "alice", "carol", nil)
		if err != nil {
			t.Fatalf("Start: %v", err)
		}
		sealDelay := time.Duration(rng.Intn(400)) * time.Microsecond

		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			time.Sleep(sealDelay)
			blobs.put("acme/alice/sess-sealing/sealed")
			runtime.Gosched()
			if err := sessionstore.RecordPodRelease(ctx, sessions, "acme", "sess-sealing", time.Now()); err != nil {
				t.Errorf("round %d: record release: %v", round, err)
			}
		}()
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				_ = runner.Run(ctx, job.ID)
				cur, err := jobs.Get(ctx, erasurejob.AllTenants, job.ID)
				if err == nil && cur.Phase.Terminal() {
					return
				}
				time.Sleep(time.Duration(rng.Intn(50)) * time.Microsecond)
			}
		}()
		wg.Wait()
		_ = runner.Run(ctx, job.ID)
		final, err := jobs.Get(ctx, erasurejob.AllTenants, job.ID)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if final.Phase != erasurejob.PhaseCompleted {
			t.Fatalf("round %d: job phase = %s (%s), want completed", round, final.Phase, final.Failure)
		}
		if left := blobs.userObjects("acme", "alice"); len(left) != 0 {
			t.Fatalf("round %d: objects of the erased user survived the completed job: %v", round, left)
		}
	}
}
