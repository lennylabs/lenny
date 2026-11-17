// SPDX-License-Identifier: MIT

package survey

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// These tests cover contributor tooling over the specification structure
// rather than a platform behavior, so they carry no specification
// annotation. Each builds a small tree with a git history in a temporary
// repository: a base commit on main and a branch commit for one step.

const warmPoolsBase = `---
code: POOL
type: capability
area: pools
summary: >-
  Keeps warm pods.
---

# Warm pools

## Requirements

<a id="pool-r1"></a>**POOL-R1. Pools keep warm pods.** A pool MUST keep minWarm pods.

<a id="pool-r2"></a>**POOL-R2. Old rule.** A pool MUST do the old thing.

## Settings

<a id="setting-minwarm"></a>

` + "```lenny-def" + `
kind: setting
name: minWarm
type: integer
default: 1
summary: The warm pods a pool keeps.
` + "```" + `
`

const warmPoolsBranch = `---
code: POOL
type: capability
area: pools
summary: >-
  Keeps warm pods.
retired:
  POOL-R2: removed, the old thing is gone
---

# Warm pools

## Requirements

<a id="pool-r1"></a>**POOL-R1. Pools keep warm pods.** A pool MUST keep at least minWarm pods.

<a id="pool-r3"></a>**POOL-R3. Pools drain.** A pool MUST drain on deletion.

## Settings

<a id="setting-warmfloor"></a>

` + "```lenny-def" + `
kind: setting
name: warmFloor
type: integer
default: 1
summary: The warm pods a pool keeps.
` + "```" + `
`

const adminPoolsBase = `---
code: ADMPOOLS
type: interface
summary: Pool operations.
---

# Pool operations

<a id="admpools-createpool"></a>

### ` + "`ADMPOOLS-createPool`" + `

Creates a pool.

<a id="admpools-deletepool"></a>

### ` + "`ADMPOOLS-deletePool`" + `

Deletes a pool.
`

const poolGo = `// SPDX-License-Identifier: MIT

package pool

// Pool is a pool.
type Pool struct{}

// Keep keeps warm pods.
//
// spec: POOL-R1
func Keep() {}

// Fill fills the pool.
//
// spec: POOL-R1
func Fill() {}

// Trim trims the pool.
//
// spec: POOL-R1
func Trim() {}

// Scale scales the pool.
//
// spec: POOL-R1
func (p *Pool) Scale() {}

// DefaultMinWarm is the default.
//
// spec: setting-minwarm
const DefaultMinWarm = 1
`

const poolTestGo = `// SPDX-License-Identifier: MIT

package pool

import "testing"

// spec: POOL-R1
func TestPoolKeepsWarmPods(t *testing.T) {}
`

const poolDocGo = `// SPDX-License-Identifier: MIT

// Package pool keeps warm pods.
//
// spec: POOL
package pool
`

const poolsDoc = `---
title: Pools
spec: [POOL]
---

# Pools

Set ` + "`minWarm`" + ` to keep pods warm.
`

const openIssue = `---
id: POOL-I1
status: open
requirements: [POOL-R1]
---

# Pools keep too few pods
`

const closedIssue = `---
id: POOL-I2
status: closed
requirements: [POOL-R1]
---

# Fixed
`

const stepsMD = `# Steps: 0200 Fixture

## Steps

### 0200.S1 Fixture step
- status: Building
- lanes: [specification, code]

Description.
`

// fixture is a temporary repository with a base commit on main and a
// branch with one step's commit.
type fixture struct {
	t    *testing.T
	root string
	base string
}

// newFixture builds the repository and leaves the branch checked out.
func newFixture(t *testing.T) *fixture {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
	f := &fixture{t: t, root: t.TempDir()}
	f.git("init", "-q", "-b", "main")
	f.write(map[string]string{
		"spec/capabilities/pools/warm-pools.md": warmPoolsBase,
		"spec/interfaces/admin-pools.md":        adminPoolsBase,
		"spec/01_numbered.md":                   "# 1. Numbered\n",
		"changes/0200-fixture/change.md":        "# Fixture\n\nStatus: `In progress`, revision 1, 2026-01-01.\n",
		"changes/0200-fixture/steps.md":         stepsMD,
		"pkg/pool/doc.go":                       poolDocGo,
		"pkg/pool/pool.go":                      poolGo,
		"pkg/pool/pool_test.go":                 poolTestGo,
		"pkg/other/other.go":                    "// SPDX-License-Identifier: MIT\n\npackage other\n",
		"docs/guide/pools.md":                   poolsDoc,
		"docs/guide/other.md":                   "# Other\n\nNo front matter.\n",
		"issues/POOL/POOL-I1-too-few-pods.md":   openIssue,
		"issues/POOL/POOL-I2-fixed.md":          closedIssue,
	})
	f.commit("base: the pool pages", "2026-01-10T12:00:00")
	f.base = strings.TrimSpace(f.git("rev-parse", "HEAD"))
	f.git("checkout", "-q", "-b", "change/0200.S1-fixture")
	f.write(map[string]string{
		"spec/capabilities/pools/warm-pools.md": warmPoolsBranch,
		"spec/interfaces/admin-pools.md":        strings.Replace(adminPoolsBase, "Deletes a pool.", "Deletes a pool and drains it.", 1),
		"pkg/pool/pool.go":                      strings.Replace(poolGo, "// Pool is a pool.", "// Pool is a pool of warm pods.", 1),
		"pkg/other/other.go":                    "// SPDX-License-Identifier: MIT\n\npackage other\n\n// Other does nothing.\nfunc Other() {}\n",
	})
	f.commit("spec: rework the pool requirements (0200.S1)", "2026-02-10T12:00:00")
	return f
}

// git runs git in the fixture and returns its output.
func (f *fixture) git(args ...string) string {
	f.t.Helper()
	return f.gitEnv(nil, args...)
}

func (f *fixture) gitEnv(env []string, args ...string) string {
	f.t.Helper()
	full := append([]string{"-C", f.root, "-c", "user.name=alice", "-c", "user.email=alice@acme.com", "-c", "commit.gpgsign=false"}, args...)
	cmd := exec.Command("git", full...)
	cmd.Env = append(os.Environ(), append([]string{"GIT_CONFIG_GLOBAL=" + os.DevNull, "GIT_CONFIG_NOSYSTEM=1"}, env...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		f.t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

// write writes files under the fixture root.
func (f *fixture) write(files map[string]string) {
	f.t.Helper()
	for p, content := range files {
		abs := filepath.Join(f.root, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			f.t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
			f.t.Fatal(err)
		}
	}
}

// commit commits every file with a fixed date.
func (f *fixture) commit(msg, date string) {
	f.t.Helper()
	f.git("add", "-A")
	f.gitEnv([]string{"GIT_AUTHOR_DATE=" + date, "GIT_COMMITTER_DATE=" + date}, "commit", "-q", "-m", msg)
}

// mustContain fails for each wanted text the output lacks.
func mustContain(t *testing.T, out string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(out, w) {
			t.Errorf("output lacks %q:\n%s", w, out)
		}
	}
}

// mustNotContain fails for each text the output carries.
func mustNotContain(t *testing.T, out string, unwanted ...string) {
	t.Helper()
	for _, w := range unwanted {
		if strings.Contains(out, w) {
			t.Errorf("output carries %q:\n%s", w, out)
		}
	}
}

func TestDigestListsTheItemsTheBranchChangesWithTheirTextAndSites(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	out, err := Digest(context.Background(), f.root, DigestOptions{Step: "0200.S1"})
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out,
		"# Digest: 0200.S1 Fixture step",
		"its merge base with `main`",
		"spec: rework the pool requirements (0200.S1)",
		"The branch adds 1, modifies 1, and retires 1.",
		"### POOL-R3 (added)",
		"### POOL-R1 (modified)",
		"A pool MUST keep minWarm pods. | <a id=\"pool-r1\"></a>**POOL-R1. Pools keep warm pods.** A pool MUST keep at least minWarm pods.",
		"### POOL-R2 (retired)",
		"Record: removed, the old thing is gone.",
		"`pkg/pool/pool.go:10 Keep`",
		"`pkg/pool/pool.go:25 Pool.Scale`",
		"- Tests: `pkg/pool/pool_test.go:7 TestPoolKeepsWarmPods`.",
		"### ADMPOOLS-deletePool (modified)",
		"Deletes a pool and drains it.",
		"### setting-warmfloor (added)",
		"### setting-minwarm (retired)",
		"`pkg/pool/pool.go:30 DefaultMinWarm`",
		"1 documentation page of 2 name in front matter",
		"The branch touches 2 specification pages: `ADMPOOLS`, `POOL`.",
		"| `POOL` | docs/guide/pools.md | no |",
		"- `minWarm` (setting, `POOL` (spec/capabilities/pools/warm-pools.md)), replaced by `warmFloor`: named by",
		"  - docs/guide/pools.md:8",
		"- POOL-R1, cited from 4 functions:",
		"## Gate results\n\nNot filled in yet.",
		"## Review outcomes\n\nNot filled in yet.",
		"## Defaults applied\n\nNot filled in yet.",
	)
	mustNotContain(t, out, "ADMPOOLS-createPool", "base: the pool pages")
}

func TestDigestOfABranchWithNoChangesReportsNone(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.git("checkout", "-q", "-b", "change/0200.S1-empty", "main")
	out, err := Digest(context.Background(), f.root, DigestOptions{Step: "0200.S1"})
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out,
		"The branch has no commits of its own yet.",
		"None. The branch adds, modifies, and retires no requirement.",
		"None. The branch adds, modifies, and retires no operation.",
		"None. The branch adds, modifies, and retires no definition block.",
		"The branch touches no specification page.",
		"None. The branch renames and removes no definition block.",
		"None. No requirement the branch touches",
	)
}

func TestDigestComparesWithAGivenBase(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	out, err := Digest(context.Background(), f.root, DigestOptions{Step: "0200.S1", Base: "HEAD"})
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "the commit given with -base", "None. The branch adds, modifies, and retires no requirement.")
}

func TestDigestRefusesAnUnknownStepAndABadArgument(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	for _, step := range []string{"0200.S2", "0200.S1-S2", "0201.S1", "0200.S3-S1", "fixture"} {
		if _, err := Digest(context.Background(), f.root, DigestOptions{Step: step}); err == nil {
			t.Errorf("Digest(%q) succeeded; want an error", step)
		}
	}
}

func TestWriteDigestWritesTheStepFileAndRefusesToReplaceIt(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	p, err := WriteDigest(ctx, f.root, DigestOptions{Step: "0200.S1"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if p != "changes/0200-fixture/digests/0200.S1.md" {
		t.Errorf("wrote %s; want changes/0200-fixture/digests/0200.S1.md", p)
	}
	if _, err := WriteDigest(ctx, f.root, DigestOptions{Step: "0200.S1"}, false); err == nil || !strings.Contains(err.Error(), "-force") {
		t.Errorf("a second write without -force returned %v; want a refusal naming -force", err)
	}
	if _, err := WriteDigest(ctx, f.root, DigestOptions{Step: "0200.S1"}, true); err != nil {
		t.Errorf("a write with -force failed: %v", err)
	}
}

func TestWhereReportsARequirementWithItsSitesDocumentationIssuesAndLastChange(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	var out bytes.Buffer
	if err := Where(context.Background(), f.root, "POOL-R1", &out); err != nil {
		t.Fatal(err)
	}
	mustContain(t, out.String(),
		"POOL-R1 (requirement)",
		"Page: POOL spec/capabilities/pools/warm-pools.md (Warm pools)",
		"A pool MUST keep at least minWarm pods.",
		"pkg/pool/pool.go:15 Fill",
		"pkg/pool/pool_test.go:7 TestPoolKeepsWarmPods",
		"docs/guide/pools.md",
		"POOL-I1 issues/POOL/POOL-I1-too-few-pods.md (open)",
		"spec: rework the pool requirements (0200.S1)",
	)
	mustNotContain(t, out.String(), "POOL-I2")
}

func TestWhereReportsTheCommitThatLastChangedTheTextRatherThanThePage(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	var out bytes.Buffer
	if err := Where(context.Background(), f.root, "ADMPOOLS-createPool", &out); err != nil {
		t.Fatal(err)
	}
	mustContain(t, out.String(), "ADMPOOLS-createPool (operation)", "Creates a pool.", "base: the pool pages")
	mustNotContain(t, out.String(), "Deletes a pool")
}

func TestWhereReportsAnUncommittedChange(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.write(map[string]string{"spec/capabilities/pools/warm-pools.md": strings.Replace(warmPoolsBranch, "on deletion", "on deletion and on drain", 1)})
	var out bytes.Buffer
	if err := Where(context.Background(), f.root, "POOL-R3", &out); err != nil {
		t.Fatal(err)
	}
	mustContain(t, out.String(), "on deletion and on drain", "uncommitted: the working tree changes it")
}

func TestWhereReportsAPageABlockAndARetiredIdentifier(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	var page bytes.Buffer
	if err := Where(ctx, f.root, "POOL", &page); err != nil {
		t.Fatal(err)
	}
	mustContain(t, page.String(), "POOL (page)", "Type: capability, area pools", "Keeps warm pods.",
		"POOL-R1, POOL-R3, setting-warmfloor", "pkg/pool/doc.go:5 package pool", "POOL-I1")
	var block bytes.Buffer
	if err := Where(ctx, f.root, "setting-warmfloor", &block); err != nil {
		t.Fatal(err)
	}
	mustContain(t, block.String(), "setting-warmfloor (definition block)", "name: warmFloor")
	var retired bytes.Buffer
	if err := Where(ctx, f.root, "POOL-R2", &retired); err != nil {
		t.Fatal(err)
	}
	mustContain(t, retired.String(), "POOL-R2 (retired)", "Removed: removed, the old thing is gone")
	if err := Where(ctx, f.root, "POOL-R9", &bytes.Buffer{}); err == nil {
		t.Error("Where(POOL-R9) succeeded; want an error for an identifier the tree does not hold")
	}
}

func TestSinceListsChangedRequirementsAndGroupsPackagesByArea(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	for _, arg := range []string{f.base, "2026-02-01"} {
		var out bytes.Buffer
		if err := Since(ctx, f.root, arg, &out); err != nil {
			t.Fatalf("Since(%s): %v", arg, err)
		}
		mustContain(t, out.String(),
			"  Added:\n    POOL-R3 Pools drain. (spec/capabilities/pools/warm-pools.md)",
			"  Modified:\n    POOL-R1 Pools keep warm pods.",
			"  Retired:\n    POOL-R2 Old rule. (spec/capabilities/pools/warm-pools.md), removed, the old thing is gone",
			"    ADMPOOLS-deletePool",
			"Go packages changed: 2",
			"  pools:\n    pkg/pool (POOL)",
			"  unmapped (the package's doc.go names no page code yet):\n    pkg/other",
		)
	}
}

func TestSinceADateBeforeTheFirstCommitReadsEverythingAsAdded(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	var out bytes.Buffer
	if err := Since(context.Background(), f.root, "2025-01-01", &out); err != nil {
		t.Fatal(err)
	}
	mustContain(t, out.String(), "before the first commit", "  Added:\n    POOL-R1", "    POOL-R3", "Go packages changed: 2")
	mustNotContain(t, out.String(), "Retired:")
}

func TestSinceRefusesAnArgumentThatIsNeitherADateNorACommit(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	if err := Since(context.Background(), f.root, "no-such-ref", &bytes.Buffer{}); err == nil {
		t.Error("Since(no-such-ref) succeeded; want an error")
	}
}

func TestRunParsesEachCommandAndRefusesMisplacedFlags(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	var out bytes.Buffer
	if err := Run(ctx, "digest", []string{"-root", f.root, "-stdout", "0200.S1"}, &out); err != nil {
		t.Fatal(err)
	}
	mustContain(t, out.String(), "# Digest: 0200.S1")
	out.Reset()
	if err := Run(ctx, "where", []string{"-root", f.root, "POOL-R3"}, &out); err != nil {
		t.Fatal(err)
	}
	mustContain(t, out.String(), "POOL-R3 (requirement)")
	for _, bad := range [][]string{
		{"where", "-root", f.root, "-stdout", "POOL-R3"},
		{"since", "-root", f.root},
		{"digest", "-root", f.root, "0200.S1", "extra"},
	} {
		if err := Run(ctx, bad[0], bad[1:], &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "usage:") {
			t.Errorf("Run(%v) returned %v; want a usage error", bad, err)
		}
	}
}

func TestAnchoredTextEndsAtTheNextRequirementOrSection(t *testing.T) {
	t.Parallel()
	page := strings.Join([]string{
		`<a id="x-r1"></a>**X-R1. One.** First rule.`,
		``,
		`- a listed case`,
		``,
		"```text",
		"# not a heading",
		"```",
		``,
		`<a id="x-r2"></a>**X-R2. Two.** Second rule.`,
		``,
		`## Next`,
		``,
		`<a id="x-op"></a>`,
		``,
		"### `X-op`",
		``,
		`Body.`,
		``,
		`#### Fields`,
		``,
		`Field table.`,
		``,
		`<a id="x-other"></a>`,
		``,
		"### `X-other`",
	}, "\n")
	cases := map[string]string{
		"x-r1":    "<a id=\"x-r1\"></a>**X-R1. One.** First rule.\n\n- a listed case\n\n```text\n# not a heading\n```",
		"x-r2":    "<a id=\"x-r2\"></a>**X-R2. Two.** Second rule.",
		"x-op":    "<a id=\"x-op\"></a>\n\n### `X-op`\n\nBody.\n\n#### Fields\n\nField table.",
		"missing": "",
	}
	for anchor, want := range cases {
		if got := anchoredText(page, anchor); got != want {
			t.Errorf("anchoredText(%s) = %q; want %q", anchor, got, want)
		}
	}
}
