// SPDX-License-Identifier: MIT

// Fixture: a gate whose tier-0 test declares the gate name but reaches the
// check by shelling out to a script under scripts/, which is the channel the
// repository tolerates. It is fixture material rather than compiled source;
// the go tool does not read a testdata tree.

package tier0_static

import (
	"os/exec"
	"testing"
)

func TestExampleGateCertifiesTheTree(t *testing.T) {
	out, err := exec.Command("bash", "scripts/gates/example-gate.sh").CombinedOutput()
	if err != nil {
		t.Errorf("the gate reported: %s", out)
	}
}
