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

// spec: 5.2 (Deployer acknowledgment (runtime process kept across sessions))
func TestPoolPolicyMirrorReportsKeepsRuntime_spec_5_2(t *testing.T) {
	ctx := context.Background()
	pools := poolstore.NewMemory()
	if err := pools.Create(ctx, poolstore.Pool{
		Name:          "reuse",
		RuntimeRef:    "rt",
		ExecutionMode: runtimestore.ExecutionModeSession,
		SessionPolicy: &runtimestore.SessionPolicy{
			AcknowledgeProcessLevelIsolation: true,
			Recycle:                          &runtimestore.RecyclePolicy{Enabled: true, AcknowledgeBestEffortScrub: true, MaxSessionsPerPod: 5},
		},
	}); err != nil {
		t.Fatalf("create pool: %v", err)
	}
	reader := NewPoolPolicyReader(pools)

	mirror, found, err := reader.PoolPolicy(ctx, "reuse")
	if err != nil || !found {
		t.Fatalf("PoolPolicy: found=%v err=%v", found, err)
	}
	if !mirror.KeepsRuntime {
		t.Error("an acknowledged recycling pool reads KeepsRuntime false")
	}

	if _, err := pools.Update(ctx, "reuse", func(p *poolstore.Pool) error {
		p.SessionPolicy.Recycle.Enabled = false
		return nil
	}); err != nil {
		t.Fatalf("update pool: %v", err)
	}
	mirror, _, err = reader.PoolPolicy(ctx, "reuse")
	if err != nil {
		t.Fatalf("PoolPolicy after update: %v", err)
	}
	if mirror.KeepsRuntime {
		t.Error("a pool updated to recycle.enabled: false still reads KeepsRuntime true")
	}
}

// spec: 5.2 (Deployer acknowledgment (runtime process kept across sessions))
func TestBindAndResumeRequestsCarryKeepsRuntime_spec_5_2(t *testing.T) {
	s := &Server{}
	row := sessionstore.Session{
		ID: "sess-1", TenantID: "acme", RuntimeRef: "rt",
		WorkspaceSnapshot: &sessionstore.WorkspaceSnapshot{Ref: "ckpt-1"},
	}
	for _, keeps := range []bool{true, false} {
		match := podsession.PoolMatch{Pool: "p", KeepsRuntime: keeps}
		bind := s.exclusiveBindRequest(context.Background(), row, match, workspaceplan.Plan{}, nil, nil, nil, "")
		if bind.KeepsRuntime != keeps {
			t.Errorf("exclusiveBindRequest KeepsRuntime = %v, want %v", bind.KeepsRuntime, keeps)
		}
		resume := checkpointResumeRequest(row, match, nil, "", 0, nil)
		if resume.KeepsRuntime != keeps {
			t.Errorf("checkpointResumeRequest KeepsRuntime = %v, want %v", resume.KeepsRuntime, keeps)
		}
	}
}
