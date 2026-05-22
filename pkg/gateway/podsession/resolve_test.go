// SPDX-License-Identifier: MIT

package podsession_test

import (
	"context"
	"errors"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1"
	"github.com/lennylabs/lenny/pkg/gateway/podsession"
)

func warmPool(name, templateRef string) *lennyv1.SandboxWarmPool {
	return &lennyv1.SandboxWarmPool{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testNS},
		Spec:       lennyv1.SandboxWarmPoolSpec{TemplateRef: templateRef, MinWarm: 1, MaxWarm: 5},
	}
}

func sandboxTemplate(name, runtimeRef, isolation string) *lennyv1.SandboxTemplate {
	return &lennyv1.SandboxTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testNS},
		Spec:       lennyv1.SandboxTemplateSpec{RuntimeRef: runtimeRef, IsolationProfile: isolation},
	}
}

func concurrentTemplate(name, runtimeRef, isolation, style string, maxConcurrent int32) *lennyv1.SandboxTemplate {
	return &lennyv1.SandboxTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testNS},
		Spec: lennyv1.SandboxTemplateSpec{
			RuntimeRef:       runtimeRef,
			IsolationProfile: isolation,
			ExecutionMode:    "concurrent",
			ConcurrencyStyle: style,
			MaxConcurrent:    maxConcurrent,
		},
	}
}

// TestResolvePoolReturnsConcurrentDispatchFields covers the gateway
// dispatch fix: ResolvePool must surface ExecutionMode,
// ConcurrencyStyle, and MaxConcurrent so startOnPod can route a
// concurrent-mode runtime through BindSlot rather than Bind. A
// regression here would put concurrent-mode sandboxes into `claimed`
// instead of `slot_active`.
func TestResolvePoolReturnsConcurrentDispatchFields(t *testing.T) {
	c := k8sClient(
		t,
		warmPool("cstateless-pool", "cstateless-tmpl"),
		concurrentTemplate("cstateless-tmpl", "load-cstateless-runtime", "sandboxed", "stateless", 8),
	)
	got, err := podsession.ResolvePool(context.Background(), c, testNS, "load-cstateless-runtime", "sandboxed")
	if err != nil {
		t.Fatalf("ResolvePool: %v", err)
	}
	if got.Pool != "cstateless-pool" {
		t.Errorf("resolved pool = %q, want cstateless-pool", got.Pool)
	}
	if got.ExecutionMode != "concurrent" {
		t.Errorf("executionMode = %q, want concurrent (the start path dispatches to BindSlot when this is concurrent)", got.ExecutionMode)
	}
	if got.ConcurrencyStyle != "stateless" {
		t.Errorf("concurrencyStyle = %q, want stateless", got.ConcurrencyStyle)
	}
	if got.MaxConcurrent != 8 {
		t.Errorf("maxConcurrent = %d, want 8", got.MaxConcurrent)
	}
}

// TestResolvePoolSessionModeLeavesDispatchFieldsEmpty covers the
// negative case: a session-mode pool must not carry concurrent-mode
// dispatch fields, so startOnPod takes the Bind path.
func TestResolvePoolSessionModeLeavesDispatchFieldsEmpty(t *testing.T) {
	c := k8sClient(
		t,
		warmPool("session-pool", "session-tmpl"),
		sandboxTemplate("session-tmpl", "claude-code", "sandboxed"),
	)
	got, err := podsession.ResolvePool(context.Background(), c, testNS, "claude-code", "sandboxed")
	if err != nil {
		t.Fatalf("ResolvePool: %v", err)
	}
	if got.ExecutionMode != "" {
		t.Errorf("executionMode = %q, want empty for the default session mode", got.ExecutionMode)
	}
	if got.ConcurrencyStyle != "" {
		t.Errorf("concurrencyStyle = %q, want empty", got.ConcurrencyStyle)
	}
	if got.MaxConcurrent != 0 {
		t.Errorf("maxConcurrent = %d, want 0", got.MaxConcurrent)
	}
}

func TestResolvePoolMatchesByRuntime(t *testing.T) {
	c := k8sClient(
		t,
		warmPool("claude-pool", "claude-tmpl"),
		sandboxTemplate("claude-tmpl", "claude-code", "sandboxed"),
	)
	got, err := podsession.ResolvePool(context.Background(), c, testNS, "claude-code", "")
	if err != nil {
		t.Fatalf("ResolvePool: %v", err)
	}
	if got.Pool != "claude-pool" {
		t.Errorf("resolved pool = %q, want claude-pool", got.Pool)
	}
}

func TestResolvePoolNoMatch(t *testing.T) {
	c := k8sClient(
		t,
		warmPool("claude-pool", "claude-tmpl"),
		sandboxTemplate("claude-tmpl", "claude-code", "sandboxed"),
	)
	_, err := podsession.ResolvePool(context.Background(), c, testNS, "other-runtime", "")
	if !errors.Is(err, podsession.ErrNoMatchingPool) {
		t.Errorf("error = %v, want ErrNoMatchingPool", err)
	}
}

func TestResolvePoolDisambiguatesByIsolation(t *testing.T) {
	c := k8sClient(
		t,
		warmPool("claude-gvisor", "tmpl-gvisor"),
		sandboxTemplate("tmpl-gvisor", "claude-code", "sandboxed"),
		warmPool("claude-kata", "tmpl-kata"),
		sandboxTemplate("tmpl-kata", "claude-code", "microvm"),
	)
	got, err := podsession.ResolvePool(context.Background(), c, testNS, "claude-code", "microvm")
	if err != nil {
		t.Fatalf("ResolvePool: %v", err)
	}
	if got.Pool != "claude-kata" {
		t.Errorf("resolved pool = %q, want claude-kata", got.Pool)
	}
}

func TestResolvePoolAmbiguous(t *testing.T) {
	// Two pools with the same runtime and isolation: the gateway cannot
	// pick one.
	c := k8sClient(
		t,
		warmPool("pool-a", "tmpl-a"),
		sandboxTemplate("tmpl-a", "claude-code", "sandboxed"),
		warmPool("pool-b", "tmpl-b"),
		sandboxTemplate("tmpl-b", "claude-code", "sandboxed"),
	)
	_, err := podsession.ResolvePool(context.Background(), c, testNS, "claude-code", "sandboxed")
	if !errors.Is(err, podsession.ErrAmbiguousPool) {
		t.Errorf("error = %v, want ErrAmbiguousPool", err)
	}
}

func TestResolvePoolSkipsDanglingTemplateRef(t *testing.T) {
	// The pool with a dangling template ref is skipped; the valid pool
	// still resolves.
	c := k8sClient(
		t,
		warmPool("broken-pool", "missing-tmpl"),
		warmPool("claude-pool", "claude-tmpl"),
		sandboxTemplate("claude-tmpl", "claude-code", "sandboxed"),
	)
	got, err := podsession.ResolvePool(context.Background(), c, testNS, "claude-code", "sandboxed")
	if err != nil {
		t.Fatalf("ResolvePool: %v", err)
	}
	if got.Pool != "claude-pool" {
		t.Errorf("resolved pool = %q, want claude-pool", got.Pool)
	}
}
