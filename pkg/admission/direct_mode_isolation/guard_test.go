// SPDX-License-Identifier: MIT

package direct_mode_isolation_test

import (
	"strings"
	"testing"

	dmi "github.com/lennylabs/lenny/pkg/admission/direct_mode_isolation"
)

// spec: §4.9 / §13.2 — the lenny-direct-mode-isolation webhook rejects
// the direct/standard and proxy/spiffe-disabled credential-delivery
// combinations in multi-tenant mode.

func TestRejectsDirectModeStandardIsolationInMultiTenant(t *testing.T) {
	d := dmi.Decide(dmi.Request{
		TenancyMode:      dmi.TenancyMulti,
		Kind:             "CredentialPool",
		DeliveryMode:     "direct",
		IsolationProfile: "standard",
	})
	if d.Allowed {
		t.Fatal("direct + standard admitted in multi-tenant mode, want rejection")
	}
	if d.Code != 403 {
		t.Errorf("code = %d, want 403", d.Code)
	}
	if !strings.Contains(d.Reason, dmi.RejectDirectModeStandardIsolation) {
		t.Errorf("reason %q does not carry the rejection code", d.Reason)
	}
	if !strings.Contains(d.Reason, "CredentialPool") {
		t.Errorf("reason %q does not name the resource kind", d.Reason)
	}
	if !strings.Contains(d.Reason, "isolationProfile: sandboxed or microvm") {
		t.Errorf("reason %q does not carry the §4.9 remediation text", d.Reason)
	}
}

func TestRejectsProxyModeSpiffeBindingDisabledInMultiTenant(t *testing.T) {
	d := dmi.Decide(dmi.Request{
		TenancyMode:   dmi.TenancyMulti,
		Kind:          "SandboxTemplate",
		DeliveryMode:  "proxy",
		SpiffeBinding: "disabled",
	})
	if d.Allowed {
		t.Fatal("proxy + spiffeBinding disabled admitted in multi-tenant mode, want rejection")
	}
	if !strings.Contains(d.Reason, dmi.RejectProxyModeSpiffeBindingDisabled) {
		t.Errorf("reason %q does not carry the rejection code", d.Reason)
	}
	if !strings.Contains(d.Reason, "SandboxTemplate") {
		t.Errorf("reason %q does not name the resource kind", d.Reason)
	}
}

func TestAllowsSafeCombinationsInMultiTenant(t *testing.T) {
	for _, tc := range []struct {
		name string
		req  dmi.Request
	}{
		{"direct + sandboxed", dmi.Request{
			TenancyMode: dmi.TenancyMulti, DeliveryMode: "direct", IsolationProfile: "sandboxed",
		}},
		{"direct + microvm", dmi.Request{
			TenancyMode: dmi.TenancyMulti, DeliveryMode: "direct", IsolationProfile: "microvm",
		}},
		{"proxy + standard", dmi.Request{
			TenancyMode: dmi.TenancyMulti, DeliveryMode: "proxy", IsolationProfile: "standard",
		}},
		{"proxy + spiffe enabled", dmi.Request{
			TenancyMode: dmi.TenancyMulti, DeliveryMode: "proxy", SpiffeBinding: "enabled",
		}},
		{"proxy + spiffe default", dmi.Request{
			TenancyMode: dmi.TenancyMulti, DeliveryMode: "proxy",
		}},
		{"no delivery mode", dmi.Request{
			TenancyMode: dmi.TenancyMulti, IsolationProfile: "standard",
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := dmi.Decide(tc.req)
			if !d.Allowed {
				t.Errorf("safe combination rejected: %s", d.Reason)
			}
			if d.Code != 200 {
				t.Errorf("code = %d, want 200", d.Code)
			}
		})
	}
}

func TestAllowsUnsafeCombinationsOutsideMultiTenant(t *testing.T) {
	// The webhook enforces only in multi-tenant mode; single-tenant and
	// development deployments rely on the controller's opt-in fields.
	for _, tc := range []struct {
		name string
		req  dmi.Request
	}{
		{"single-tenant direct + standard", dmi.Request{
			TenancyMode: "single", DeliveryMode: "direct", IsolationProfile: "standard",
		}},
		{"single-tenant proxy + spiffe disabled", dmi.Request{
			TenancyMode: "single", DeliveryMode: "proxy", SpiffeBinding: "disabled",
		}},
		{"devMode multi-tenant direct + standard", dmi.Request{
			TenancyMode: dmi.TenancyMulti, DevMode: true,
			DeliveryMode: "direct", IsolationProfile: "standard",
		}},
		{"empty tenancy mode", dmi.Request{
			DeliveryMode: "direct", IsolationProfile: "standard",
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := dmi.Decide(tc.req)
			if !d.Allowed {
				t.Errorf("combination rejected outside multi-tenant enforcement: %s", d.Reason)
			}
		})
	}
}

// spec: §13.2 lines 438-442 (NET-006) — deliveryMode: proxy with
// egressProfile: provider-direct is mutually exclusive in every tenancy
// mode, so the webhook rejects it before the multi-tenant gate.

func TestRejectsProxyProviderDirectComboInEveryTenancyMode_spec_13_2_NET006(t *testing.T) {
	for _, tc := range []struct {
		name string
		req  dmi.Request
	}{
		{"multi-tenant", dmi.Request{
			TenancyMode: dmi.TenancyMulti, Kind: "SandboxTemplate",
			DeliveryMode: "proxy", EgressProfile: "provider-direct",
		}},
		{"single-tenant", dmi.Request{
			TenancyMode: "single", Kind: "SandboxTemplate",
			DeliveryMode: "proxy", EgressProfile: "provider-direct",
		}},
		{"dev-mode multi-tenant", dmi.Request{
			TenancyMode: dmi.TenancyMulti, DevMode: true, Kind: "SandboxTemplate",
			DeliveryMode: "proxy", EgressProfile: "provider-direct",
		}},
		{"empty tenancy mode", dmi.Request{
			Kind:         "SandboxTemplate",
			DeliveryMode: "proxy", EgressProfile: "provider-direct",
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := dmi.Decide(tc.req)
			if d.Allowed {
				t.Fatalf("proxy + provider-direct admitted in %s, want NET-006 rejection", tc.name)
			}
			if d.Code != 403 {
				t.Errorf("code = %d, want 403", d.Code)
			}
			if !strings.Contains(d.Reason, dmi.RejectInvalidPoolEgressDeliveryCombo) {
				t.Errorf("reason %q does not carry the InvalidPoolEgressDeliveryCombo code", d.Reason)
			}
			if !strings.Contains(d.Reason, "NET-006") {
				t.Errorf("reason %q does not cite NET-006", d.Reason)
			}
		})
	}
}

func TestAllowsCoherentEgressDeliveryPairings_spec_13_2_NET006(t *testing.T) {
	// The two correct pairings (§13.2 line 442) and the benign cases the
	// NET-006 check must not touch.
	for _, tc := range []struct {
		name string
		req  dmi.Request
	}{
		{"proxy + restricted", dmi.Request{
			TenancyMode: "single", DeliveryMode: "proxy", EgressProfile: "restricted",
		}},
		{"direct + provider-direct", dmi.Request{
			TenancyMode: "single", DeliveryMode: "direct", EgressProfile: "provider-direct",
		}},
		{"proxy + empty egress (defaults restricted)", dmi.Request{
			TenancyMode: "single", DeliveryMode: "proxy",
		}},
		{"direct + internet", dmi.Request{
			TenancyMode: "single", DeliveryMode: "direct", EgressProfile: "internet",
		}},
		{"empty delivery + provider-direct", dmi.Request{
			TenancyMode: "single", EgressProfile: "provider-direct",
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := dmi.Decide(tc.req)
			if !d.Allowed {
				t.Errorf("coherent pairing rejected: %s", d.Reason)
			}
		})
	}
}

func TestRejectionNamesResourceWhenKindAbsent(t *testing.T) {
	d := dmi.Decide(dmi.Request{
		TenancyMode:      dmi.TenancyMulti,
		DeliveryMode:     "direct",
		IsolationProfile: "standard",
	})
	if d.Allowed || !strings.Contains(d.Reason, "resource") {
		t.Errorf("reason %q does not fall back to a generic resource noun", d.Reason)
	}
}
