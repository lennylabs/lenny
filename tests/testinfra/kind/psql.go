// SPDX-License-Identifier: MIT

package kind

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// The one-shot psql helpers run a SQL script against the e2e Postgres from
// a throwaway postgres:16.4 pod. The pod connects to the Postgres pod IP
// rather than the Service DNS name, because the lenny-system default-deny
// NetworkPolicy denies a throwaway pod's egress to CoreDNS. ON_ERROR_STOP
// makes any failed statement a non-zero exit.
//
// `kubectl run --attach` can return before it has attached to a container
// that exits quickly, and it then reports success with no output. For a
// query that reads as an empty result set, which turns into a false skip
// or a false failure. RunPsqlQuery therefore appends a marker row to every
// query and retries a run whose output lacks it (TEST-GAPS T-17.4.45). A
// script run through RunPsqlExec is not retried, because its output is not
// read and a retry of a non-idempotent script (an INSERT seed) would fail
// on the rows the first run committed.

// PsqlImage is the client image the one-shot psql pods run.
const PsqlImage = "postgres:16.4"

// PsqlEndMarker is the row RunPsqlQuery appends to every query, so a run
// whose output was lost is told apart from an empty result.
const PsqlEndMarker = "LENNY_PSQL_END"

// psqlQueryAttempts bounds how many times RunPsqlQuery reruns a query whose
// output lacks the end marker.
const psqlQueryAttempts = 3

// errPsqlMarkerMissing reports that every attempt returned without the end
// marker.
var errPsqlMarkerMissing = errors.New("psql output lacks the end marker")

// StripPsqlEndMarker removes the end-marker line from psql output and
// reports whether the marker was present.
func StripPsqlEndMarker(out string) (string, bool) {
	lines := strings.Split(out, "\n")
	kept := lines[:0]
	found := false
	for _, line := range lines {
		if strings.TrimSpace(line) == PsqlEndMarker {
			found = true
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n"), found
}

// withPsqlEndMarker terminates sql and appends the end-marker SELECT. With
// ON_ERROR_STOP the marker prints only after every preceding statement
// succeeded.
func withPsqlEndMarker(sql string) string {
	sql = strings.TrimSpace(sql)
	if !strings.HasSuffix(sql, ";") {
		sql += ";"
	}
	return sql + " SELECT '" + PsqlEndMarker + "';"
}

// runUntilMarked calls run up to attempts times and returns the first
// output that carries the end marker, with the marker line removed. A run
// error stops the loop and is returned with that run's output. When no
// attempt carries the marker, it returns the last output and
// errPsqlMarkerMissing. onRetry, when non-nil, is called before each rerun.
func runUntilMarked(attempts int, run func() (string, error), onRetry func(attempt int)) (string, error) {
	var out string
	for i := 0; i < attempts; i++ {
		var err error
		out, err = run()
		if err != nil {
			return out, err
		}
		if body, ok := StripPsqlEndMarker(out); ok {
			return body, nil
		}
		if onRetry != nil && i+1 < attempts {
			onRetry(i + 1)
		}
	}
	return out, errPsqlMarkerMissing
}

// psqlRunArgs returns the `kubectl run` arguments for a one-shot psql pod.
func psqlRunArgs(namespace, podName, pgIP string, psqlFlags ...string) []string {
	args := []string{
		"-n", namespace, "run", podName,
		"--image=" + PsqlImage, "--image-pull-policy=IfNotPresent",
		"--restart=Never", "--attach", "--rm", "--quiet",
		"--env=PGPASSWORD=lenny",
		"--command", "--",
		"psql", "-h", pgIP, "-U", "lenny", "-d", "lenny",
	}
	return append(args, psqlFlags...)
}

// runPsqlPod deletes any leftover pod named podName and runs one psql pod.
func (c *Cluster) runPsqlPod(t testing.TB, namespace, podName, pgIP string, psqlFlags ...string) (string, error) {
	t.Helper()
	_, _ = c.KubectlOut(t, "-n", namespace, "delete", "pod", podName,
		"--ignore-not-found", "--wait=true")
	return c.KubectlOut(t, psqlRunArgs(namespace, podName, pgIP, psqlFlags...)...)
}

// RunPsqlQuery runs a read-only SQL query against the e2e Postgres at pgIP
// from a one-shot pod in namespace and returns the rows as raw lines (psql
// -tA: tuples only, unaligned). It retries a run whose output lost the end
// marker, and fails the test on a query error or when every attempt lost
// its output. The query must be safe to rerun.
func (c *Cluster) RunPsqlQuery(t testing.TB, namespace, pgIP, podName, sql string) string {
	t.Helper()
	query := withPsqlEndMarker(sql)
	out, err := runUntilMarked(psqlQueryAttempts, func() (string, error) {
		return c.runPsqlPod(t, namespace, podName, pgIP, "-tA", "-v", "ON_ERROR_STOP=1", "-c", query)
	}, func(attempt int) {
		t.Logf("psql query %q returned without its end marker (attempt %d of %d); retrying",
			podName, attempt, psqlQueryAttempts)
	})
	switch {
	case errors.Is(err, errPsqlMarkerMissing):
		t.Fatalf("psql query %q returned without its end marker in %d attempts; output %q",
			podName, psqlQueryAttempts, out)
	case err != nil:
		t.Fatalf("psql query %q failed: %v\n%s", podName, err, out)
	}
	return out
}

// RunPsqlExec runs a SQL script against the e2e Postgres at pgIP from a
// one-shot pod in namespace and fails the test on a non-zero exit. A seed
// or cleanup that did not apply is not a recoverable condition. The
// script runs once; see the package comment above for why it is not
// retried.
func (c *Cluster) RunPsqlExec(t testing.TB, namespace, pgIP, podName, sql string) {
	t.Helper()
	if out, err := c.runPsqlPod(t, namespace, podName, pgIP, "-v", "ON_ERROR_STOP=1", "-c", sql); err != nil {
		t.Fatalf("psql script %q failed: %v\n%s", podName, err, out)
	}
}

// RunAttachedUntil runs `kubectl run --attach` with runArgs up to attempts
// times, deleting any leftover pod named podName before each run, until
// complete reports the output whole. It returns the last output and run
// error. It serves one-shot pods other than psql (a `lenny-migrate
// version` read) whose output can be lost the same way. The command must
// be safe to rerun.
func (c *Cluster) RunAttachedUntil(t testing.TB, namespace, podName string, attempts int,
	complete func(out string, err error) bool, runArgs ...string,
) (string, error) {
	t.Helper()
	if attempts < 1 {
		return "", fmt.Errorf("RunAttachedUntil %q: attempts %d < 1", podName, attempts)
	}
	var (
		out string
		err error
	)
	for i := 0; i < attempts; i++ {
		_, _ = c.KubectlOut(t, "-n", namespace, "delete", "pod", podName,
			"--ignore-not-found", "--wait=true")
		out, err = c.KubectlOut(t, append([]string{"-n", namespace, "run", podName}, runArgs...)...)
		if complete(out, err) {
			return out, err
		}
		if i+1 < attempts {
			t.Logf("one-shot pod %q returned incomplete output (attempt %d of %d); retrying",
				podName, i+1, attempts)
		}
	}
	return out, err
}
