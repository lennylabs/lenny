// SPDX-License-Identifier: MIT

package lint

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/scripts/refactor/rewrite"
	"github.com/lennylabs/lenny/scripts/specshift/annotation"
	"github.com/lennylabs/lenny/tests/static/lint/gatewaylayout"
)

// This file holds the merge-base reads the shrink-only tests of the
// gateway layout and wiring registers share. It reads the tree and git
// rather than a requirement, so it carries no specification annotation.

const gatewayManifest = "scripts/refactor/manifest"

// ratchetContext is the merge base and the moves of the manifest, which a
// renamed row maps back through.
type ratchetContext struct {
	root  string
	base  string
	moves []gatewaylayout.Move
}

// newRatchetContext finds the merge base with main, skipping the test when
// there is none, and reads the manifest at HEAD.
func newRatchetContext(t *testing.T, root string) *ratchetContext {
	t.Helper()
	base, _, err := annotation.MergeBase(context.Background(), root)
	if errors.Is(err, annotation.ErrNoBase) {
		t.Skipf("not-yet-applicable: %v, as in a shallow clone, so there is no merge-base register to compare with", err)
	}
	if err != nil {
		t.Fatal(err)
	}
	return &ratchetContext{
		root:  root,
		base:  base,
		moves: parseManifest(t, readFile(t, filepath.Join(root, filepath.FromSlash(gatewayManifest)))),
	}
}

// baseFile returns a file's content at the merge base, or false when the
// path does not exist there. Any other git failure fails the test.
func (r *ratchetContext) baseFile(t *testing.T, rel string) ([]byte, bool) {
	t.Helper()
	if !r.existsAtBase(t, rel) {
		return nil, false
	}
	return r.git(t, "show", r.base+":"+rel), true
}

// existsAtBase reports whether a path exists at the merge base, as
// git ls-tree lists it. A git failure fails the test rather than reading
// as an absent path.
func (r *ratchetContext) existsAtBase(t *testing.T, rel string) bool {
	t.Helper()
	return len(bytes.TrimSpace(r.git(t, "ls-tree", r.base, "--", rel))) > 0
}

func (r *ratchetContext) git(t *testing.T, args ...string) []byte {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", r.root}, args...)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, stderr.String())
	}
	return out
}

func parseManifest(t *testing.T, data []byte) []gatewaylayout.Move {
	t.Helper()
	manifest, err := rewrite.ParseManifest(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	moves := make([]gatewaylayout.Move, 0, len(manifest))
	for _, m := range manifest {
		moves = append(moves, gatewaylayout.Move{Old: rewrite.RepoRel(m.Old), New: rewrite.RepoRel(m.New), Step: m.Step})
	}
	return moves
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
