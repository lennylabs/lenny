// SPDX-License-Identifier: MIT

package envtest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	crenvtest "sigs.k8s.io/controller-runtime/pkg/envtest"

	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// Shared is one envtest control plane that every test in a package binary
// reuses. Booting etcd and kube-apiserver and installing the CRDs takes
// about six seconds, so a package that called Start per test spent most of
// its wall time on control-plane starts (TEST-GAPS T-17.4.22). A package
// declares one Shared value, calls Start from each test, and calls Stop from
// TestMain after m.Run returns:
//
//	var sharedEnv envtest.Shared
//
//	func TestMain(m *testing.M) {
//		code := m.Run()
//		if err := sharedEnv.Stop(); err != nil {
//			fmt.Fprintf(os.Stderr, "envtest: stop shared API server: %v\n", err)
//		}
//		os.Exit(code)
//	}
//
// The control plane starts lazily on the first Start call, so a filtered run
// that selects no envtest-backed test never boots it. Tests sharing it see
// each other's objects, so each test isolates itself in its own namespace
// (NamespaceName and CreateNamespace). A test that creates cluster-scoped
// objects, or that relies on an empty cluster, keeps calling the package
// Start function for a private control plane instead.
//
// The control plane lives for the process, so a test cannot sweep it at
// start for a previous run's leftovers: a killed run takes its etcd with it.
type Shared struct {
	once sync.Once
	env  *crenvtest.Environment
	cfg  *rest.Config
	// assets is the binary directory the first Start resolved.
	assets string
	// err records a failed start, so every later Start reports the same
	// failure rather than retrying a start that already timed out.
	err error
}

// Start returns the shared control plane, booting it on the first call. It
// skips the test when the envtest binaries are not installed, and fails it
// when the control plane did not start.
func (s *Shared) Start(t testing.TB) *Environment {
	t.Helper()
	assets := SkipUnlessAvailable(t)
	s.once.Do(func() {
		// Pre-set the error so a Goexit out of RepoRoot leaves the value
		// in a failed state rather than a nil config.
		s.err = errors.New("shared control plane start did not complete")
		env := &crenvtest.Environment{
			CRDDirectoryPaths:     []string{filepath.Join(schematest.RepoRoot(t), "charts", "lenny", "crds")},
			ErrorIfCRDPathMissing: true,
			BinaryAssetsDirectory: assets,
		}
		cfg, err := env.Start()
		if err != nil {
			s.err = fmt.Errorf("start API server: %w", err)
			return
		}
		s.env, s.cfg, s.assets, s.err = env, cfg, assets, nil
	})
	if s.err != nil {
		t.Fatalf("envtest: shared control plane: %v", s.err)
	}
	return &Environment{cfg: s.cfg, assets: s.assets}
}

// Stop stops the shared control plane when it was started. It is safe to
// call when no test started it.
func (s *Shared) Stop() error {
	if s.env == nil {
		return nil
	}
	return s.env.Stop()
}

// maxNamespacePrefix bounds the part of a namespace name derived from the
// test name, leaving room for the random suffix within the 63-character
// DNS-1123 label limit.
const maxNamespacePrefix = 52

// NamespaceName returns a namespace name unique to this call, derived from
// t.Name() so a leftover object names the test that created it. The name is
// a valid DNS-1123 label: the test name is lowercased, every run of
// characters outside [a-z0-9] becomes one hyphen, the result is truncated,
// and a random hexadecimal suffix is appended.
func NamespaceName(t testing.TB) string {
	t.Helper()
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(t.Name()) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			dash = false
			continue
		}
		if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	prefix := b.String()
	if len(prefix) > maxNamespacePrefix {
		prefix = prefix[:maxNamespacePrefix]
	}
	prefix = strings.Trim(prefix, "-")
	if prefix == "" {
		prefix = "test"
	}
	var suffix [3]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		t.Fatalf("envtest: namespace suffix: %v", err)
	}
	return prefix + "-" + hex.EncodeToString(suffix[:])
}

// Namespaces assigns each test one namespace name for its lifetime, so the
// fixtures, the component under test, and the assertions of one test agree
// on the namespace without threading a value through every helper that
// already receives the test. The zero value is ready to use, and its methods
// are safe for concurrent use.
//
// A subtest inherits the namespace of its nearest ancestor that has already
// created one (Create), because such a subtest runs against the client its
// parent built. Otherwise a subtest gets its own name, so a table whose
// subtests each build a client starts each one from an empty namespace.
type Namespaces struct {
	mu sync.Mutex
	// byName maps a live test's full name to its assignment. For adds an
	// entry, Create marks it created, and the test's cleanup removes it.
	byName map[string]*nsEntry
}

// nsEntry is one test's namespace assignment.
type nsEntry struct {
	name string
	// created records that Create made the namespace on the API server,
	// which is what lets a subtest inherit it.
	created bool
	// inherited records that the name belongs to an ancestor, so Create
	// does not register a second cleanup for it.
	inherited bool
}

// For returns the namespace name of test t: the name already assigned to t,
// else the namespace an ancestor created, else a name minted with
// NamespaceName. It only names the namespace; Create creates it.
func (n *Namespaces) For(t testing.TB) string {
	t.Helper()
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.entryLocked(t).name
}

// Create creates the namespace of test t through c, as For names it, and
// returns the name. The creation is idempotent, so a test that builds two
// clients, or a subtest that inherits its parent's namespace, may call it
// again.
func (n *Namespaces) Create(t testing.TB, c client.Client) string {
	t.Helper()
	n.mu.Lock()
	e := n.entryLocked(t)
	inherited := e.inherited
	e.created = true
	n.mu.Unlock()
	if inherited {
		return e.name
	}
	CreateNamespace(t, c, e.name)
	return e.name
}

// entryLocked returns the assignment of t, adding it when absent. n.mu is
// held.
func (n *Namespaces) entryLocked(t testing.TB) *nsEntry {
	key := t.Name()
	if e, ok := n.byName[key]; ok {
		return e
	}
	if n.byName == nil {
		n.byName = map[string]*nsEntry{}
	}
	e := &nsEntry{}
	for anc := key; strings.Contains(anc, "/"); {
		anc = anc[:strings.LastIndex(anc, "/")]
		if pe, ok := n.byName[anc]; ok && pe.created {
			e.name, e.created, e.inherited = pe.name, true, true
			break
		}
	}
	if e.name == "" {
		e.name = NamespaceName(t)
	}
	n.byName[key] = e
	t.Cleanup(func() {
		n.mu.Lock()
		defer n.mu.Unlock()
		delete(n.byName, key)
	})
	return e
}

// namespaceCleanupTimeout bounds the best-effort namespace delete at test
// cleanup, so a slow API server cannot stall the next test.
const namespaceCleanupTimeout = 10 * time.Second

// CreateNamespace creates the namespace name through c and registers a
// best-effort delete in t.Cleanup. envtest runs no namespace controller, so
// the delete only moves the namespace to Terminating and its objects stay
// in etcd until the control plane stops. Isolation therefore rests on the
// unique name rather than on the delete.
func CreateNamespace(t testing.TB, c client.Client, name string) {
	t.Helper()
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}
	if err := c.Create(context.Background(), ns); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatalf("envtest: create namespace %s: %v", name, err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), namespaceCleanupTimeout)
		defer cancel()
		if err := c.Delete(ctx, ns); err != nil && !apierrors.IsNotFound(err) {
			t.Logf("envtest: delete namespace %s: %v", name, err)
		}
	})
}
