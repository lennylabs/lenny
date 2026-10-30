// SPDX-License-Identifier: MIT

// Tier-11 consistency check between the BUILD-GAPS.md record of the Redis
// Cluster cross-slot finding (F-12.4.28), the remediation plan entry that
// owns its lenny-ops class, and the tree.
//
// The lenny-ops Tier 2 remediation-lock store keeps its lock, ID, and epoch
// keys under the ops:{lock}: hash tag, so every lock script runs in one Redis
// Cluster slot. Once the store carries that tag, the finding is closed, its
// class B/C resolution names the tests that pin the fix, and the plan entry
// points at the proposal that landed it. A record that still says the class is
// open, or that names a test the tree no longer declares, sends a build loop
// back to a fix that already landed or to a test it cannot find.
//
// This test reads the repository state directly (no build tag, no
// infrastructure), the same posture as the other tier-11 doc checks.
//
// spec: 12.4 (Redis HA and Failure Modes), 25.4 (Storage Tiers)

package tier11_docs_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const (
	crossSlotFindingID       = "F-12.4.28"
	crossSlotClassBCLabel    = "**Resolution (class B/C"
	opsLockHashTag           = "ops:{lock}:"
	opsLockStorePath         = "pkg/ops/coordination/redisstore/redisstore.go"
	opsLockPlanEntryPrefix   = "- [ ] **P6. `lenny-ops` remediation-lock keys"
	opsLockPlanEntryTickedPf = "- [x] **P6. `lenny-ops` remediation-lock keys"
)

// staleCrossSlotPhrases are statements the record carried while class B/C was
// open. Each one is false once the lock store carries the hash tag.
var staleCrossSlotPhrases = []string{
	"coordination exempt until P6",
	"stays OPEN for class B/C",
}

var (
	backtickedTestFunc = regexp.MustCompile("`(Test[A-Za-z0-9_]+)`")
	backtickedTestFile = regexp.MustCompile("`([A-Za-z0-9_./-]+_test\\.go)`")
)

// buildGapsFinding returns the heading and body of the finding whose heading
// names id. The body runs to the next heading or horizontal rule.
func buildGapsFinding(body, id string) (heading, text string, ok bool) {
	lines := strings.Split(body, "\n")
	for i, line := range lines {
		if !strings.HasPrefix(line, "### ") || !strings.Contains(line, id+" — ") {
			continue
		}
		var b strings.Builder
		for _, next := range lines[i+1:] {
			if strings.HasPrefix(next, "#") || strings.TrimSpace(next) == "---" {
				break
			}
			b.WriteString(next)
			b.WriteString("\n")
		}
		return line, b.String(), true
	}
	return "", "", false
}

// paragraphStartingWith returns the line of text that begins with label.
func paragraphStartingWith(text, label string) (string, bool) {
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, label) {
			return line, true
		}
	}
	return "", false
}

// namedTests returns the backticked test function names and test file paths
// a paragraph cites.
func namedTests(paragraph string) (funcs, files []string) {
	for _, m := range backtickedTestFunc.FindAllStringSubmatch(paragraph, -1) {
		funcs = append(funcs, m[1])
	}
	for _, m := range backtickedTestFile.FindAllStringSubmatch(paragraph, -1) {
		files = append(files, m[1])
	}
	return funcs, files
}

// declaredIn reports whether any of the files declares func name.
func declaredIn(sources map[string]string, name string) bool {
	decl := "func " + name + "("
	for _, src := range sources {
		if strings.Contains(src, decl) {
			return true
		}
	}
	return false
}

// diagnosis: the lenny-ops lock store carries the ops:{lock}: hash tag, but the
// BUILD-GAPS record of F-12.4.28 still reads as open for class B/C, lacks its
// class B/C resolution, or cites a test the tree no longer declares. Update the
// record to the tree rather than reverting the store.
func TestBuildGapsCrossSlotRecordMatchesOpsLockStore_spec_12_4(t *testing.T) {
	root := repoRoot(t)
	store, err := os.ReadFile(filepath.Join(root, opsLockStorePath))
	if err != nil {
		t.Fatalf("read %s: %v", opsLockStorePath, err)
	}
	if !strings.Contains(string(store), opsLockHashTag) {
		t.Skip("not-yet-applicable: the lenny-ops lock store does not yet carry the ops:{lock}: hash tag, so F-12.4.28 class B/C is open")
	}

	heading, text, ok := buildGapsFinding(readDocPage(t, filepath.Join(root, "BUILD-GAPS.md")), crossSlotFindingID)
	if !ok {
		t.Fatalf("BUILD-GAPS.md: no %s heading (renumbered or removed?)", crossSlotFindingID)
	}
	if !strings.HasPrefix(heading, "### - [x] ") || !strings.HasSuffix(heading, "— CLOSED") {
		t.Errorf("BUILD-GAPS.md %s heading %q: want a ticked box and a CLOSED status", crossSlotFindingID, heading)
	}
	for _, phrase := range staleCrossSlotPhrases {
		if strings.Contains(text, phrase) {
			t.Errorf("BUILD-GAPS.md %s still states %q", crossSlotFindingID, phrase)
		}
	}

	paragraph, ok := paragraphStartingWith(text, crossSlotClassBCLabel)
	if !ok {
		t.Fatalf("BUILD-GAPS.md %s: no %s…):** paragraph", crossSlotFindingID, crossSlotClassBCLabel)
	}
	funcs, files := namedTests(paragraph)
	if len(funcs) == 0 || len(files) == 0 {
		t.Fatalf("BUILD-GAPS.md %s class B/C resolution names no test functions or test files", crossSlotFindingID)
	}
	sources := make(map[string]string, len(files))
	for _, f := range files {
		src, err := os.ReadFile(filepath.Join(root, f))
		if err != nil {
			t.Errorf("BUILD-GAPS.md %s class B/C resolution cites %s: %v", crossSlotFindingID, f, err)
			continue
		}
		sources[f] = string(src)
	}
	for _, fn := range funcs {
		if !declaredIn(sources, fn) {
			t.Errorf("BUILD-GAPS.md %s class B/C resolution names %s, which none of its cited test files declares", crossSlotFindingID, fn)
		}
	}
}

// diagnosis: the lenny-ops lock store carries the ops:{lock}: hash tag, but the
// remediation plan entry that owns the lock keys still reads as unwritten or no
// longer names the proposal that landed the fix.
func TestRemediationPlanOpsLockEntryNamesProposal_spec_12_4(t *testing.T) {
	root := repoRoot(t)
	store, err := os.ReadFile(filepath.Join(root, opsLockStorePath))
	if err != nil {
		t.Fatalf("read %s: %v", opsLockStorePath, err)
	}
	if !strings.Contains(string(store), opsLockHashTag) {
		t.Skip("not-yet-applicable: the lenny-ops lock store does not yet carry the ops:{lock}: hash tag")
	}
	plan := readDocPage(t, filepath.Join(root, "gateway-runtime-comms-remediation.md"))
	lines := strings.Split(plan, "\n")
	for i, line := range lines {
		if !strings.HasPrefix(line, opsLockPlanEntryPrefix) && !strings.HasPrefix(line, opsLockPlanEntryTickedPf) {
			continue
		}
		entry := strings.Join(lines[i:min(i+3, len(lines))], "\n")
		if strings.Contains(entry, "(not yet written)") {
			t.Errorf("gateway-runtime-comms-remediation.md:%d P6 entry still reads (not yet written)", i+1)
		}
		if !strings.Contains(entry, "proposals/0099_") {
			t.Errorf("gateway-runtime-comms-remediation.md:%d P6 entry does not point at the proposal that landed the lock keys", i+1)
		}
		return
	}
	t.Fatal("gateway-runtime-comms-remediation.md: no P6 lenny-ops remediation-lock entry (renamed or removed?)")
}

// diagnosis: the record parsers no longer find a finding's body, its class
// B/C paragraph, or the tests that paragraph cites, so the currency check
// above passes or fails vacuously.
func TestBuildGapsCrossSlotRecordParsers_spec_12_4(t *testing.T) {
	doc := strings.Join([]string{
		"### - [x] F-12.4.27 — other [High] — CLOSED",
		"**Resolution (class B/C, abc):** `TestOther_spec_1` in `tests/other_test.go`.",
		"",
		"---",
		"",
		"### - [x] F-12.4.28 — cross slot [High] — CLOSED",
		"**Resolution (classes A and A′, def):** done.",
		"**Resolution (class B/C, ghi):** in `pkg/x/x_test.go`, `TestA_spec_25_4` and `TestB_spec_12_4`.",
		"",
		"---",
		"### - [ ] F-12.4.29 — next [High] — OPEN",
	}, "\n")

	heading, text, ok := buildGapsFinding(doc, "F-12.4.28")
	if !ok || !strings.HasSuffix(heading, "— CLOSED") {
		t.Fatalf("buildGapsFinding = %q, %v", heading, ok)
	}
	if strings.Contains(text, "F-12.4.29") || strings.Contains(text, "TestOther_spec_1") {
		t.Errorf("finding body leaks a neighbouring finding: %q", text)
	}
	paragraph, ok := paragraphStartingWith(text, crossSlotClassBCLabel)
	if !ok {
		t.Fatal("paragraphStartingWith missed the class B/C paragraph")
	}
	funcs, files := namedTests(paragraph)
	if strings.Join(funcs, ",") != "TestA_spec_25_4,TestB_spec_12_4" {
		t.Errorf("namedTests funcs = %v", funcs)
	}
	if strings.Join(files, ",") != "pkg/x/x_test.go" {
		t.Errorf("namedTests files = %v", files)
	}
	sources := map[string]string{"pkg/x/x_test.go": "func TestA_spec_25_4(t *testing.T) {}"}
	if !declaredIn(sources, "TestA_spec_25_4") || declaredIn(sources, "TestB_spec_12_4") {
		t.Error("declaredIn does not tell a declared test from a missing one")
	}
	if _, _, ok := buildGapsFinding(doc, "F-12.4.99"); ok {
		t.Error("buildGapsFinding found a finding the document does not hold")
	}
}
