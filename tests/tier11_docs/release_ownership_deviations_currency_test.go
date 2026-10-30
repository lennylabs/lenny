// SPDX-License-Identifier: MIT

// Tier-11 consistency check between the deviations record of the
// release-ownership work (proposal 0093) and the tree it describes.
//
// The deviations record tells a later reader where an implementation departed
// from the proposal and where the landed code lives instead. The full_revoke
// lease step moved from package main into a library package after its entry
// was written, and the entry went on naming a type the tree no longer held.
// This check holds the entry to the tree: the symbol it names as the lease
// step is declared in the file it names, every Go file it cites exists, and
// every type it says no longer exists is absent from the gateway binary.
//
// Like the BUILD-GAPS records checks beside it, this asserts a documentation
// contract rather than a platform behavior, so it cites no specification
// section.

package tier11_docs_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// releaseOwnershipDeviations is the deviations record of the release-ownership
// proposal, relative to the repository root.
const releaseOwnershipDeviations = releaseOwnershipProposal +
	"0093_fix_release-ownership-for-pods-concurrent-workspace-slots-per-po.deviations.md"

// leaseStepClaim matches the sentence that names the full_revoke lease step's
// declaration and the file that holds it.
var leaseStepClaim = regexp.MustCompile("lease step is `([A-Za-z_]\\w*(?:\\.[A-Za-z_]\\w*)?)` in `([^`]+\\.go)`")

// goneTypesClaim matches the sentence that names the types the move retired.
var goneTypesClaim = regexp.MustCompile("`([A-Za-z_]\\w*)` and `([A-Za-z_]\\w*)` no longer exist")

// citedGoFile matches a backticked repository-relative Go file path.
var citedGoFile = regexp.MustCompile("`((?:pkg|cmd|tests)/[^`]+\\.go)`")

// deviationEntries splits a deviations record into its level-2 sections,
// keyed by heading line.
func deviationEntries(record string) map[string]string {
	entries := map[string]string{}
	var heading string
	var body strings.Builder
	flush := func() {
		if heading != "" {
			entries[heading] = body.String()
		}
		body.Reset()
	}
	for _, line := range strings.Split(record, "\n") {
		if strings.HasPrefix(line, "## ") {
			flush()
			heading = line
			continue
		}
		body.WriteString(line)
		body.WriteByte('\n')
	}
	flush()
	return entries
}

// fullRevokeLeaseEntry returns the one deviations entry whose heading names
// the full_revoke lease step, failing the test when there is not exactly one.
func fullRevokeLeaseEntry(t *testing.T, record string) (string, string) {
	t.Helper()
	var heading, body string
	n := 0
	for h, b := range deviationEntries(record) {
		if strings.Contains(h, "full_revoke lease") {
			heading, body = h, b
			n++
		}
	}
	if n != 1 {
		t.Fatalf("%s carries %d entries on the full_revoke lease step, want 1", releaseOwnershipDeviations, n)
	}
	return heading, body
}

// declaredInDir reports whether any non-test Go file directly in dir declares
// symbol.
func declaredInDir(t *testing.T, dir, symbol string) bool {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		t.Fatalf("glob %s: %v", dir, err)
	}
	for _, p := range paths {
		if strings.HasSuffix(p, "_test.go") {
			continue
		}
		if declaresSymbol(parseGoFile(t, p), symbol) {
			return true
		}
	}
	return false
}

// diagnosis: the deviations entry for the full_revoke lease step no longer
// matches the tree. It names a lease-step symbol the named file does not
// declare, cites a Go file that does not exist, or says a type is gone that
// the gateway binary still declares. Update the entry to name where the lease
// step lives now, so a reader following the record finds the code it
// describes.
func TestReleaseOwnershipDeviationNamesTheLandedLeaseStep(t *testing.T) {
	root := repoRoot(t)
	heading, body := fullRevokeLeaseEntry(t, readDocPage(t, filepath.Join(root, filepath.FromSlash(releaseOwnershipDeviations))))

	m := leaseStepClaim.FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("%s\n  does not name the lease step's declaration and file", heading)
	}
	symbol, file := m[1], m[2]
	path := filepath.Join(root, filepath.FromSlash(file))
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("%s\n  names the lease step in %s, which does not exist", heading, file)
	}
	if !declaresSymbol(parseGoFile(t, path), symbol) {
		t.Errorf("%s\n  names %s as the lease step, but %s does not declare it", heading, symbol, file)
	}

	for _, c := range citedGoFile.FindAllStringSubmatch(body, -1) {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(c[1]))); err != nil {
			t.Errorf("%s\n  cites %s, which does not exist", heading, c[1])
		}
	}

	gone := goneTypesClaim.FindStringSubmatch(body)
	if gone == nil {
		t.Fatalf("%s\n  does not name the types the move retired", heading)
	}
	for _, name := range gone[1:] {
		if declaredInDir(t, filepath.Join(root, "cmd", "lenny-gateway"), name) {
			t.Errorf("%s\n  says %s no longer exists, but cmd/lenny-gateway declares it", heading, name)
		}
	}
}

// diagnosis: the entry parser or the claim patterns no longer read a
// deviations entry the way the check above assumes, so it passes or fails
// vacuously.
func TestReleaseOwnershipDeviationProbes(t *testing.T) {
	record := "# Deviations\n\nIntro.\n\n## Proposed: The full_revoke lease step is a library type\n\n" +
		"The lease step is `Revoker.RevokeUserLeases` in `pkg/a/b.go`. `x` and `y` no longer exist.\n\n" +
		"## Proposed: Something else\n\nThe lease step is `Other` in `cmd/c.go`.\n"
	entries := deviationEntries(record)
	if len(entries) != 2 {
		t.Fatalf("deviationEntries found %d entries, want 2", len(entries))
	}
	body := entries["## Proposed: The full_revoke lease step is a library type"]
	if strings.Contains(body, "cmd/c.go") {
		t.Error("deviationEntries let one entry's body run into the next")
	}
	m := leaseStepClaim.FindStringSubmatch(body)
	if m == nil || m[1] != "Revoker.RevokeUserLeases" || m[2] != "pkg/a/b.go" {
		t.Errorf("leaseStepClaim = %q, want the method and its file", m)
	}
	if g := goneTypesClaim.FindStringSubmatch(body); g == nil || g[1] != "x" || g[2] != "y" {
		t.Errorf("goneTypesClaim = %q, want x and y", g)
	}
	if c := citedGoFile.FindAllStringSubmatch(body, -1); len(c) != 1 || c[0][1] != "pkg/a/b.go" {
		t.Errorf("citedGoFile = %q, want pkg/a/b.go only", c)
	}
}
