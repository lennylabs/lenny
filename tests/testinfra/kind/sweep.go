// SPDX-License-Identifier: MIT

package kind

import (
	"fmt"
	"regexp"
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
// object of each named resource type in namespace that matches selector. An
// empty namespace selects a cluster-scoped resource type. The selector must
// be the fixture's own label (for example lenny.dev/test=chaos-concurrency),
// so the sweep never touches state another fixture or the platform owns. A
// SandboxClaim the sweep selects has its finalizers cleared first: a fixture
// that parks a claim behind a test finalizer would otherwise leave it
// Terminating forever. Later calls in the same process are no-ops, because
// every object then matching the selector belongs to this run.
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

// namespaceArgs scopes a kubectl invocation to namespace. An empty namespace
// selects a cluster-scoped resource type (such as runtimes), so no -n flag is
// passed.
func namespaceArgs(namespace string, args ...string) []string {
	if namespace == "" {
		return args
	}
	return append([]string{"-n", namespace}, args...)
}

// sweepResource deletes every object of one resource type matching selector.
func sweepResource(t testing.TB, c *Cluster, namespace, selector, resource string) {
	t.Helper()
	out, err := c.KubectlOut(t, namespaceArgs(namespace, "get", resource, "-l", selector,
		"--ignore-not-found", "-o", "name")...)
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
			if pout, perr := c.KubectlOut(t, namespaceArgs(namespace, "patch", name, "--type=merge",
				"-p", `{"metadata":{"finalizers":null}}`)...); perr != nil {
				t.Logf("start-time sweep: clearing finalizers on %s failed (continuing): %v\n%s", name, perr, pout)
			}
		}
	}
	if dout, derr := c.KubectlOut(t, namespaceArgs(namespace, "delete", resource, "-l", selector,
		"--ignore-not-found", "--wait=false")...); derr != nil {
		t.Logf("start-time sweep: deleting %s with %s in %s failed (continuing): %v\n%s",
			resource, selector, namespace, derr, dout)
		return
	}
	t.Logf("start-time sweep: reclaimed %d leftover %s from an earlier run (%s in %s): %s",
		len(names), resource, selector, namespace, strings.Join(names, ", "))
}

// SweepNamedOnce is the start-time sweep for a fixture whose objects carry no
// label of the fixture's own, so they can be recognized only by name. Objects
// a platform controller derives from a fixture's request are the usual case:
// the PoolScalingController names a pool's SandboxTemplate and
// SandboxWarmPool after the pool and stamps only platform labels on them.
//
// The first call per (namespace, pattern) pair in a process lists each named
// resource type in namespace, in the order given, and deletes every object
// whose name StaleNames selects. The caller orders resources so a dependent
// object goes before the object it references (a SandboxWarmPool before its
// SandboxTemplate), and each delete waits for the objects to leave the API
// server before the next type is swept. The wait matters for a template: the
// sandboxtemplate-deletion-guard admits a template delete without consulting
// the gateway only once no SandboxWarmPool references the template.
//
// The pattern must be anchored on the fixture's own name prefix, so the sweep
// never touches state another fixture or the platform owns. keep names this
// run's own objects; the sweep runs before the fixture creates anything, so
// keep normally matches nothing and guards against a call placed too late.
// Like SweepLabeledOnce, the sweep is best-effort, logs each reclaimed object,
// and never fails the test.
func SweepNamedOnce(t testing.TB, c *Cluster, namespace string, pattern *regexp.Regexp, keep []string, resources ...string) {
	t.Helper()
	key := namespace + "\x00name:" + pattern.String()
	once, _ := sweepOnce.LoadOrStore(key, &sync.Once{})
	once.(*sync.Once).Do(func() {
		for _, res := range resources {
			sweepNamedResource(t, c, namespace, pattern, keep, res)
		}
	})
}

// sweepNamedResource deletes every object of one resource type whose name
// StaleNames selects.
func sweepNamedResource(t testing.TB, c *Cluster, namespace string, pattern *regexp.Regexp, keep []string, resource string) {
	t.Helper()
	out, err := c.KubectlOut(t, namespaceArgs(namespace, "get", resource, "--ignore-not-found",
		"-o", `jsonpath={range .items[*]}{.metadata.name}{"\n"}{end}`)...)
	if err != nil {
		t.Logf("start-time sweep: listing %s in %q failed (continuing): %v\n%s", resource, namespace, err, out)
		return
	}
	stale := StaleNames(strings.Fields(out), pattern, keep...)
	if len(stale) == 0 {
		return
	}
	if err := DeleteNamed(t, c, namespace, resource, stale...); err != nil {
		t.Logf("start-time sweep: %v (continuing)", err)
		return
	}
	for _, name := range stale {
		t.Logf("start-time sweep: reclaimed leftover %s %s from an earlier run (namespace %q)", resource, name, namespace)
	}
}

// StaleNames returns the names that pattern matches in full and keep does not
// list, in input order and without duplicates. It is the selection step of
// SweepNamedOnce, kept free of cluster I/O so it can be unit tested. A match
// that covers only part of a name does not select it, so a pattern written as
// prefix plus fixed suffix cannot select a longer name that merely contains
// the fixture's prefix (a member Sandbox named after its pool, or another
// suite's object that shares the stem).
func StaleNames(names []string, pattern *regexp.Regexp, keep ...string) []string {
	skip := make(map[string]bool, len(keep)+len(names))
	for _, k := range keep {
		skip[k] = true
	}
	var stale []string
	for _, name := range names {
		if skip[name] {
			continue
		}
		if loc := pattern.FindStringIndex(name); loc == nil || loc[0] != 0 || loc[1] != len(name) {
			continue
		}
		skip[name] = true
		stale = append(stale, name)
	}
	return stale
}

// DeleteNamed deletes the named objects of one resource type and waits up to
// a minute for them to leave the API server. An object already absent is not
// an error. The returned error carries kubectl's output, so a caller that logs
// it shows why the delete was refused (an admission webhook denial, for
// example).
func DeleteNamed(t testing.TB, c *Cluster, namespace, resource string, names ...string) error {
	t.Helper()
	if len(names) == 0 {
		return nil
	}
	args := append([]string{"delete", resource}, names...)
	args = append(args, "--ignore-not-found", "--wait=true", "--timeout=60s")
	if out, err := c.KubectlOut(t, namespaceArgs(namespace, args...)...); err != nil {
		return fmt.Errorf("deleting %s %s in %q: %w\n%s", resource, strings.Join(names, ", "), namespace, err, out)
	}
	return nil
}
