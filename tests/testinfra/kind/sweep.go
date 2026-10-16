// SPDX-License-Identifier: MIT

package kind

import (
	"strings"
	"sync"
	"testing"
)

// sweepOnce records which (namespace, selector) pairs this process has swept.
// The sweep reclaims objects a killed earlier run left behind; running it once
// per process, before the first fixture of the run creates anything, is what
// keeps it from deleting objects this run created.
var sweepOnce sync.Map // map[string]*sync.Once

// SweepLabeledOnce is the start-time sweep the environment preflight requires
// of a fixture that creates cluster state (check 6 in
// .claude/rules/test-coverage.md). A run killed by the harness watchdog or a
// wall-clock timeout never runs its t.Cleanup, so the objects its fixture
// created stay on the long-lived cluster, and any later test waiting on them
// waits until its timeout.
//
// The first call per (namespace, selector) pair in a process deletes every
// object of each named resource type in namespace that matches selector. The
// selector must be the fixture's own label (for example
// lenny.dev/test=chaos-concurrency), so the sweep never touches state another
// fixture or the platform owns. A SandboxClaim the sweep selects has its
// finalizers cleared first: a fixture that parks a claim behind a test
// finalizer would otherwise leave it Terminating forever. Later calls in the
// same process are no-ops, because every object then matching the selector
// belongs to this run.
//
// The sweep is best-effort and never fails the test: a list, patch, or delete
// error is logged and the test continues against its own fixtures. Each
// reclaimed object is logged by name.
func SweepLabeledOnce(t testing.TB, c *Cluster, namespace, selector string, resources ...string) {
	t.Helper()
	key := namespace + "\x00" + selector
	once, _ := sweepOnce.LoadOrStore(key, &sync.Once{})
	once.(*sync.Once).Do(func() {
		for _, res := range resources {
			sweepResource(t, c, namespace, selector, res)
		}
	})
}

// sweepResource deletes every object of one resource type matching selector.
func sweepResource(t testing.TB, c *Cluster, namespace, selector, resource string) {
	t.Helper()
	out, err := c.KubectlOut(t, "-n", namespace, "get", resource, "-l", selector,
		"--ignore-not-found", "-o", "name")
	if err != nil {
		t.Logf("start-time sweep: listing %s with %s in %s failed (continuing): %v\n%s",
			resource, selector, namespace, err, out)
		return
	}
	names := strings.Fields(strings.TrimSpace(out))
	if len(names) == 0 {
		return
	}
	if resource == "sandboxclaims" || strings.HasPrefix(resource, "sandboxclaims.") {
		for _, name := range names {
			if pout, perr := c.KubectlOut(t, "-n", namespace, "patch", name, "--type=merge",
				"-p", `{"metadata":{"finalizers":null}}`); perr != nil {
				t.Logf("start-time sweep: clearing finalizers on %s failed (continuing): %v\n%s", name, perr, pout)
			}
		}
	}
	if dout, derr := c.KubectlOut(t, "-n", namespace, "delete", resource, "-l", selector,
		"--ignore-not-found", "--wait=false"); derr != nil {
		t.Logf("start-time sweep: deleting %s with %s in %s failed (continuing): %v\n%s",
			resource, selector, namespace, derr, dout)
		return
	}
	t.Logf("start-time sweep: reclaimed %d leftover %s from an earlier run (%s in %s): %s",
		len(names), resource, selector, namespace, strings.Join(names, ", "))
}
