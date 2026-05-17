// SPDX-License-Identifier: MIT

package adapter_test

import (
	"encoding/json"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/adapter"
)

// §9.1 / §15: consumers inspect the adapterCapabilities block by its
// camelCase JSON keys, notably supportsElicitation. The wire keys are
// part of the contract, so pin them here.
func TestCapabilitiesJSONKeys(t *testing.T) {
	caps := adapter.Capabilities{
		PathPrefix:                "/v1",
		Protocol:                  "rest",
		SupportsSessionContinuity: true,
		SupportsDelegation:        false,
		SupportsElicitation:       true,
		SupportsInterrupt:         true,
	}
	b, err := json.Marshal(caps)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, key := range []string{
		"pathPrefix", "protocol", "supportsSessionContinuity",
		"supportsDelegation", "supportsElicitation", "supportsInterrupt",
	} {
		if _, ok := m[key]; !ok {
			t.Errorf("missing wire key %q in %s", key, b)
		}
	}
	if m["supportsElicitation"] != true {
		t.Errorf("supportsElicitation: got %v, want true", m["supportsElicitation"])
	}
	if m["supportsDelegation"] != false {
		t.Errorf("supportsDelegation: got %v, want false", m["supportsDelegation"])
	}
}

func TestCapabilitiesRoundTrip(t *testing.T) {
	in := adapter.Capabilities{
		PathPrefix:          "/mcp",
		Protocol:            "mcp",
		SupportsElicitation: true,
	}
	b, _ := json.Marshal(in)
	var out adapter.Capabilities
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out != in {
		t.Errorf("round-trip: got %+v, want %+v", out, in)
	}
}
