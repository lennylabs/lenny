// SPDX-License-Identifier: MIT

package stack_test

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	lennyv1alpha1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/tests/testinfra/envtest"
)

// sharedEnv is the one envtest control plane every envtest-backed test in
// this package binary reuses. Starting one per test cost about six seconds
// each, most of the package's wall time (TEST-GAPS T-17.4.23).
//
// Tests sharing it see each other's objects, so each test isolates itself
// one of two ways.
//
//   - Where the code under test takes the namespace as an argument, or the
//     test supplies the manifest, the test works in its own namespace
//     (testNamespace). The apply fixtures name that namespace as their
//     cluster-scoped Namespace object, so each test's Namespace is unique,
//     and each fixture Service pins a NodePort unique to the test
//     (testNodePort), because NodePorts are allocated cluster-wide.
//   - Where the product code fixes the namespace and the object name (the
//     control-plane Deployments and Secrets in "default", the echo
//     SandboxWarmPool in "lenny-agents", and the runtime-apply pool pair),
//     exactly one test owns each such object, and claimFixedObjects fails a
//     second test that claims it and deletes a copy an earlier iteration
//     left, so the owner starts from an absent object.
//
// The echo Runtime is the one fixed-name object two tests write. Both apply
// it through an upsert and assert only on the values they wrote, and the
// reconvergence test deletes it at start so its first apply is a create.
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

// testNamespace returns the namespace test t works in. It only names the
// namespace: a test either creates it through the code under test (the
// agent-namespace ensure, or the Namespace object in an apply fixture) or
// through createTestNamespace.
func testNamespace(t *testing.T) string {
	t.Helper()
	return testNamespaces.For(t)
}

// createTestNamespace creates test t's namespace on the shared control plane
// and returns its name, for a test whose code under test writes into a
// namespace it does not create.
func createTestNamespace(t *testing.T, cfg *rest.Config) string {
	t.Helper()
	return testNamespaces.Create(t, fixedObjectClient(t, cfg))
}

// nodePortMu guards nextNodePort and nodePorts.
var nodePortMu sync.Mutex

// nextNodePort is the next NodePort testNodePort hands out. The range starts
// at the port the embedded chart renders for the gateway Service and stays
// inside the default 30000-32767 NodePort range for any realistic test count.
var nextNodePort = 30080

// nodePorts maps a test's namespace to the NodePort it was given.
var nodePorts = map[string]int{}

// testNodePort returns the NodePort test t's fixture Service pins: the same
// port on every call in t, and a port no other test in this process was
// given. The shared API server allocates NodePorts cluster-wide, so two tests
// applying a Service with the same pinned NodePort would collide even in
// different namespaces. The map is keyed by the test's namespace, which is
// unique per test and per iteration under go test -count, and a port is
// never reused, because the cleanup's namespace delete does not remove the
// Service (envtest runs no namespace controller).
func testNodePort(t *testing.T) int {
	t.Helper()
	ns := testNamespace(t)
	nodePortMu.Lock()
	defer nodePortMu.Unlock()
	if p, ok := nodePorts[ns]; ok {
		return p
	}
	p := nextNodePort
	nextNodePort++
	nodePorts[ns] = p
	return p
}

// fixedOwnersMu guards fixedOwners.
var fixedOwnersMu sync.Mutex

// fixedOwners maps a fixed-name object's key to the top-level test that owns
// it on the shared control plane.
var fixedOwners = map[string]string{}

// fixedObjectDeleteTimeout bounds the wait for a leftover fixed-name object to
// disappear after its delete.
const fixedObjectDeleteTimeout = 10 * time.Second

// claimFixedObjects records test t as the owner of each object, whose
// namespace and name the product code fixes, and deletes any copy of it left
// by an earlier iteration of the same test in this process (go test -count).
// It fails t when another test already claimed the object, because that test
// would see or overwrite t's object on the shared control plane. Each object
// needs only its kind's Go type, name, and namespace set.
func claimFixedObjects(t *testing.T, cfg *rest.Config, objs ...ctrlclient.Object) {
	t.Helper()
	cl := fixedObjectClient(t, cfg)
	owner := rootTestName(t)
	for _, o := range objs {
		key := fmt.Sprintf("%T %s/%s", o, o.GetNamespace(), o.GetName())
		fixedOwnersMu.Lock()
		prev, claimed := fixedOwners[key]
		if !claimed {
			fixedOwners[key] = owner
		}
		fixedOwnersMu.Unlock()
		if claimed && prev != owner {
			t.Fatalf("fixed-name object %s is owned by %s; a second test writing it shares state on the shared control plane", key, prev)
		}
		deleteFixedObject(t, cl, key, o)
	}
}

// deleteFixedObject deletes o and waits until the API server no longer
// returns it.
func deleteFixedObject(t *testing.T, cl ctrlclient.Client, key string, o ctrlclient.Object) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), fixedObjectDeleteTimeout)
	defer cancel()
	err := cl.Delete(ctx, o, ctrlclient.GracePeriodSeconds(0), ctrlclient.PropagationPolicy("Background"))
	if apierrors.IsNotFound(err) {
		return
	}
	if err != nil {
		t.Fatalf("delete leftover %s: %v", key, err)
	}
	for {
		err := cl.Get(ctx, ctrlclient.ObjectKeyFromObject(o), o)
		if apierrors.IsNotFound(err) {
			return
		}
		if ctx.Err() != nil {
			t.Fatalf("leftover %s still present after delete: %v", key, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// rootTestName returns the name of t's top-level test.
func rootTestName(t *testing.T) string {
	root, _, _ := strings.Cut(t.Name(), "/")
	return root
}

// fixedObjectClient builds a controller-runtime client carrying the core,
// apps, and lenny.dev schemes for the shared control plane.
func fixedObjectClient(t *testing.T, cfg *rest.Config) ctrlclient.Client {
	t.Helper()
	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(lennyv1alpha1.AddToScheme(scheme))
	cl, err := ctrlclient.New(cfg, ctrlclient.Options{Scheme: scheme})
	if err != nil {
		t.Fatalf("build fixed-object client: %v", err)
	}
	return cl
}
