// SPDX-License-Identifier: MIT

package lint

import (
	"path/filepath"
	"testing"

	"github.com/lennylabs/lenny/tests/static/lint/gatewaylayout"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

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
	reg, err := gatewaylayout.LoadRegister(filepath.Join(root, "tests", "registers", "gateway-layout.yaml"))
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
