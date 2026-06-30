// SPDX-License-Identifier: MIT

package poolstore_test

import (
	"context"
	"errors"
	"testing"

	"github.com/lennylabs/lenny/pkg/elicitation"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/poolstore"
)

// TestValidateElicitationPolicy_spec_9_2 exercises the §9.2 per-pool
// elicitation policy admission rules: a url-mode allowlist that enables
// agent-initiated url-mode must name at least one domain (line 86), and
// the depth policy, when set, must be a recognised enum value (lines
// 94-96). F-9.2.12.
func TestValidateElicitationPolicy_spec_9_2(t *testing.T) {
	cases := []struct {
		name    string
		pool    poolstore.Pool
		wantErr error // nil, sentinel, or "any"
		anyErr  bool
	}{
		{
			name: "url-mode enabled with empty allowlist is rejected",
			pool: poolstore.Pool{
				Name:               "p",
				URLModeElicitation: elicitation.URLModeAllowlist{Enabled: true},
			},
			wantErr: poolstore.ErrURLModeDomainRequired,
		},
		{
			name: "url-mode enabled with blank-only allowlist is rejected",
			pool: poolstore.Pool{
				Name: "p",
				URLModeElicitation: elicitation.URLModeAllowlist{
					Enabled: true, DomainAllowlist: []string{"  ", ""},
				},
			},
			wantErr: poolstore.ErrURLModeDomainRequired,
		},
		{
			name: "url-mode enabled with a domain is accepted",
			pool: poolstore.Pool{
				Name: "p",
				URLModeElicitation: elicitation.URLModeAllowlist{
					Enabled: true, DomainAllowlist: []string{"accounts.example.com"},
				},
			},
		},
		{
			name: "url-mode disabled is accepted regardless of allowlist",
			pool: poolstore.Pool{Name: "p"},
		},
		{
			name:   "invalid depth policy is rejected",
			pool:   poolstore.Pool{Name: "p", ElicitationDepthPolicy: elicitation.DepthPolicy("sometimes")},
			anyErr: true,
		},
		{
			name: "valid depth policy is accepted",
			pool: poolstore.Pool{Name: "p", ElicitationDepthPolicy: elicitation.DepthBlockAll},
		},
		{
			name: "empty depth policy is accepted (resolves to platform default)",
			pool: poolstore.Pool{Name: "p"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := poolstore.ValidateElicitationPolicy(tc.pool)
			switch {
			case tc.wantErr != nil:
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v, want %v", err, tc.wantErr)
				}
			case tc.anyErr:
				if err == nil {
					t.Fatal("expected an error, got nil")
				}
			default:
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
			}
		})
	}
}

// TestCreateRejectsURLModeWithoutDomain_spec_9_2_86 proves the §9.2 line
// 86 admission rule fires at the store boundary so a misconfigured pool
// never enters the registry. F-9.2.12.
func TestCreateRejectsURLModeWithoutDomain_spec_9_2_86(t *testing.T) {
	s := poolstore.NewMemory()
	err := s.Create(context.Background(), poolstore.Pool{
		Name:               "p",
		URLModeElicitation: elicitation.URLModeAllowlist{Enabled: true},
	})
	if !errors.Is(err, poolstore.ErrURLModeDomainRequired) {
		t.Fatalf("Create err = %v, want ErrURLModeDomainRequired", err)
	}
}

// TestUpdateRejectsURLModeWithoutDomain_spec_9_2_86 proves a PUT that
// enables url-mode without a domain is rejected on Update too. F-9.2.12.
func TestUpdateRejectsURLModeWithoutDomain_spec_9_2_86(t *testing.T) {
	s := poolstore.NewMemory()
	if err := s.Create(context.Background(), poolstore.Pool{Name: "p"}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	_, err := s.Update(context.Background(), "p", func(p *poolstore.Pool) error {
		p.URLModeElicitation = elicitation.URLModeAllowlist{Enabled: true}
		return nil
	})
	if !errors.Is(err, poolstore.ErrURLModeDomainRequired) {
		t.Fatalf("Update err = %v, want ErrURLModeDomainRequired", err)
	}
}

// TestCreatePersistsElicitationPolicy_spec_9_2 proves the §9.2 per-pool
// policy round-trips through the Memory store without slice aliasing.
// F-9.2.12.
func TestCreatePersistsElicitationPolicy_spec_9_2(t *testing.T) {
	s := poolstore.NewMemory()
	domains := []string{"accounts.example.com", "*.login.example.com"}
	if err := s.Create(context.Background(), poolstore.Pool{
		Name:                       "p",
		ElicitationDepthPolicy:     elicitation.DepthSuppressAtDepth,
		ElicitationSuppressAtDepth: 2,
		URLModeElicitation: elicitation.URLModeAllowlist{
			Enabled: true, DomainAllowlist: domains,
		},
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Mutate the caller's slice after Create; the store must not observe it.
	domains[0] = "evil.test"

	got, err := s.Get(context.Background(), "p")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ElicitationDepthPolicy != elicitation.DepthSuppressAtDepth {
		t.Errorf("depthPolicy = %q, want suppress_at_depth", got.ElicitationDepthPolicy)
	}
	if got.ElicitationSuppressAtDepth != 2 {
		t.Errorf("suppressAtDepth = %d, want 2", got.ElicitationSuppressAtDepth)
	}
	if !got.URLModeElicitation.Enabled || got.URLModeElicitation.DomainAllowlist[0] != "accounts.example.com" {
		t.Errorf("url-mode = %+v, want enabled with the original first domain (no aliasing)", got.URLModeElicitation)
	}
	// Mutating the returned slice must not corrupt the store either.
	got.URLModeElicitation.DomainAllowlist[1] = "tampered.test"
	again, _ := s.Get(context.Background(), "p")
	if again.URLModeElicitation.DomainAllowlist[1] != "*.login.example.com" {
		t.Errorf("returned slice aliases the store: %+v", again.URLModeElicitation)
	}
}
