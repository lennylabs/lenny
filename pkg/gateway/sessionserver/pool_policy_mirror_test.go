// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/poolstore"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/workspaceplan"
)

// TestPoolPolicyMirrorReportsReuseAndAcknowledgmentPredicates pins the pool
// policy mirror's predicate pair: it computes ReusesPods and
// RequiresProcessAcknowledgment from the stored sessionPolicy on every
// resolution, so an admitted update changes both at the next acquisition.
//
// spec: §5.2 (Deployer acknowledgment (runtime process kept across sessions))
func TestPoolPolicyMirrorReportsReuseAndAcknowledgmentPredicates_spec_5_2(t *testing.T) {
	ctx := context.Background()
	pools := poolstore.NewMemory()
	if err := pools.Create(ctx, poolstore.Pool{
		Name:          "reuse",
		RuntimeRef:    "rt",
		ExecutionMode: runtimestore.ExecutionModeSession,
		SessionPolicy: &runtimestore.SessionPolicy{
			AcknowledgeProcessLevelIsolation: true,
			Recycle: &runtimestore.RecyclePolicy{
				Enabled: true, AcknowledgeBestEffortScrub: true,
				MaxSessionsPerPod: 5, RuntimeProcess: runtimestore.RuntimeProcessKeep,
			},
		},
	}); err != nil {
		t.Fatalf("create pool: %v", err)
	}
	if err := pools.Create(ctx, poolstore.Pool{
		Name:          "concurrent",
		RuntimeRef:    "rt",
		ExecutionMode: runtimestore.ExecutionModeSession,
		SessionPolicy: &runtimestore.SessionPolicy{MaxConcurrentSessions: 2, AcknowledgeProcessLevelIsolation: true},
	}); err != nil {
		t.Fatalf("create concurrent pool: %v", err)
	}
	reader := NewPoolPolicyReader(pools)

	assertPair := func(name, pool string, wantReuse, wantAck bool) {
		t.Helper()
		mirror, found, err := reader.PoolPolicy(ctx, pool)
		if err != nil || !found {
			t.Fatalf("%s: PoolPolicy: found=%v err=%v", name, found, err)
		}
		if mirror.ReusesPods != wantReuse || mirror.RequiresProcessAcknowledgment != wantAck {
			t.Errorf("%s: ReusesPods=%v RequiresProcessAcknowledgment=%v, want %v %v",
				name, mirror.ReusesPods, mirror.RequiresProcessAcknowledgment, wantReuse, wantAck)
		}
	}
	assertPair("keep recycling pool", "reuse", true, true)
	assertPair("concurrent pool", "concurrent", true, true)

	// A sequential pool under `restart` reuses pods without requiring the
	// acknowledgment. Admission refuses `restart` in the interim, so the row
	// is served by a store double rather than written through validation.
	restartRow := poolstore.Pool{Name: "restart", SessionPolicy: &runtimestore.SessionPolicy{
		Recycle: &runtimestore.RecyclePolicy{
			Enabled: true, AcknowledgeBestEffortScrub: true,
			MaxSessionsPerPod: 5, RuntimeProcess: runtimestore.RuntimeProcessRestart,
		},
	}}
	mirror, found, err := NewPoolPolicyReader(rowStore{Store: pools, row: restartRow}).PoolPolicy(ctx, "restart")
	if err != nil || !found {
		t.Fatalf("restart pool: PoolPolicy: found=%v err=%v", found, err)
	}
	if !mirror.ReusesPods || mirror.RequiresProcessAcknowledgment {
		t.Errorf("restart pool: ReusesPods=%v RequiresProcessAcknowledgment=%v, want true false",
			mirror.ReusesPods, mirror.RequiresProcessAcknowledgment)
	}

	if _, err := pools.Update(ctx, "reuse", func(p *poolstore.Pool) error {
		p.SessionPolicy.Recycle.Enabled = false
		p.SessionPolicy.Recycle.RuntimeProcess = ""
		return nil
	}); err != nil {
		t.Fatalf("update pool: %v", err)
	}
	assertPair("pool updated to recycle.enabled: false", "reuse", false, false)
}

// TestBindAndResumeRequestsCarryReuseAndAcknowledgmentPredicates pins that
// the exclusive bind and the checkpoint resume copy both predicates from the
// resolved pool match, each independently of the other.
//
// spec: §5.2 (Deployer acknowledgment (runtime process kept across sessions))
func TestBindAndResumeRequestsCarryReuseAndAcknowledgmentPredicates_spec_5_2(t *testing.T) {
	s := &Server{}
	row := sessionstore.Session{
		ID: "sess-1", TenantID: "acme", RuntimeRef: "rt",
		WorkspaceSnapshot: &sessionstore.WorkspaceSnapshot{Ref: "ckpt-1"},
	}
	for _, reuse := range []bool{true, false} {
		for _, ack := range []bool{true, false} {
			match := podsession.PoolMatch{Pool: "p", ReusesPods: reuse, RequiresProcessAcknowledgment: ack}
			bind := s.exclusiveBindRequest(context.Background(), row, match, workspaceplan.Plan{}, nil, nil, nil, "")
			if bind.ReusesPods != reuse || bind.RequiresProcessAcknowledgment != ack {
				t.Errorf("exclusiveBindRequest = (%v, %v), want (%v, %v)",
					bind.ReusesPods, bind.RequiresProcessAcknowledgment, reuse, ack)
			}
			resume := checkpointResumeRequest(row, match, nil, "", 0, nil, resumeBind{})
			if resume.ReusesPods != reuse || resume.RequiresProcessAcknowledgment != ack {
				t.Errorf("checkpointResumeRequest = (%v, %v), want (%v, %v)",
					resume.ReusesPods, resume.RequiresProcessAcknowledgment, reuse, ack)
			}
		}
	}
}

// rowStore is a poolstore.Store double whose Get returns row for row.Name.
type rowStore struct {
	poolstore.Store
	row poolstore.Pool
}

func (r rowStore) Get(ctx context.Context, name string) (poolstore.Pool, error) {
	if name == r.row.Name {
		return r.row, nil
	}
	return r.Store.Get(ctx, name)
}
