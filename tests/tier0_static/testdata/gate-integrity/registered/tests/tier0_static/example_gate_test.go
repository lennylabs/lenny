// SPDX-License-Identifier: MIT

// Fixture: a gate registered through the channel the harness runs as a tier-0
// Go test. It is fixture material rather than compiled source; the go tool
// does not read a testdata tree.

package tier0_static

import "testing"

func TestExampleGateCertifiesTheTree(t *testing.T) {
	t.Parallel()
}
