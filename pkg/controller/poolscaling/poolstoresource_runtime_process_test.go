// SPDX-License-Identifier: MIT

package poolscaling_test

import (
	"context"
	"testing"

	"github.com/lennylabs/lenny/pkg/controller/poolscaling"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/poolstore"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
)

// listedRows is a poolstore.Store double whose List returns rows, so a row
// the gateway's interim admission would refuse (a `restart` pool) still
// reaches the mirror under test.
type listedRows struct {
	poolstore.Store
	rows []poolstore.Pool
}

func (l listedRows) List(context.Context, poolstore.ListFilter) ([]poolstore.Pool, error) {
	return l.rows, nil
}

// TestSessionPolicyToCRDMirrorsRuntimeProcess pins the PoolScalingController
// mirror of recycle.runtimeProcess onto SandboxTemplate
// spec.sessionPolicy.recycle.runtimeProcess: each value passes through, a
// row that sets only the lifetime still yields a recycle block (the block
// was nil for such a row before the lifetime was mirrored, so the
// WarmPoolController would read no lifetime), the lifetime rides beside a
// scrub control, and a row with none of the CRD-modeled fields yields no
// block.
//
// spec: §5.2 (Runtime-process lifetime), §4.6.3 (CRD field ownership and write boundaries)
func TestSessionPolicyToCRDMirrorsRuntimeProcess(t *testing.T) {
	recycle := func(lifetime runtimestore.RuntimeProcessLifetime, profile runtimestore.MicrovmScrubMode) *runtimestore.SessionPolicy {
		return &runtimestore.SessionPolicy{Recycle: &runtimestore.RecyclePolicy{
			Enabled: true, MaxSessionsPerPod: 5, RuntimeProcess: lifetime, ScrubProfile: profile,
		}}
	}
	cases := []struct {
		name        string
		sp          *runtimestore.SessionPolicy
		wantBlock   bool
		wantLife    string
		wantProfile string
	}{
		{"keep only", recycle(runtimestore.RuntimeProcessKeep, ""), true, "keep", ""},
		{"restart only", recycle(runtimestore.RuntimeProcessRestart, ""), true, "restart", ""},
		{"keep beside in-place", recycle(runtimestore.RuntimeProcessKeep, runtimestore.MicrovmScrubInPlace), true, "keep", "in-place"},
		{"scrub profile without lifetime", recycle("", runtimestore.MicrovmScrubVMRestart), true, "", "vm-restart"},
		{"recycle without CRD fields", recycle("", ""), false, "", ""},
		{"no recycle block", &runtimestore.SessionPolicy{MaxConcurrentSessions: 2}, false, "", ""},
		{"no session policy", nil, false, "", ""},
	}
	for _, tc := range cases {
		src := &poolscaling.PoolStoreSource{
			Store:     listedRows{rows: []poolstore.Pool{{Name: "p", RuntimeRef: "rt", SessionPolicy: tc.sp}}},
			Namespace: "lenny-agents",
		}
		configs, err := src.ListPoolConfigs(context.Background())
		if err != nil || len(configs) != 1 {
			t.Fatalf("%s: ListPoolConfigs = %d configs, %v", tc.name, len(configs), err)
		}
		sp := configs[0].Template.SessionPolicy
		if (sp != nil && sp.Recycle != nil) != tc.wantBlock {
			t.Fatalf("%s: recycle block present = %v, want %v", tc.name, sp != nil, tc.wantBlock)
		}
		if !tc.wantBlock {
			continue
		}
		if sp.Recycle.RuntimeProcess != tc.wantLife || sp.Recycle.ScrubProfile != tc.wantProfile {
			t.Errorf("%s: runtimeProcess=%q scrubProfile=%q, want %q %q", tc.name,
				sp.Recycle.RuntimeProcess, sp.Recycle.ScrubProfile, tc.wantLife, tc.wantProfile)
		}
	}
}
