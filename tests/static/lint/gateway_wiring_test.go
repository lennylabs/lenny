// SPDX-License-Identifier: MIT

package lint

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/lennylabs/lenny/scripts/specshift/annotation"
	"github.com/lennylabs/lenny/tests/static/lint/gatewaylayout"
	"github.com/lennylabs/lenny/tests/static/lint/gatewaywiring"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

const gatewayWiringRegister = "tests/registers/gateway-wiring.yaml"

// TestGatewayWiringCrossesOnlyThroughTheSeam runs the wiring check over
// cmd/lenny-gateway: every value of one gateway part the composition root
// passes where another part expects an interface or a function not
// declared in pkg/gateway/seam is a crossing, and the check refuses a
// crossing tests/registers/gateway-wiring.yaml does not carry and a row
// that no longer occurs.
//
// diagnosis: an unregistered crossing means a change wired a value of one
// part into another part's own interface; declare the interface in
// pkg/gateway/seam and wire it there. A stale row means a step routed a
// crossing through the seam interface; delete its row in the same commit.
// spec: GW-R1
func TestGatewayWiringCrossesOnlyThroughTheSeam(t *testing.T) {
	root := schematest.RepoRoot(t)
	layout, err := gatewaylayout.LoadRegister(filepath.Join(root, filepath.FromSlash(gatewayLayoutRegister)))
	if err != nil {
		t.Fatal(err)
	}
	reg, err := gatewaywiring.LoadRegister(filepath.Join(root, filepath.FromSlash(gatewayWiringRegister)))
	if err != nil {
		t.Fatalf("load the gateway wiring register: %v", err)
	}
	prog, err := gatewaywiring.LoadProgram(root, "./cmd/lenny-gateway")
	if err != nil {
		t.Fatalf("load cmd/lenny-gateway: %v", err)
	}
	cl := gatewaylayout.NewClassifier(gatewaylayout.Layout, layout)
	res := gatewaywiring.Reconcile(gatewaywiring.Crossings(prog, cl.Part), reg)
	for _, m := range res.Malformed {
		t.Errorf("malformed register row: %s", m)
	}
	for _, c := range res.Unregistered {
		t.Errorf("unregistered wiring crossing: %s", c)
	}
	for _, s := range res.Stale {
		t.Errorf("stale register row: %s", s)
	}
}

// TestGatewayWiringRegisterOnlyShrinks compares the wiring register with
// its copy at the merge base with main: a branch may delete rows, and may
// add or rewrite one only where a move of the manifest rewrote its paths or
// in the step that moves one of its packages.
//
// diagnosis: a refused row means a change registered a new crossing
// instead of routing it through pkg/gateway/seam.
// spec: GW-R1
func TestGatewayWiringRegisterOnlyShrinks(t *testing.T) {
	root := schematest.RepoRoot(t)
	ctx := context.Background()
	base, ref, err := annotation.MergeBase(ctx, root)
	if errors.Is(err, annotation.ErrNoBase) {
		t.Skipf("not-yet-applicable: %v, as in a shallow clone, so there is no merge-base register to compare with", err)
	}
	if err != nil {
		t.Fatal(err)
	}
	baseData, err := exec.CommandContext(ctx, "git", "-C", root, "show", base+":"+gatewayWiringRegister).Output()
	if err != nil {
		t.Logf("the register does not exist at %s (%s), so the branch introduces it and the ratchet starts from this copy", base, ref)
		return
	}
	baseReg, err := gatewaywiring.ParseRegister(baseData)
	if err != nil {
		t.Fatalf("decode the merge-base register: %v", err)
	}
	reg, err := gatewaywiring.LoadRegister(filepath.Join(root, filepath.FromSlash(gatewayWiringRegister)))
	if err != nil {
		t.Fatal(err)
	}
	for _, problem := range gatewaywiring.Ratchet(baseReg, reg, manifestMoves(t, root)) {
		t.Error(problem)
	}
}
