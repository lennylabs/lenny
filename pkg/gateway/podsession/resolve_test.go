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

func TestResolvePoolMatchesByRuntime(t *testing.T) {
	c := k8sClient(
		t,
		warmPool("claude-pool", "claude-tmpl"),
		sandboxTemplate("claude-tmpl", "claude-code", "gvisor"),
	)
	got, err := podsession.ResolvePool(context.Background(), c, testNS, "claude-code", "")
	if err != nil {
		t.Fatalf("ResolvePool: %v", err)
	}
	if got != "claude-pool" {
		t.Errorf("resolved pool = %q, want claude-pool", got)
	}
}

func TestResolvePoolNoMatch(t *testing.T) {
	c := k8sClient(
		t,
		warmPool("claude-pool", "claude-tmpl"),
		sandboxTemplate("claude-tmpl", "claude-code", "gvisor"),
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
		sandboxTemplate("tmpl-gvisor", "claude-code", "gvisor"),
		warmPool("claude-kata", "tmpl-kata"),
		sandboxTemplate("tmpl-kata", "claude-code", "kata"),
	)
	got, err := podsession.ResolvePool(context.Background(), c, testNS, "claude-code", "kata")
	if err != nil {
		t.Fatalf("ResolvePool: %v", err)
	}
	if got != "claude-kata" {
		t.Errorf("resolved pool = %q, want claude-kata", got)
	}
}

func TestResolvePoolAmbiguous(t *testing.T) {
	// Two pools with the same runtime and isolation: the gateway cannot
	// pick one.
	c := k8sClient(
		t,
		warmPool("pool-a", "tmpl-a"),
		sandboxTemplate("tmpl-a", "claude-code", "gvisor"),
		warmPool("pool-b", "tmpl-b"),
		sandboxTemplate("tmpl-b", "claude-code", "gvisor"),
	)
	_, err := podsession.ResolvePool(context.Background(), c, testNS, "claude-code", "gvisor")
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
		sandboxTemplate("claude-tmpl", "claude-code", "gvisor"),
	)
	got, err := podsession.ResolvePool(context.Background(), c, testNS, "claude-code", "gvisor")
	if err != nil {
		t.Fatalf("ResolvePool: %v", err)
	}
	if got != "claude-pool" {
		t.Errorf("resolved pool = %q, want claude-pool", got)
	}
}
