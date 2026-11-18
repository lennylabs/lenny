// SPDX-License-Identifier: MIT

package lint

import (
	"path/filepath"
	"testing"

	"github.com/lennylabs/lenny/tests/static/lint/gatewaylayout"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

const gatewayLayoutRegister = "tests/registers/gateway-layout.yaml"

// TestGatewayLayoutHoldsTheImportRule runs the gateway layout gate over the
// module: it reads the non-test import graph with go list and refuses every
// import that crosses a part boundary of the gateway other than through the
// seam interface, every import of a confined credential or admission
// package by a part that GW-R2 does not allow it, and every gateway package
// outside the directory of its part, that tests/registers/gateway-layout.yaml
// does not carry. It also refuses a register row whose violation no longer
// occurs, so the register only shrinks as the steps of change 0117 move each
// part.
//
// diagnosis: an unregistered violation means a change added an import
// across a seam boundary or a package outside the layout; route the call
// through pkg/gateway/seam or place the package in its part's directory. An
// unregistered confined-imports violation means a part that GW-R2 does not
// allow imported a credential or admission package; reach it through an
// operation of pkg/gateway/seam instead. A stale row means a step removed a
// violation; delete its row in the same commit.
// spec: GW-R1, GW-R2
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
// the step of a move the branch performs. A new row that places a package
// in another part's directory is refused, and so is any row naming a step
// that has landed.
//
// diagnosis: a refused row means a change registered a new layout
// violation instead of fixing it; route the import through
// pkg/gateway/seam or place the package in its part's directory. A row
// naming a landed step is a violation that step left behind.
// spec: GW-R1, GW-R2
func TestGatewayLayoutRegisterOnlyShrinks(t *testing.T) {
	root := schematest.RepoRoot(t)
	reg, err := gatewaylayout.LoadRegister(filepath.Join(root, filepath.FromSlash(gatewayLayoutRegister)))
	if err != nil {
		t.Fatal(err)
	}
	r := newRatchetContext(t, root)
	for _, problem := range gatewaylayout.LandedRows(reg, r.input.Landed) {
		t.Error(problem)
	}
	baseData, ok := r.baseFile(t, gatewayLayoutRegister)
	if !ok {
		t.Logf("the register does not exist at the merge base %s, so the branch introduces it and the ratchet starts from this copy", r.base)
		return
	}
	baseReg, err := gatewaylayout.ParseRegister(baseData)
	if err != nil {
		t.Fatalf("decode the merge-base register: %v", err)
	}
	for _, problem := range gatewaylayout.Ratchet(baseReg, reg, r.input, gatewaylayout.Layout) {
		t.Error(problem)
	}
}
