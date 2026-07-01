// SPDX-License-Identifier: MIT

package pgstore

import (
	"testing"

	"github.com/lennylabs/lenny/pkg/elicitation"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/poolstore"
)

// TestEncodeDecodeElicitationPolicy_spec_9_2 proves the §9.2 per-pool
// elicitation policy round-trips through the JSONB column encoder, and
// that a pool with no explicit policy encodes to SQL NULL (nil bytes) so
// the dispatcher falls back to the §9.2 platform defaults. F-9.2.12.
func TestEncodeDecodeElicitationPolicy_spec_9_2(t *testing.T) {
	t.Run("empty policy encodes to NULL", func(t *testing.T) {
		raw, err := encodeElicitationPolicy(poolstore.Pool{Name: "p"})
		if err != nil {
			t.Fatalf("encode: %v", err)
		}
		if raw != nil {
			t.Fatalf("empty policy encoded to %q, want nil (SQL NULL)", raw)
		}
		var p poolstore.Pool
		if err := decodeElicitationPolicy(nil, &p); err != nil {
			t.Fatalf("decode NULL: %v", err)
		}
		if p.ElicitationDepthPolicy != "" || p.URLModeElicitation.Enabled {
			t.Errorf("NULL decoded to a non-zero policy: %+v", p)
		}
	})

	t.Run("full policy round-trips", func(t *testing.T) {
		in := poolstore.Pool{
			Name:                       "p",
			ElicitationDepthPolicy:     elicitation.DepthSuppressAtDepth,
			ElicitationSuppressAtDepth: 4,
			URLModeElicitation: elicitation.URLModeAllowlist{
				Enabled:         true,
				DomainAllowlist: []string{"accounts.example.com", "*.login.example.com"},
			},
		}
		raw, err := encodeElicitationPolicy(in)
		if err != nil {
			t.Fatalf("encode: %v", err)
		}
		if raw == nil {
			t.Fatal("a non-empty policy encoded to NULL")
		}
		var out poolstore.Pool
		if err := decodeElicitationPolicy(raw, &out); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if out.ElicitationDepthPolicy != elicitation.DepthSuppressAtDepth {
			t.Errorf("depthPolicy = %q, want suppress_at_depth", out.ElicitationDepthPolicy)
		}
		if out.ElicitationSuppressAtDepth != 4 {
			t.Errorf("suppressAtDepth = %d, want 4", out.ElicitationSuppressAtDepth)
		}
		if !out.URLModeElicitation.Enabled || len(out.URLModeElicitation.DomainAllowlist) != 2 {
			t.Errorf("url-mode = %+v, want enabled with 2 domains", out.URLModeElicitation)
		}
		if out.URLModeElicitation.DomainAllowlist[1] != "*.login.example.com" {
			t.Errorf("domain[1] = %q, want *.login.example.com", out.URLModeElicitation.DomainAllowlist[1])
		}
	})

	t.Run("url-mode enabled with no depth policy round-trips", func(t *testing.T) {
		in := poolstore.Pool{
			Name: "p",
			URLModeElicitation: elicitation.URLModeAllowlist{
				Enabled: true, DomainAllowlist: []string{"accounts.example.com"},
			},
		}
		raw, err := encodeElicitationPolicy(in)
		if err != nil {
			t.Fatalf("encode: %v", err)
		}
		var out poolstore.Pool
		if err := decodeElicitationPolicy(raw, &out); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if out.ElicitationDepthPolicy != "" {
			t.Errorf("depthPolicy = %q, want empty", out.ElicitationDepthPolicy)
		}
		if !out.URLModeElicitation.Enabled {
			t.Error("url-mode enabled flag lost in round-trip")
		}
	})
}
