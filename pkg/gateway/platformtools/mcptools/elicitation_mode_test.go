// SPDX-License-Identifier: MIT

package mcptools_test

import (
	"context"
	"errors"
	"testing"

	"github.com/lennylabs/lenny/pkg/elicitation"
	"github.com/lennylabs/lenny/pkg/gateway/platformtools/mcptools"
	"github.com/lennylabs/lenny/pkg/gateway/seam"
)

// integrityFacts answers the tenant integrity mode, or fails every read.
type integrityFacts struct {
	mode string
	err  error
}

func (f integrityFacts) TenantElicitationIntegrity(context.Context, string) (string, error) {
	return f.mode, f.err
}

func (integrityFacts) TenantCredentialProviders(context.Context, string) ([]string, error) {
	return nil, nil
}

func (integrityFacts) DelegatorRefusal(context.Context, string, string) (*seam.Refusal, error) {
	return nil, nil
}

// TestTenantElicitationModesTakesTheStricterOfFloorAndTenant checks that the
// effective mode is the stricter of the platform floor and the tenant's
// stored mode, and that a failed read or no tenant facts resolves as a
// tenant that stored no mode.
// spec: §9.2 (Elicitation Chain)
func TestTenantElicitationModesTakesTheStricterOfFloorAndTenant(t *testing.T) {
	ctx := context.Background()
	floor := func(f string) func() string { return func() string { return f } }
	cases := []struct {
		name    string
		floor   string
		tenants seam.TenantFacts
	}{
		{"tenant stricter", string(elicitation.ModeOff), integrityFacts{mode: string(elicitation.ModeEnforce)}},
		{"floor stricter", string(elicitation.ModeEnforce), integrityFacts{mode: string(elicitation.ModeOff)}},
		{"read fails", string(elicitation.ModeOff), integrityFacts{err: errors.New("tenant store down")}},
		{"no tenant facts", string(elicitation.ModeDetectOnly), nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stored := ""
			if f, ok := tc.tenants.(integrityFacts); ok && f.err == nil {
				stored = f.mode
			}
			want := elicitation.ResolveEffectiveWithDefaults(tc.floor, stored)
			if got := mcptools.TenantElicitationModes(floor(tc.floor), tc.tenants)(ctx, "acme"); got != want {
				t.Errorf("mode = %q, want %q", got, want)
			}
		})
	}
	if got := mcptools.TenantElicitationModes(floor(string(elicitation.ModeOff)), integrityFacts{mode: string(elicitation.ModeEnforce)})(ctx, "acme"); got != elicitation.ModeEnforce {
		t.Errorf("an enforcing tenant under an off floor = %q, want enforce", got)
	}
}
