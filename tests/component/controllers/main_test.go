// SPDX-License-Identifier: MIT

//go:build component

package controllers_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	nodev1 "k8s.io/api/node/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/lennylabs/lenny/tests/testinfra/envtest"
)

// sharedEnv is the one envtest control plane every envtest-backed test in
// this package binary reuses. Starting one per test cost seven to ten
// seconds each under -race, most of the package's wall time (TEST-GAPS
// T-17.4.24).
//
// Tests sharing it see each other's objects, so each test isolates itself
// as follows.
//
//   - Namespaced fixtures live in the test's own namespace
//     (testNamespaces), named from t.Name() with a random suffix. Every
//     reconciler and product call in the package reads and lists within the
//     namespace it is given, so a test never observes another test's
//     namespaced objects.
//   - Cluster-scoped Runtime fixtures carry a name unique to the test
//     (clusterName), and claimClusterName fails a test that names an object
//     another test already claimed.
//   - The `lenny-agents` Namespace, which the runtime-apply verb fixes, is
//     claimed by the one test that drives that verb.
//   - RuntimeClasses have fixed names (`runc`, `gvisor`, `kata`) because the
//     isolation profiles resolve to them, and the embedded apply tests
//     assert behavior with `runc` absent. A test that needs one claims it
//     with claimRuntimeClasses, which fails when the RuntimeClass already
//     exists (a test leaked it) or another live test holds it, and deletes
//     it at cleanup, so every test starts with no RuntimeClass.
//
// No test keeps a private control plane. TestEtcdUnavailableDegradedMode
// reproduces the API-server outage with a second client pointed at a closed
// loopback port (unreachableConfig) and leaves the API server itself
// running, so the outage is invisible to every other client of the shared
// control plane.
//
// No envtest-backed test runs t.Parallel(); the claims above assume tests
// run one after another, and claimRuntimeClasses fails a test that would
// overlap with another holding the same RuntimeClass.
var sharedEnv envtest.Shared

// testNamespaces holds the namespace each test was assigned.
var testNamespaces envtest.Namespaces

func TestMain(m *testing.M) {
	code := m.Run()
	if err := sharedEnv.Stop(); err != nil {
		fmt.Fprintf(os.Stderr, "envtest: stop shared API server: %v\n", err)
	}
	os.Exit(code)
}

// clusterOwnersMu guards clusterOwners.
var clusterOwnersMu sync.Mutex

// clusterOwners maps a cluster-scoped object's kind and name to the
// top-level test that claimed it on the shared control plane.
var clusterOwners = map[string]string{}

// claimClusterName records test t as the owner of the cluster-scoped object
// of the given kind and name, and fails t when another test already claimed
// it, because that test would see or overwrite t's object on the shared
// control plane. Claims persist for the process, so a second iteration of
// the same test under go test -count may reclaim its own name.
func claimClusterName(t *testing.T, kind, name string) {
	t.Helper()
	key := kind + "/" + name
	owner := rootTestName(t)
	clusterOwnersMu.Lock()
	prev, claimed := clusterOwners[key]
	if !claimed {
		clusterOwners[key] = owner
	}
	clusterOwnersMu.Unlock()
	if claimed && prev != owner {
		t.Fatalf("cluster-scoped %s is owned by %s; a second test writing it shares state on the shared control plane", key, prev)
	}
}

// clusterName returns a name for a cluster-scoped fixture of the given kind:
// base followed by a random hexadecimal suffix, claimed for test t. The
// suffix keeps the name unique across tests and across go test -count
// iterations, and keeps a name that matched the §5.1 registry-name pattern
// matching it, and one that did not still not matching it.
func clusterName(t *testing.T, kind, base string) string {
	t.Helper()
	var suffix [3]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		t.Fatalf("cluster name suffix: %v", err)
	}
	name := base + "-" + hex.EncodeToString(suffix[:])
	claimClusterName(t, kind, name)
	return name
}

// ensureFixedNamespace creates the namespace name, which the product code
// fixes, after claiming it for test t. It does not delete the namespace at
// cleanup: envtest runs no namespace controller, so a deleted namespace
// stays Terminating and refuses the next iteration's creates.
func ensureFixedNamespace(t *testing.T, c client.Client, name string) {
	t.Helper()
	claimClusterName(t, "Namespace", name)
	if err := c.Create(context.Background(), &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatalf("create namespace %s: %v", name, err)
	}
}

// runtimeClassHoldersMu guards runtimeClassHolders.
var runtimeClassHoldersMu sync.Mutex

// runtimeClassHolders maps a RuntimeClass name to the live test holding it.
var runtimeClassHolders = map[string]string{}

// runtimeClassDeleteTimeout bounds the wait for a RuntimeClass delete at
// cleanup.
const runtimeClassDeleteTimeout = 10 * time.Second

// claimRuntimeClasses gives test t exclusive use of the named RuntimeClasses
// for its lifetime. It fails t when another live test holds one, or when one
// already exists, which means an earlier test leaked it; either would let a
// test observe a RuntimeClass it did not create. At cleanup it deletes each
// RuntimeClass, waits until it is gone, and releases the claim. The caller
// creates the RuntimeClasses it needs after the claim.
func claimRuntimeClasses(t *testing.T, c client.Client, names ...string) {
	t.Helper()
	ctx := context.Background()
	owner := t.Name()
	for _, name := range names {
		runtimeClassHoldersMu.Lock()
		prev, held := runtimeClassHolders[name]
		if !held {
			runtimeClassHolders[name] = owner
		}
		runtimeClassHoldersMu.Unlock()
		if held {
			t.Fatalf("RuntimeClass %s is held by the live test %s; tests sharing the control plane must not overlap on it", name, prev)
		}
		t.Cleanup(func() {
			deleteRuntimeClass(t, c, name)
			runtimeClassHoldersMu.Lock()
			delete(runtimeClassHolders, name)
			runtimeClassHoldersMu.Unlock()
		})
		err := c.Get(ctx, client.ObjectKey{Name: name}, &nodev1.RuntimeClass{})
		if err == nil {
			t.Fatalf("RuntimeClass %s exists before this test created it; an earlier test leaked it on the shared control plane", name)
		}
		if !apierrors.IsNotFound(err) {
			t.Fatalf("get RuntimeClass %s: %v", name, err)
		}
	}
}

// deleteRuntimeClass deletes the RuntimeClass name and waits until the API
// server no longer returns it.
func deleteRuntimeClass(t *testing.T, c client.Client, name string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), runtimeClassDeleteTimeout)
	defer cancel()
	rc := &nodev1.RuntimeClass{ObjectMeta: metav1.ObjectMeta{Name: name}}
	if err := c.Delete(ctx, rc); err != nil && !apierrors.IsNotFound(err) {
		t.Errorf("delete RuntimeClass %s: %v", name, err)
		return
	}
	for {
		err := c.Get(ctx, client.ObjectKey{Name: name}, &nodev1.RuntimeClass{})
		if apierrors.IsNotFound(err) {
			return
		}
		if ctx.Err() != nil {
			t.Errorf("RuntimeClass %s still present after delete: %v", name, err)
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// rootTestName returns the name of t's top-level test.
func rootTestName(t *testing.T) string {
	root, _, _ := strings.Cut(t.Name(), "/")
	return root
}
