// SPDX-License-Identifier: MIT

package poolstore_test

import (
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/runtime/poolstore"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
)

// interimRestartMessage is the exact interim refusal of `restart`.
const interimRestartMessage = `poolstore: recycle.runtimeProcess "restart" is refused until the ` +
	`intra-pod MCP servers rebind per runtime generation (proposal 0084); use "keep"`

// recyclePool builds a session pool with the given concurrency, recycle
// reuse bound, scrub profile, lifetime, and acknowledgment.
func recyclePool(concurrent, maxSessions int, profile runtimestore.MicrovmScrubMode,
	lifetime runtimestore.RuntimeProcessLifetime, ack bool,
) poolstore.Pool {
	return poolstore.Pool{
		Name:          "rp",
		ExecutionMode: runtimestore.ExecutionModeSession,
		SessionPolicy: &runtimestore.SessionPolicy{
			MaxConcurrentSessions:            concurrent,
			AcknowledgeProcessLevelIsolation: ack,
			Recycle: &runtimestore.RecyclePolicy{
				Enabled: true, AcknowledgeBestEffortScrub: true,
				MaxSessionsPerPod: maxSessions, ScrubProfile: profile, RuntimeProcess: lifetime,
			},
		},
	}
}

// TestValidateRecyclePolicyRuntimeProcess_spec_5_2 covers each branch of the
// recycle.runtimeProcess admission rule on a sequential and a concurrent
// reusing recycling pool: the value enum, required with no default on a
// reusing recycling pool, rejected on every other recycle block, and the
// acknowledgment that a `keep` pool requires.
//
// spec: §5.2 (Runtime-process lifetime), §5.2 (Deployer acknowledgment (runtime process kept across sessions))
func TestValidateRecyclePolicyRuntimeProcess_spec_5_2(t *testing.T) {
	keep := runtimestore.RuntimeProcessKeep
	cases := []struct {
		name    string
		pool    poolstore.Pool
		wantSub string
	}{
		{"sequential keep acknowledged is admitted", recyclePool(0, 5, "", keep, true), ""},
		{"concurrent keep acknowledged is admitted", recyclePool(2, 5, "", keep, true), ""},
		{"sequential missing lifetime is rejected", recyclePool(0, 5, "", "", true), "recycle.runtimeProcess (keep or restart) is required"},
		{"concurrent missing lifetime is rejected", recyclePool(2, 5, "", "", true), "recycle.runtimeProcess (keep or restart) is required"},
		{"sequential unknown lifetime is rejected", recyclePool(0, 5, "", "forever", true), "not a recognised §5.2 lifetime"},
		{"concurrent unknown lifetime is rejected", recyclePool(2, 5, "", "forever", true), "not a recognised §5.2 lifetime"},
		{"lifetime on a one-session pool is rejected", recyclePool(0, 1, "", keep, true), "recycle.runtimeProcess is permitted only"},
		{"lifetime on a vm-restart pool is rejected", recyclePool(0, 5, runtimestore.MicrovmScrubVMRestart, keep, true), "recycle.runtimeProcess is permitted only"},
		{"one-session pool without lifetime is admitted", recyclePool(0, 1, "", "", false), ""},
		{"sequential keep without acknowledgment is rejected", recyclePool(0, 5, "", keep, false), "acknowledgeProcessLevelIsolation=true"},
	}
	disabled := recyclePool(0, 5, "", keep, true)
	disabled.SessionPolicy.Recycle.Enabled = false
	cases = append(cases, struct {
		name    string
		pool    poolstore.Pool
		wantSub string
	}{"lifetime on a disabled recycle block is rejected", disabled, "recycle.runtimeProcess is permitted only"})
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := poolstore.ValidateSessionPolicy(tc.pool)
			if tc.wantSub == "" {
				if err != nil {
					t.Fatalf("want admitted, got %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantSub) {
				t.Fatalf("error %v, want one containing %q", err, tc.wantSub)
			}
		})
	}
}

// TestValidateRecyclePolicyRefusesRestartInterim pins the interim refusal of
// recycle.runtimeProcess: restart on every pool, with its exact message, on a
// sequential and a concurrent reusing recycling pool, acknowledged or not,
// and that the required-and-rejected checks of the field run before it.
//
// INTERIM TEST. The refusal it pins has no specification text; it stands
// until proposal 0084 rebinds the intra-pod MCP servers per runtime
// generation. Delete this test together with the gate when proposal 0084
// lands.
func TestValidateRecyclePolicyRefusesRestartInterim(t *testing.T) {
	restart := runtimestore.RuntimeProcessRestart
	for _, pool := range []poolstore.Pool{
		recyclePool(0, 5, "", restart, false),
		recyclePool(0, 5, runtimestore.MicrovmScrubInPlace, restart, false),
		recyclePool(2, 5, "", restart, true),
	} {
		pool.SessionPolicy.Recycle.AcknowledgeMicrovmResidualState = true
		err := poolstore.ValidateSessionPolicy(pool)
		if err == nil || err.Error() != interimRestartMessage {
			t.Errorf("%+v: error %v, want the interim refusal", pool.SessionPolicy.Recycle, err)
		}
	}
	// The presence rule runs first: `restart` on a pool that does not reuse
	// pods receives the rejected-elsewhere error.
	err := poolstore.ValidateSessionPolicy(recyclePool(0, 1, "", restart, false))
	if err == nil || !strings.Contains(err.Error(), "recycle.runtimeProcess is permitted only") {
		t.Errorf("restart on a one-session pool: %v, want the presence rejection", err)
	}
	// The store refuses to persist a `restart` pool.
	if err := poolstore.NewMemory().Create(t.Context(), recyclePool(0, 5, "", restart, false)); err == nil ||
		err.Error() != interimRestartMessage {
		t.Errorf("Create: %v, want the interim refusal", err)
	}
}

// TestValidateRuntimeProcessRestartable_spec_5_2 pins the end-state refusal
// of `restart` on a pool whose runtime is not restartable, which the
// admission paths run before the store's interim refusal.
//
// spec: §5.2 (Runtime-process lifetime), §5.1 (command)
func TestValidateRuntimeProcessRestartable_spec_5_2(t *testing.T) {
	restartPool := recyclePool(0, 5, "", runtimestore.RuntimeProcessRestart, false)
	keepPool := recyclePool(0, 5, "", runtimestore.RuntimeProcessKeep, true)
	cases := []struct {
		name        string
		restartable bool
		pool        poolstore.Pool
		wantErr     bool
	}{
		{"restart on a non-restartable runtime is refused", false, restartPool, true},
		{"restart on a restartable runtime passes", true, restartPool, false},
		{"keep on a non-restartable runtime passes", false, keepPool, false},
		{"no session policy passes", false, poolstore.Pool{}, false},
		{"no recycle block passes", false, poolstore.Pool{SessionPolicy: &runtimestore.SessionPolicy{}}, false},
	}
	for _, tc := range cases {
		err := poolstore.ValidateRuntimeProcessRestartable(tc.restartable, tc.pool)
		if (err != nil) != tc.wantErr {
			t.Errorf("%s: error %v, wantErr %v", tc.name, err, tc.wantErr)
		}
		if err != nil && !strings.Contains(err.Error(), "requires a restartable runtime") {
			t.Errorf("%s: message %q does not name the restartable requirement", tc.name, err)
		}
	}
}

// TestProcessAcknowledgmentMessageKeepPool_spec_5_2 pins the acknowledgment
// rejection an unacknowledged `keep` pool receives: it states that the pod
// keeps one runtime process across the sessions it serves.
//
// spec: §5.2 (Deployer acknowledgment (runtime process kept across sessions))
func TestProcessAcknowledgmentMessageKeepPool_spec_5_2(t *testing.T) {
	err := poolstore.ValidateSessionPolicy(recyclePool(0, 5, "", runtimestore.RuntimeProcessKeep, false))
	if err == nil || !strings.Contains(err.Error(), "the pod keeps one runtime process across the sessions it serves") {
		t.Fatalf("error %v, want the kept-process clause", err)
	}
}
