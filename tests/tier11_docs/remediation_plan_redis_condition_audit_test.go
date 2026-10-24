// SPDX-License-Identifier: MIT

// Tier-11 consistency check between the Shared Redis topology condition audit
// in the remediation plan, the Redis packages in the tree, and BUILD-GAPS.md.
//
// §12.4 permits one Redis deployment for every concern while every Redis store
// meets the Shared Redis topology conditions, and the Redis Cluster migration
// pre-plan requires every store to be verified against them before migration.
// The remediation plan's P6 entry holds that verification as a table with one
// row per package that imports go-redis. A package added without a row has not
// been audited, a row whose package is gone describes code that no longer
// exists, and an unmet cell that names no filed finding leaves the gap
// untracked. This test fails in each of those cases.
//
// This test reads the repository state directly (no build tag, no
// infrastructure), the same posture as the other tier-11 doc checks.
//
// spec: 12.4 (Redis HA and Failure Modes, Shared Redis topology; Redis Cluster
// migration pre-plan)

package tier11_docs_test

import (
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

const (
	conditionAuditLabel = "**Condition audit.**"
	conditionAuditGoRed = "github.com/redis/go-redis/v9"
)

// conditionAuditColumns are the table columns: the package, then one column
// per §12.4 Shared Redis topology condition.
var conditionAuditColumns = []string{
	"Package", "Single slot", "Cross-slot `SCAN`", "Coordination durability",
	"Failure path", "Lease TTL", "Separation",
}

// conditionAuditGateTests are the tier-0 gates the single-slot and SCAN
// columns rest on.
var conditionAuditGateTests = []string{
	"TestRedisStoresInstallSlotGuard_spec_12_4",
	"TestRedisScanRoutesByNode_spec_12_4",
}

// conditionAuditRoots are the trees the tier-0 Redis usage scan reads.
var conditionAuditRoots = []string{"pkg", "cmd", "sdks"}

// conditionAuditMandatoryRows are the lenny-ops stores the audit must cover
// by name, whatever the scan returns.
var conditionAuditMandatoryRows = []string{
	"pkg/ops/coordination/redisstore",
	"pkg/ops/escalation/redisstore",
}

var findingIDPattern = regexp.MustCompile(`F-\d+(?:\.\d+)+`)

// conditionAuditRow is one parsed table row.
type conditionAuditRow struct {
	pkg   string
	cells []string
}

// conditionAudit is the parsed audit sub-item of a plan entry.
type conditionAudit struct {
	text   string // the sub-item prose and table
	header []string
	rows   []conditionAuditRow
}

// p6PlanEntry returns the lines of the plan entry whose first line starts with
// prefix, up to the next top-level list item or heading.
func p6PlanEntry(plan string, prefixes ...string) (string, bool) {
	lines := strings.Split(plan, "\n")
	for i, line := range lines {
		matched := false
		for _, p := range prefixes {
			if strings.HasPrefix(line, p) {
				matched = true
			}
		}
		if !matched {
			continue
		}
		var b strings.Builder
		b.WriteString(line)
		b.WriteString("\n")
		for _, next := range lines[i+1:] {
			if strings.HasPrefix(next, "- ") || strings.HasPrefix(next, "#") ||
				(next != "" && !strings.HasPrefix(next, " ")) {
				break
			}
			b.WriteString(next)
			b.WriteString("\n")
		}
		return b.String(), true
	}
	return "", false
}

// parseConditionAudit returns the audit sub-item of entry and its table.
func parseConditionAudit(entry string) (conditionAudit, bool) {
	lines := strings.Split(entry, "\n")
	start := -1
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "- "+conditionAuditLabel) {
			start = i
			break
		}
	}
	if start < 0 {
		return conditionAudit{}, false
	}
	var audit conditionAudit
	var text strings.Builder
	for _, line := range lines[start:] {
		trimmed := strings.TrimSpace(line)
		if line != lines[start] && strings.HasPrefix(trimmed, "- **") {
			break
		}
		text.WriteString(line)
		text.WriteString("\n")
		if !strings.HasPrefix(trimmed, "|") {
			continue
		}
		cells := splitTableRow(trimmed)
		switch {
		case audit.header == nil:
			audit.header = cells
		case isSeparatorRow(cells):
		default:
			audit.rows = append(audit.rows, conditionAuditRow{pkg: strings.Trim(cells[0], "`"), cells: cells})
		}
	}
	audit.text = text.String()
	return audit, true
}

// splitTableRow splits a markdown table row into trimmed cells.
func splitTableRow(row string) []string {
	row = strings.TrimSuffix(strings.TrimPrefix(row, "|"), "|")
	parts := strings.Split(row, "|")
	for i, p := range parts {
		parts[i] = strings.TrimSpace(p)
	}
	return parts
}

// redisPackages returns every package directory, relative to root, whose
// non-test Go files import go-redis under the scan roots, skipping testdata
// and vendor trees.
func redisPackages(t *testing.T, root string) []string {
	t.Helper()
	set := map[string]bool{}
	fset := token.NewFileSet()
	for _, r := range conditionAuditRoots {
		err := filepath.WalkDir(filepath.Join(root, r), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if d.Name() == "testdata" || d.Name() == "vendor" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
			if err != nil {
				return err
			}
			for _, imp := range f.Imports {
				if p, _ := strconv.Unquote(imp.Path.Value); p == conditionAuditGoRed {
					rel, _ := filepath.Rel(root, filepath.Dir(path))
					set[filepath.ToSlash(rel)] = true
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", r, err)
		}
	}
	out := make([]string, 0, len(set))
	for p := range set {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// unmetCellsWithoutFinding returns "package: column" for every cell that
// reads unmet and names no finding ID.
func unmetCellsWithoutFinding(audit conditionAudit) []string {
	var out []string
	for _, row := range audit.rows {
		for i, cell := range row.cells {
			if strings.HasPrefix(cell, "unmet") && !findingIDPattern.MatchString(cell) && i < len(audit.header) {
				out = append(out, row.pkg+": "+audit.header[i])
			}
		}
	}
	return out
}

// buildGapsHeadingIDs returns every finding ID that a BUILD-GAPS.md heading
// names.
func buildGapsHeadingIDs(doc string) map[string]bool {
	ids := map[string]bool{}
	for _, line := range strings.Split(doc, "\n") {
		if !strings.HasPrefix(line, "### ") {
			continue
		}
		if m := findingIDPattern.FindString(line); m != "" && strings.Contains(line, m+" — ") {
			ids[m] = true
		}
	}
	return ids
}

func readP6ConditionAudit(t *testing.T, root string) (string, conditionAudit) {
	t.Helper()
	plan := readDocPage(t, filepath.Join(root, "gateway-runtime-comms-remediation.md"))
	entry, ok := p6PlanEntry(plan, opsLockPlanEntryPrefix, opsLockPlanEntryTickedPf)
	if !ok {
		t.Fatal("gateway-runtime-comms-remediation.md: no P6 lenny-ops remediation-lock entry (renamed or removed?)")
	}
	audit, ok := parseConditionAudit(entry)
	if !ok {
		t.Fatalf("gateway-runtime-comms-remediation.md P6 entry: no %s sub-item", conditionAuditLabel)
	}
	return entry, audit
}

// diagnosis: a package that imports go-redis has no row in the P6 Shared
// Redis topology condition audit, a row names a package that no longer
// imports go-redis, or the table lost a condition column or a gate citation.
// Audit the new package against every §12.4 Shared Redis topology condition
// and add its row, or delete the stale row.
func TestRedisConditionAuditCoversEveryRedisPackage_spec_12_4(t *testing.T) {
	root := repoRoot(t)
	_, audit := readP6ConditionAudit(t, root)

	if strings.Join(audit.header, "|") != strings.Join(conditionAuditColumns, "|") {
		t.Fatalf("condition audit header = %q, want %q", audit.header, conditionAuditColumns)
	}
	gate, err := os.ReadFile(filepath.Join(root, "tests/tier0_static/redis_cluster_safety_test.go"))
	if err != nil {
		t.Fatalf("read tier-0 Redis cluster safety test: %v", err)
	}
	for _, name := range conditionAuditGateTests {
		if !strings.Contains(audit.text, "`"+name+"`") {
			t.Errorf("condition audit does not cite %s", name)
		}
		if !strings.Contains(string(gate), "func "+name+"(") {
			t.Errorf("condition audit cites %s, which tests/tier0_static/redis_cluster_safety_test.go no longer declares", name)
		}
	}

	rows := map[string]bool{}
	for _, row := range audit.rows {
		if len(row.cells) != len(conditionAuditColumns) {
			t.Errorf("condition audit row %s has %d cells, want %d", row.pkg, len(row.cells), len(conditionAuditColumns))
		}
		for i, cell := range row.cells {
			if cell == "" {
				t.Errorf("condition audit row %s: empty %s cell", row.pkg, conditionAuditColumns[i])
			}
		}
		if rows[row.pkg] {
			t.Errorf("condition audit lists %s twice", row.pkg)
		}
		rows[row.pkg] = true
	}

	pkgs := redisPackages(t, root)
	if len(pkgs) == 0 {
		t.Fatal("found no package importing go-redis; the scan is broken")
	}
	live := map[string]bool{}
	for _, p := range pkgs {
		live[p] = true
		if !rows[p] {
			t.Errorf("%s imports go-redis but has no row in the P6 condition audit", p)
		}
	}
	for _, p := range conditionAuditMandatoryRows {
		if !rows[p] {
			t.Errorf("the P6 condition audit has no row for the lenny-ops store %s", p)
		}
	}
	for p := range rows {
		if !live[p] {
			t.Errorf("the P6 condition audit row %s names a package that no longer imports go-redis; delete the row", p)
		}
	}
}

// diagnosis: the P6 condition audit has an unmet cell that names no finding,
// names a finding BUILD-GAPS.md does not hold, or the P6 entry is not ticked
// although its audit has landed. File the finding, or correct the cell.
func TestRedisConditionAuditFindingsAreFiled_spec_12_4(t *testing.T) {
	root := repoRoot(t)
	entry, audit := readP6ConditionAudit(t, root)

	if !strings.HasPrefix(entry, opsLockPlanEntryTickedPf) {
		t.Errorf("the P6 entry carries a condition audit but is not ticked")
	}
	for _, cell := range unmetCellsWithoutFinding(audit) {
		t.Errorf("condition audit cell %s reads unmet and names no BUILD-GAPS finding", cell)
	}

	ids := buildGapsHeadingIDs(readDocPage(t, filepath.Join(root, "BUILD-GAPS.md")))
	cited := map[string]bool{}
	for _, row := range audit.rows {
		for _, cell := range row.cells {
			for _, id := range findingIDPattern.FindAllString(cell, -1) {
				cited[id] = true
			}
		}
	}
	filed, ok := paragraphStartingWith(strings.ReplaceAll(entry, "\n    ", " "), "  - **Filed.**")
	if !ok {
		t.Fatal("the P6 entry has no **Filed.** sub-item listing the findings the audit filed")
	}
	for _, id := range findingIDPattern.FindAllString(filed, -1) {
		cited[id] = true
	}
	if len(cited) == 0 {
		t.Fatal("the P6 condition audit cites no finding; the parser is broken or the findings were dropped")
	}
	for id := range cited {
		if !ids[id] {
			t.Errorf("the P6 condition audit cites %s, which no BUILD-GAPS.md heading names", id)
		}
	}
}

// diagnosis: the plan-entry and audit-table parsers no longer find the entry,
// its table, its rows, or its unmet cells, so the two checks above pass or
// fail vacuously.
func TestRedisConditionAuditParsers_spec_12_4(t *testing.T) {
	plan := strings.Join([]string{
		"- [x] **P5. other**",
		"  - **Condition audit.** not this one.",
		"- [x] **P6. `lenny-ops` remediation-lock keys and the brace rule**",
		"  - **Condition audit.** One row per package.",
		"",
		"    | Package | Single slot | Cross-slot `SCAN` | Coordination durability | Failure path | Lease TTL | Separation |",
		"    |:--|:--|:--|:--|:--|:--|:--|",
		"    | `pkg/a` | met | met | met | unmet (F-1.2.3) | n/a | unmet |",
		"    | `pkg/b` | met | met | n/a | n/a | n/a | n/a |",
		"",
		"  - **Filed.** F-1.2.3 and F-4.5.6.",
		"- [ ] **P7. next**",
	}, "\n")

	entry, ok := p6PlanEntry(plan, opsLockPlanEntryPrefix, opsLockPlanEntryTickedPf)
	if !ok || strings.Contains(entry, "P7") || strings.Contains(entry, "P5") {
		t.Fatalf("planEntry = %q, %v", entry, ok)
	}
	audit, ok := parseConditionAudit(entry)
	if !ok {
		t.Fatal("parseConditionAudit missed the audit sub-item")
	}
	if strings.Join(audit.header, "|") != strings.Join(conditionAuditColumns, "|") {
		t.Errorf("header = %q", audit.header)
	}
	if len(audit.rows) != 2 || audit.rows[0].pkg != "pkg/a" || audit.rows[1].pkg != "pkg/b" {
		t.Fatalf("rows = %+v", audit.rows)
	}
	if strings.Contains(audit.text, "**Filed.**") {
		t.Error("audit text runs into the next sub-item")
	}
	if got := unmetCellsWithoutFinding(audit); strings.Join(got, ",") != "pkg/a: Separation" {
		t.Errorf("unmetCellsWithoutFinding = %v", got)
	}
	ids := buildGapsHeadingIDs("### - [ ] F-1.2.3 — gap [High] — OPEN\nsee F-9.9.9 — prose\n")
	if !ids["F-1.2.3"] || ids["F-9.9.9"] {
		t.Errorf("buildGapsHeadingIDs = %v", ids)
	}
	if _, ok := parseConditionAudit("- [x] **P6. x**\n  - **Progress.** none\n"); ok {
		t.Error("parseConditionAudit found an audit the entry does not hold")
	}
}
