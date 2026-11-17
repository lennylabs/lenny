// SPDX-License-Identifier: MIT

package docgate

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// These tests cover contributor tooling rather than a platform behavior,
// so they carry no specification annotation. Each builds a repository with
// a base commit on main and a branch that changes a requirement, then
// edits or leaves the documentation as the case needs.

// poolPage returns a pool page with one requirement and the given status
// line, empty for a page written in full.
func poolPage(status, rule string) string {
	return "---\ncode: POOL\ntype: capability\narea: pools\n" + status + "summary: Keeps warm pods.\n---\n\n# Warm pools\n\n## Requirements\n\n" +
		"<a id=\"pool-r1\"></a>**POOL-R1. Pools keep warm pods.** " + rule + "\n"
}

const poolDocs = "---\ntitle: Pools\nspec: [POOL]\n---\n\n# Pools\n\nA pool keeps warm pods.\n"

type repo struct {
	t    *testing.T
	root string
}

// newRepo commits a base on main with the pool page at the given status
// and a documentation page that names it, then checks out a branch that
// changes the requirement's text.
func newRepo(t *testing.T, status string) *repo {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Fatalf("git is not on PATH, so the fixture repository cannot be built: %v", err)
	}
	r := &repo{t: t, root: t.TempDir()}
	r.git("init", "-q", "-b", "main")
	r.write(map[string]string{
		"spec/capabilities/pools/warm-pools.md": poolPage(status, "A pool MUST keep minWarm pods."),
		"docs/guide/pools.md":                   poolDocs,
		"changes/0300-pools/change.md":          "# Pools\n\n## Documentation\n\n- `docs/guide/pools.md`: operators read the new floor.\n",
	})
	r.commit("base")
	r.git("checkout", "-q", "-b", "change/0300.S1-pools")
	r.write(map[string]string{"spec/capabilities/pools/warm-pools.md": poolPage(status, "A pool MUST keep at least minWarm pods.")})
	r.commit("spec: raise the floor")
	return r
}

func (r *repo) git(args ...string) {
	r.t.Helper()
	cmd := exec.Command("git", append([]string{"-C", r.root, "-c", "user.name=alice", "-c", "user.email=alice@acme.com", "-c", "commit.gpgsign=false"}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		r.t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

func (r *repo) write(files map[string]string) {
	r.t.Helper()
	for p, content := range files {
		abs := filepath.Join(r.root, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			r.t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
			r.t.Fatal(err)
		}
	}
}

func (r *repo) commit(msg string) {
	r.t.Helper()
	r.git("add", "-A")
	r.git("commit", "-q", "-m", msg)
}

func TestImpactRefusesAChangedRequirementWithNoDocumentationEdit(t *testing.T) {
	t.Parallel()
	r := newRepo(t, "")
	rep, err := CheckImpact(context.Background(), r.root)
	if err != nil {
		t.Fatal(err)
	}
	if rep.BaseRef != "main" || len(rep.Checked) != 1 || rep.Checked[0] != "POOL" {
		t.Errorf("the report compared with %q and checked %v; want main and [POOL]", rep.BaseRef, rep.Checked)
	}
	mustContain(t, "the problems", joined(rep.Problems),
		"spec/capabilities/pools/warm-pools.md: the diff changes POOL-R1 on POOL and edits no documentation page whose front matter names POOL (the pages that name it are docs/guide/pools.md)")

	var out bytes.Buffer
	if err := Run(context.Background(), []string{"-root", r.root, "impact"}, &out); !errors.Is(err, ErrProblems) {
		t.Errorf("docs impact returned %v, want ErrProblems\n%s", err, out.String())
	}
}

func TestImpactPassesWhenTheDiffEditsADocumentationPageThatNamesThePage(t *testing.T) {
	t.Parallel()
	r := newRepo(t, "")
	// An uncommitted edit counts, as the annotation lint reads the working
	// tree.
	r.write(map[string]string{"docs/guide/pools.md": poolDocs + "\nA pool keeps at least that many.\n"})
	rep, err := CheckImpact(context.Background(), r.root)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Problems) > 0 {
		t.Errorf("the gate refused a diff that edits the documentation:\n%s", joined(rep.Problems))
	}
	var out bytes.Buffer
	if err := Run(context.Background(), []string{"-root", r.root, "impact"}, &out); err != nil {
		t.Fatalf("docs impact: %v\n%s", err, out.String())
	}
	mustContain(t, "the output", out.String(), "1 landed page(s) touched, no problems")
}

func TestImpactPassesWhenTheChangeStatesNoReaderFacingBehaviorChanges(t *testing.T) {
	t.Parallel()
	r := newRepo(t, "")
	r.write(map[string]string{"changes/0300-pools/change.md": "# Pools\n\n## Documentation\n\nNo reader-facing behavior changes.\n\n## Tests\n\n- Tier 0.\n"})
	r.commit("changes: state the documentation impact")
	rep, err := CheckImpact(context.Background(), r.root)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Problems) > 0 || len(rep.Waivers) != 1 || rep.Waivers[0] != "changes/0300-pools" {
		t.Errorf("waivers %v, problems:\n%s", rep.Waivers, joined(rep.Problems))
	}
}

func TestImpactIgnoresTheStatementOutsideTheDocumentationSection(t *testing.T) {
	t.Parallel()
	r := newRepo(t, "")
	r.write(map[string]string{"changes/0300-pools/change.md": "# Pools\n\n## Problem\n\nNo reader-facing behavior changes.\n\n## Documentation\n\n- `docs/guide/pools.md`.\n"})
	rep, err := CheckImpact(context.Background(), r.root)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Problems) != 1 || len(rep.Waivers) != 0 {
		t.Errorf("waivers %v, problems:\n%s", rep.Waivers, joined(rep.Problems))
	}
}

func TestImpactDoesNotCheckAPageThatHasNotLanded(t *testing.T) {
	t.Parallel()
	r := newRepo(t, "status: partial\n")
	rep, err := CheckImpact(context.Background(), r.root)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Problems) > 0 || len(rep.NotYet) != 1 || rep.NotYet[0] != "POOL" {
		t.Errorf("not yet %v, problems:\n%s", rep.NotYet, joined(rep.Problems))
	}
}

func TestImpactReportsAPageNoDocumentationNamesYet(t *testing.T) {
	t.Parallel()
	r := newRepo(t, "")
	r.write(map[string]string{"docs/guide/pools.md": "---\ntitle: Pools\n---\n\n# Pools\n"})
	rep, err := CheckImpact(context.Background(), r.root)
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, "the problems", joined(rep.Problems), "no documentation page names it in its front matter yet")
}

func TestImpactSkipsWithNoMergeBase(t *testing.T) {
	t.Parallel()
	r := &repo{t: t, root: t.TempDir()}
	r.git("init", "-q", "-b", "trunk")
	r.write(map[string]string{"spec/capabilities/pools/warm-pools.md": poolPage("", "A pool MUST keep minWarm pods.")})
	r.commit("base")
	if _, err := CheckImpact(context.Background(), r.root); !errors.Is(err, ErrNoBase) {
		t.Fatalf("CheckImpact returned %v, want ErrNoBase", err)
	}
	var out bytes.Buffer
	if err := Run(context.Background(), []string{"-root", r.root, "impact"}, &out); err != nil {
		t.Fatalf("docs impact with no merge base: %v", err)
	}
	mustContain(t, "the output", out.String(), "docs impact: skipped:")
}
