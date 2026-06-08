// SPDX-License-Identifier: MIT

package condition_test

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	sandboxcond "github.com/lennylabs/lenny/pkg/sandbox/condition"
	"github.com/lennylabs/lenny/pkg/sandbox/state"
	"github.com/lennylabs/lenny/tests/testinfra/envtest"
)

const testNS = "lenny-agents"

func newScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := lennyv1.AddToScheme(s); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}
	if err := corev1.AddToScheme(s); err != nil {
		t.Fatalf("AddToScheme corev1: %v", err)
	}
	return s
}

func newClient(t *testing.T, sb *lennyv1.Sandbox) client.Client {
	t.Helper()
	env := envtest.Start(t)
	c, err := client.New(env.RESTConfig(), client.Options{Scheme: newScheme(t)})
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}
	ctx := context.Background()
	if err := c.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: testNS}}); err != nil {
		t.Fatalf("create namespace: %v", err)
	}
	if err := c.Create(ctx, sb); err != nil {
		t.Fatalf("create sandbox: %v", err)
	}
	return c
}

// spec: §6.2 line 305 / §4.6.1 — distinct lifecycle condition types
// accumulate as history; they do not overwrite one another because each
// Type is owned by its own SSA field manager.
func TestApplyAccumulatesDistinctConditions_spec_6_2_305(t *testing.T) {
	sb := &lennyv1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{Name: "pod-1", Namespace: testNS},
		Spec:       lennyv1.SandboxSpec{PoolRef: "p"},
	}
	c := newClient(t, sb)
	ctx := context.Background()

	if err := sandboxcond.Apply(ctx, c, sb, metav1.Condition{
		Type: sandboxcond.Suspended, Reason: "InterruptAcknowledged",
	}); err != nil {
		t.Fatalf("apply suspended: %v", err)
	}
	if err := sandboxcond.Apply(ctx, c, sb, metav1.Condition{
		Type: sandboxcond.Terminated, Reason: "Completed",
	}); err != nil {
		t.Fatalf("apply terminated: %v", err)
	}

	var got lennyv1.Sandbox
	if err := c.Get(ctx, client.ObjectKey{Namespace: testNS, Name: "pod-1"}, &got); err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(got.Status.Conditions) != 2 {
		t.Fatalf("conditions = %d, want 2 (both accumulate): %+v", len(got.Status.Conditions), got.Status.Conditions)
	}
	for _, ty := range []string{sandboxcond.Suspended, sandboxcond.Terminated} {
		if cond := apimeta.FindStatusCondition(got.Status.Conditions, ty); cond == nil {
			t.Errorf("missing %s condition", ty)
		} else if cond.Status != metav1.ConditionTrue {
			t.Errorf("%s status = %q, want True", ty, cond.Status)
		}
	}
}

// spec: §6.2 line 305 — re-applying the same condition Type updates that
// entry in place (listType=map keyed by type) rather than duplicating it.
func TestApplyUpdatesSameTypeInPlace_spec_6_2_305(t *testing.T) {
	sb := &lennyv1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{Name: "pod-2", Namespace: testNS},
		Spec:       lennyv1.SandboxSpec{PoolRef: "p"},
	}
	c := newClient(t, sb)
	ctx := context.Background()

	if err := sandboxcond.Apply(ctx, c, sb, metav1.Condition{
		Type: sandboxcond.Suspended, Reason: "InterruptAcknowledged",
	}); err != nil {
		t.Fatalf("apply 1: %v", err)
	}
	if err := sandboxcond.Apply(ctx, c, sb, metav1.Condition{
		Type: sandboxcond.Suspended, Reason: "InterruptTimeout",
	}); err != nil {
		t.Fatalf("apply 2: %v", err)
	}

	var got lennyv1.Sandbox
	if err := c.Get(ctx, client.ObjectKey{Namespace: testNS, Name: "pod-2"}, &got); err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(got.Status.Conditions) != 1 {
		t.Fatalf("conditions = %d, want 1 (updated in place): %+v", len(got.Status.Conditions), got.Status.Conditions)
	}
	if got.Status.Conditions[0].Reason != "InterruptTimeout" {
		t.Errorf("reason = %q, want InterruptTimeout (latest wins)", got.Status.Conditions[0].Reason)
	}
}

// spec: §6.2 lines 105-117 — TerminalReason maps every terminal phase and
// only terminal phases.
func TestTerminalReason_spec_6_2_105(t *testing.T) {
	cases := map[state.State]string{
		state.Completed: "Completed",
		state.Failed:    "Failed",
		state.Cancelled: "Cancelled",
		state.Expired:   "Expired",
		state.Attached:  "",
		state.Idle:      "",
		state.Draining:  "",
	}
	for phase, want := range cases {
		if got := sandboxcond.TerminalReason(phase); got != want {
			t.Errorf("TerminalReason(%q) = %q, want %q", phase, got, want)
		}
	}
}

// An empty condition type is rejected so a caller never writes a malformed
// entry the API server would refuse.
func TestApplyRejectsEmptyType(t *testing.T) {
	sb := &lennyv1.Sandbox{ObjectMeta: metav1.ObjectMeta{Name: "pod-3", Namespace: testNS}}
	if err := sandboxcond.Apply(context.Background(), nil, sb, metav1.Condition{Reason: "x"}); err == nil {
		t.Error("Apply with empty type must error")
	}
}
