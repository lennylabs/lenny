// SPDX-License-Identifier: MIT

// Example golden-file test demonstrating the framework. The §15.1
// session response is the canonical shape new tests can copy as a
// pattern. Regenerate via `go test -update-golden ./tests/testinfra/golden/...`.

package golden_test

import (
	"encoding/json"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/golden"
)

// spec: 18.3 (golden-file framework)
// diagnosis: A canonical session-response shape regressed. Inspect
//
//	the diff above. When the change is intentional, re-run
//	with `-update-golden` and commit the result.
func TestExampleSessionResponseRoundTrip(t *testing.T) {
	t.Parallel()
	// Produce the canonical response payload (in real tests this
	// comes from a handler or store).
	resp := map[string]any{
		"id":         "sess_01ABC",
		"tenantId":   "acme",
		"runtimeRef": "claude-code",
		"state":      "created",
		"userId":     "alice@acme.com",
	}
	body, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	golden.AssertJSON(t, "session_response.json", body)
}
