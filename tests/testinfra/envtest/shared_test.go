// SPDX-License-Identifier: MIT

package envtest_test

import (
	"context"
	"regexp"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlfake "sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/lennylabs/lenny/tests/testinfra/envtest"
)

// dns1123Label is the Kubernetes namespace name grammar.
var dns1123Label = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)

// spec: 12.2.4 (the envtest harness isolates tests sharing one control
// plane by namespace)
//
// diagnosis: NamespaceName produced a name the API server rejects (not a
//
//	DNS-1123 label, or longer than 63 characters), dropped the test-name
//	prefix, or returned the same name twice, so tests sharing one control
//	plane would fail to create or would collide in their namespace.
func TestNamespaceNameIsUniqueDNS1123Label(t *testing.T) {
	for _, name := range []string{
		"Simple",
		"Under_Score/Sub Test#01",
		"--Leading/trailing--",
		strings.Repeat("VeryLongTestName", 10),
		"漢字",
	} {
		t.Run(name, func(t *testing.T) {
			a, b := envtest.NamespaceName(t), envtest.NamespaceName(t)
			for _, ns := range []string{a, b} {
				if len(ns) > 63 || !dns1123Label.MatchString(ns) {
					t.Errorf("NamespaceName = %q, not a DNS-1123 label of at most 63 characters", ns)
				}
			}
			if a == b {
				t.Errorf("NamespaceName returned %q twice", a)
			}
		})
	}
	t.Run("PrefixFromTestName", func(t *testing.T) {
		ns := envtest.NamespaceName(t)
		const want = "testnamespacenameisuniquedns1123label-prefixfromtest"
		if !strings.HasPrefix(ns, want+"-") {
			t.Errorf("NamespaceName = %q, want prefix %q", ns, want)
		}
	})
}

// spec: 12.2.4 (the envtest harness isolates tests sharing one control
// plane by namespace)
//
// diagnosis: Namespaces.For returned different names for one test, so a
//
//	test's fixtures and assertions would address different namespaces, or
//	returned the parent's name to a subtest, so sibling subtests would share
//	one namespace.
func TestNamespacesForIsStablePerTest(t *testing.T) {
	var n envtest.Namespaces
	parent := n.For(t)
	if again := n.For(t); again != parent {
		t.Fatalf("For(t) = %q then %q, want one name per test", parent, again)
	}
	t.Run("sub", func(t *testing.T) {
		if sub := n.For(t); sub == parent {
			t.Errorf("subtest got the parent's uncreated namespace %q", sub)
		}
	})
}

// spec: 12.2.4 (the envtest harness isolates tests sharing one control
// plane by namespace)
//
// diagnosis: a subtest running against the client its parent built did not
//
//	inherit the namespace the parent created, so its fixtures and lookups
//	address a namespace that does not exist; or Create did not create the
//	namespace on the API server.
func TestNamespacesSubtestInheritsCreatedNamespace(t *testing.T) {
	c := ctrlfake.NewClientBuilder().Build()
	var n envtest.Namespaces
	parent := n.Create(t, c)
	if err := c.Get(context.Background(), client.ObjectKey{Name: parent}, &corev1.Namespace{}); err != nil {
		t.Fatalf("Create did not create namespace %q: %v", parent, err)
	}
	t.Run("sub", func(t *testing.T) {
		if sub := n.For(t); sub != parent {
			t.Errorf("subtest namespace = %q, want the parent's created namespace %q", sub, parent)
		}
		if again := n.Create(t, c); again != parent {
			t.Errorf("Create in the subtest = %q, want the inherited %q", again, parent)
		}
	})
}

// spec: 12.2.4 (the shared control plane skips cleanly when the envtest
// binaries are absent)
//
// diagnosis: Shared.Start failed instead of skipping on a host with no
//
//	kube-apiserver binary, or Stop errored for a control plane that was
//	never started.
func TestSharedStartSkipsOnMissingAssets(t *testing.T) {
	t.Setenv("KUBEBUILDER_ASSETS", "/nonexistent/envtest/path")
	var shared envtest.Shared
	t.Run("inner", func(inner *testing.T) {
		defer func() {
			if inner.Failed() {
				t.Errorf("Shared.Start failed instead of skipping")
			}
			if !inner.Skipped() {
				t.Errorf("Shared.Start did not skip on missing assets")
			}
		}()
		shared.Start(inner)
	})
	if err := shared.Stop(); err != nil {
		t.Errorf("Stop of an unstarted control plane: %v", err)
	}
}
