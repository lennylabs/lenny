// SPDX-License-Identifier: MIT

package sandbox

import (
	"testing"

	"github.com/lennylabs/lenny/pkg/adapter/sharedassets"
	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
)

// TestEncodeSharedAssets_RoundTripsThroughAdapterDecode confirms the §6.4
// line 409 wiring: the controller encodes a Runtime's inline sharedAssets
// into the form the adapter decodes, preserving path, content, and mode.
func TestEncodeSharedAssets_RoundTripsThroughAdapterDecode_spec_6_4(t *testing.T) {
	assets := []lennyv1.SharedAsset{
		{Path: "config/app.yaml", Content: "k: v\n", Mode: "0444"},
		{Path: "lib/data.txt", Content: "payload"},
	}
	enc, err := encodeSharedAssets(assets)
	if err != nil {
		t.Fatalf("encodeSharedAssets: %v", err)
	}
	if enc == "" {
		t.Fatal("encodeSharedAssets returned empty for a non-empty list")
	}
	got, err := sharedassets.Decode(enc)
	if err != nil {
		t.Fatalf("adapter Decode: %v", err)
	}
	if len(got) != len(assets) {
		t.Fatalf("decoded %d specs, want %d", len(got), len(assets))
	}
	for i, a := range assets {
		if got[i].Path != a.Path || got[i].Content != a.Content || got[i].Mode != a.Mode {
			t.Errorf("spec[%d] = %+v, want path=%q content=%q mode=%q",
				i, got[i], a.Path, a.Content, a.Mode)
		}
	}
}

// TestEncodeSharedAssets_EmptyYieldsEmptyString confirms a Runtime with no
// sharedAssets produces the empty flag value, which the pod builder reads
// as "mount /workspace/shared empty and read-only".
func TestEncodeSharedAssets_EmptyYieldsEmptyString_spec_6_4(t *testing.T) {
	for _, in := range [][]lennyv1.SharedAsset{nil, {}} {
		enc, err := encodeSharedAssets(in)
		if err != nil {
			t.Fatalf("encodeSharedAssets(%v): %v", in, err)
		}
		if enc != "" {
			t.Errorf("encodeSharedAssets(%v) = %q, want empty string", in, enc)
		}
	}
}
