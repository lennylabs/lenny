// SPDX-License-Identifier: MIT

// In-package unit tests for the §4.6.1 agent_pod_state derivation: the
// pure projection of a pool's live Sandbox set onto the mirror row set.
// The Sync write path itself is covered by the Postgres component test
// in tests/tier2_component/stores.
package warmpool

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
)

func deriveTestPool() *lennyv1.SandboxWarmPool {
	return &lennyv1.SandboxWarmPool{
		ObjectMeta: metav1.ObjectMeta{Name: "claude-worker-small", Namespace: "lenny-agents"},
	}
}

func deriveTestTemplate(execMode string) *lennyv1.SandboxTemplate {
	return &lennyv1.SandboxTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: "claude-worker-small", Namespace: "lenny-agents"},
		Spec:       lennyv1.SandboxTemplateSpec{ExecutionMode: execMode},
	}
}

func TestDerivePodStatesProjectsSandboxFields(t *testing.T) {
	pool := deriveTestPool()
	tmpl := deriveTestTemplate("task")
	sandboxes := []lennyv1.Sandbox{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "sb-idle", ResourceVersion: "4096"},
			Spec:       lennyv1.SandboxSpec{IsolationProfile: "sandboxed"},
			Status:     lennyv1.SandboxStatus{Phase: "idle", NodeName: "node-7"},
		},
		{
			// A Sandbox that has not yet reported a phase derives as
			// warming, matching observedPhase.
			ObjectMeta: metav1.ObjectMeta{Name: "sb-fresh", ResourceVersion: "4100"},
			Spec:       lennyv1.SandboxSpec{IsolationProfile: "microvm"},
		},
	}

	got := derivePodStates(pool, tmpl, sandboxes)
	if len(got) != 2 {
		t.Fatalf("derivePodStates returned %d rows, want 2", len(got))
	}

	idle := got[0]
	if idle.PodID != "sb-idle" {
		t.Errorf("PodID = %q, want sb-idle (the Sandbox name)", idle.PodID)
	}
	if idle.PoolID != pool.Name {
		t.Errorf("PoolID = %q, want %q", idle.PoolID, pool.Name)
	}
	if idle.State != "idle" {
		t.Errorf("State = %q, want idle", idle.State)
	}
	if idle.IsolationProfile != "sandboxed" {
		t.Errorf("IsolationProfile = %q, want sandboxed (from the Sandbox spec)", idle.IsolationProfile)
	}
	if idle.ExecutionMode != "task" {
		t.Errorf("ExecutionMode = %q, want task (the pool-level mode from the template)", idle.ExecutionMode)
	}
	if idle.ResourceVersion != 4096 {
		t.Errorf("ResourceVersion = %d, want 4096", idle.ResourceVersion)
	}
	if idle.NodeName != "node-7" {
		t.Errorf("NodeName = %q, want node-7", idle.NodeName)
	}
	// An idle/warm pod carries no session.
	if idle.TenantID != "" || idle.SessionID != "" {
		t.Errorf("idle pod carries tenant/session %q/%q, want empty", idle.TenantID, idle.SessionID)
	}

	fresh := got[1]
	if fresh.State != "warming" {
		t.Errorf("a Sandbox with no reported phase derived State = %q, want warming", fresh.State)
	}
	if fresh.IsolationProfile != "microvm" {
		t.Errorf("IsolationProfile = %q, want microvm", fresh.IsolationProfile)
	}
	if fresh.NodeName != "" {
		t.Errorf("an unscheduled pod derived NodeName = %q, want empty", fresh.NodeName)
	}
}

func TestDerivePodStatesEmptySet(t *testing.T) {
	got := derivePodStates(deriveTestPool(), deriveTestTemplate("session"), nil)
	if len(got) != 0 {
		t.Errorf("derivePodStates(nil sandboxes) returned %d rows, want 0", len(got))
	}
}

func TestDerivePodStatesNilTemplate(t *testing.T) {
	// A nil template must not panic; execution mode falls back to empty.
	got := derivePodStates(deriveTestPool(), nil, []lennyv1.Sandbox{
		{ObjectMeta: metav1.ObjectMeta{Name: "sb-a", ResourceVersion: "1"}},
	})
	if len(got) != 1 {
		t.Fatalf("derivePodStates returned %d rows, want 1", len(got))
	}
	if got[0].ExecutionMode != "" {
		t.Errorf("ExecutionMode with a nil template = %q, want empty", got[0].ExecutionMode)
	}
}

func TestParseResourceVersion(t *testing.T) {
	cases := []struct {
		in   string
		want int64
	}{
		{"0", 0},
		{"4096", 4096},
		{"9007199254740993", 9007199254740993},
		{"", 0},             // an empty resourceVersion mirrors as 0
		{"not-a-number", 0}, // an opaque/unparseable value mirrors as 0
	}
	for _, c := range cases {
		if got := parseResourceVersion(c.in); got != c.want {
			t.Errorf("parseResourceVersion(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}
