// SPDX-License-Identifier: MIT

package gitref

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/workspaceplan"
)

// tempRepo creates a local Git repository with one commit on `main`
// and returns its path and the commit SHA.
func tempRepo(t *testing.T) (dir, commitSHA string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	dir = t.TempDir()
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=alice", "GIT_AUTHOR_EMAIL=alice@acme.com",
			"GIT_COMMITTER_NAME=alice", "GIT_COMMITTER_EMAIL=alice@acme.com",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	run("init", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "README.md")
	run("commit", "-m", "initial")
	return dir, run("rev-parse", "HEAD")
}

func gitTag(t *testing.T, dir string, annotated bool, name string) {
	t.Helper()
	args := []string{"tag"}
	if annotated {
		args = append(args, "-a", name, "-m", "release "+name)
	} else {
		args = append(args, name)
	}
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=alice", "GIT_AUTHOR_EMAIL=alice@acme.com",
		"GIT_COMMITTER_NAME=alice", "GIT_COMMITTER_EMAIL=alice@acme.com",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git tag: %v\n%s", err, out)
	}
}

// the resolver satisfies the workspaceplan.RefResolver interface.
var _ workspaceplan.RefResolver = (*LsRemoteResolver)(nil)

func TestResolveBranch(t *testing.T) {
	dir, want := tempRepo(t)
	r := NewLsRemoteResolver(Options{})
	got, err := r.Resolve(context.Background(), workspaceplan.GitClone{URL: dir, Ref: "main"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got != want {
		t.Errorf("Resolve(main) = %q, want %q", got, want)
	}
}

func TestResolveLightweightTag(t *testing.T) {
	dir, want := tempRepo(t)
	gitTag(t, dir, false, "v1.0")
	r := NewLsRemoteResolver(Options{})
	got, err := r.Resolve(context.Background(), workspaceplan.GitClone{URL: dir, Ref: "v1.0"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got != want {
		t.Errorf("Resolve(v1.0) = %q, want the commit SHA %q", got, want)
	}
}

func TestResolveAnnotatedTagPeelsToCommit(t *testing.T) {
	dir, want := tempRepo(t)
	gitTag(t, dir, true, "v2.0")
	r := NewLsRemoteResolver(Options{})
	got, err := r.Resolve(context.Background(), workspaceplan.GitClone{URL: dir, Ref: "v2.0"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	// An annotated tag must resolve to the commit it points at, not the
	// tag object's own SHA.
	if got != want {
		t.Errorf("Resolve(v2.0) = %q, want the peeled commit SHA %q", got, want)
	}
}

func TestResolveUnknownRefIsNotFound(t *testing.T) {
	dir, _ := tempRepo(t)
	r := NewLsRemoteResolver(Options{})
	_, err := r.Resolve(context.Background(), workspaceplan.GitClone{URL: dir, Ref: "no-such-ref"})
	var re *workspaceplan.ResolveError
	if !errors.As(err, &re) {
		t.Fatalf("error = %v, want *ResolveError", err)
	}
	if re.Reason != workspaceplan.ResolveRefNotFound {
		t.Errorf("Reason = %q, want ref_not_found", re.Reason)
	}
	if re.Reason.Transient() {
		t.Error("ref_not_found must not be retryable")
	}
}

func TestResolveMissingRepoIsClassified(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	r := NewLsRemoteResolver(Options{})
	_, err := r.Resolve(context.Background(), workspaceplan.GitClone{
		URL: filepath.Join(t.TempDir(), "does-not-exist"), Ref: "main",
	})
	var re *workspaceplan.ResolveError
	if !errors.As(err, &re) {
		t.Fatalf("error = %v, want *ResolveError", err)
	}
	if re.Reason != workspaceplan.ResolveNetworkError {
		t.Errorf("Reason = %q, want network_error for an unreachable repo", re.Reason)
	}
}

func TestResolveDrivesPinCommitSHAs(t *testing.T) {
	// End to end: the resolver feeds workspaceplan.PinCommitSHAs.
	dir, want := tempRepo(t)
	plan := &workspaceplan.Plan{
		SchemaVersion: 1,
		Sources: []workspaceplan.Source{
			{Type: workspaceplan.TypeGitClone, Variant: workspaceplan.GitClone{URL: dir, Ref: "main"}},
		},
	}
	if err := workspaceplan.PinCommitSHAs(context.Background(), plan, NewLsRemoteResolver(Options{})); err != nil {
		t.Fatalf("PinCommitSHAs: %v", err)
	}
	gc := plan.Sources[0].Variant.(workspaceplan.GitClone)
	if gc.ResolvedCommitSha != want {
		t.Errorf("ResolvedCommitSha = %q, want %q", gc.ResolvedCommitSha, want)
	}
}

func TestPickSHA(t *testing.T) {
	const branch, tag, peeled = "aaaaaaaa", "bbbbbbbb", "cccccccc"
	cases := []struct {
		name   string
		output string
		ref    string
		want   string
		ok     bool
	}{
		{
			name:   "branch wins over a same-named tag",
			output: branch + "\trefs/heads/x\n" + tag + "\trefs/tags/x\n",
			ref:    "x", want: branch, ok: true,
		},
		{
			name:   "annotated tag peels to its commit",
			output: tag + "\trefs/tags/v1\n" + peeled + "\trefs/tags/v1^{}\n",
			ref:    "v1", want: peeled, ok: true,
		},
		{
			name:   "lightweight tag resolves directly",
			output: tag + "\trefs/tags/v1\n",
			ref:    "v1", want: tag, ok: true,
		},
		{
			name:   "no matching ref",
			output: branch + "\trefs/heads/other\n",
			ref:    "x", want: "", ok: false,
		},
		{
			name:   "empty output",
			output: "",
			ref:    "x", want: "", ok: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := pickSHA(tc.output, tc.ref)
			if got != tc.want || ok != tc.ok {
				t.Errorf("pickSHA = (%q, %v), want (%q, %v)", got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestClassify(t *testing.T) {
	if classify(context.DeadlineExceeded, "") != workspaceplan.ResolveNetworkError {
		t.Error("a context deadline must classify as network_error")
	}
	if classify(nil, "fatal: Authentication failed for 'https://x/y'") != workspaceplan.ResolveAuthFailed {
		t.Error("an authentication failure must classify as auth_failed")
	}
	if classify(nil, "fatal: could not read Username for 'https://x'") != workspaceplan.ResolveAuthFailed {
		t.Error("a credential prompt failure must classify as auth_failed")
	}
	if classify(nil, "fatal: unable to access: Could not resolve host: x") != workspaceplan.ResolveNetworkError {
		t.Error("a DNS failure must classify as network_error")
	}
	if classify(nil, "some unrecognized failure") != workspaceplan.ResolveNetworkError {
		t.Error("an unrecognized failure must default to network_error")
	}
}
