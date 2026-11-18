// SPDX-License-Identifier: MIT

package lint

import (
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
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

const gatewayLayoutRegister = "tests/registers/gateway-layout.yaml"

// TestGatewayLayoutHoldsTheImportRule runs the gateway layout gate over the
// module: it reads the non-test import graph with go list and refuses every
// import that crosses a part boundary of the gateway other than through the
// seam interface, and every gateway package outside the directory of its
// part, that tests/registers/gateway-layout.yaml does not carry. It also
// refuses a register row whose violation no longer occurs, so the register
// only shrinks as the steps of change 0117 move each part.
//
// diagnosis: an unregistered violation means a change added an import
// across a seam boundary or a package outside the layout; route the call
// through pkg/gateway/seam or place the package in its part's directory. A
// stale row means a step removed a violation; delete its row in the same
// commit.
// spec: GW-R1
func TestGatewayLayoutHoldsTheImportRule(t *testing.T) {
	root := schematest.RepoRoot(t)
	reg, err := gatewaylayout.LoadRegister(filepath.Join(root, filepath.FromSlash(gatewayLayoutRegister)))
	if err != nil {
		t.Fatalf("load the gateway layout register: %v", err)
	}
	module, pkgs, err := gatewaylayout.ListPackages(root)
	if err != nil {
		t.Fatalf("list the module's packages: %v", err)
	}
	res := gatewaylayout.Check(module, gatewaylayout.Layout, pkgs, reg)
	for _, m := range res.Malformed {
		t.Errorf("malformed register row: %s", m)
	}
	for _, v := range res.Unregistered {
		t.Errorf("unregistered layout violation: %s", v)
	}
	for _, s := range res.Stale {
		t.Errorf("stale register row: %s", s)
	}
}

// TestGatewayLayoutRegisterOnlyShrinks compares the register with its copy
// at the merge base with main: a branch may delete rows, and may add or
// rewrite a row only where a move of the manifest rewrote its paths or in
// the step that moves its package. A row that puts a package in transit
// inside a directory of the layout is refused.
//
// diagnosis: a refused row means a change registered a new layout
// violation instead of fixing it; route the import through
// pkg/gateway/seam or place the package in its part's directory.
// spec: GW-R1
func TestGatewayLayoutRegisterOnlyShrinks(t *testing.T) {
	root := schematest.RepoRoot(t)
	ctx := context.Background()
	base, ref, err := annotation.MergeBase(ctx, root)
	if errors.Is(err, annotation.ErrNoBase) {
		t.Skipf("not-yet-applicable: %v, as in a shallow clone, so there is no merge-base register to compare with", err)
	}
	if err != nil {
		t.Fatal(err)
	}
	baseData, err := exec.CommandContext(ctx, "git", "-C", root, "show", base+":"+gatewayLayoutRegister).Output()
	if err != nil {
		t.Logf("the register does not exist at %s (%s), so the branch introduces it and the ratchet starts from this copy", base, ref)
		return
	}
	baseReg, err := gatewaylayout.ParseRegister(baseData)
	if err != nil {
		t.Fatalf("decode the merge-base register: %v", err)
	}
	reg, err := gatewaylayout.LoadRegister(filepath.Join(root, filepath.FromSlash(gatewayLayoutRegister)))
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(filepath.Join(root, "scripts", "refactor", "manifest"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	manifest, err := rewrite.ParseManifest(f)
	if err != nil {
		t.Fatal(err)
	}
	moves := make([]gatewaylayout.Move, 0, len(manifest))
	for _, m := range manifest {
		moves = append(moves, gatewaylayout.Move{Old: rewrite.RepoRel(m.Old), New: rewrite.RepoRel(m.New), Step: m.Step})
	}
	for _, problem := range gatewaylayout.Ratchet(baseReg, reg, moves, gatewaylayout.Layout) {
		t.Errorf("%s", strings.TrimSpace(problem))
	}
}
