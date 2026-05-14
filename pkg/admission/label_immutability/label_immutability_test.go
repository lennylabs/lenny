// SPDX-License-Identifier: MIT

package label_immutability

import (
	"errors"
	"strings"
	"testing"
)

func TestDecideAllowsNoChange(t *testing.T) {
	labels := map[string]string{
		LabelManaged:       "true",
		LabelDeliveryMode:  "stream",
		LabelEgressProfile: "internet",
		LabelTenantID:      "acme",
	}
	d, err := Decide(Request{OldLabels: labels, NewLabels: labels, UserInfoUsername: "anyone"})
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if !d.Allowed {
		t.Errorf("identical labels should be allowed, got %v", d)
	}
}

func TestDecideRejectsManagedLabelMutation(t *testing.T) {
	d, _ := Decide(Request{
		OldLabels:        map[string]string{LabelManaged: "true"},
		NewLabels:        map[string]string{LabelManaged: "false"},
		UserInfoUsername: GatewayServiceAccount,
	})
	if d.Allowed {
		t.Errorf("lenny.dev/managed mutation must be rejected")
	}
	if !strings.Contains(d.Reason, "lenny.dev/managed is immutable") {
		t.Errorf("Reason should call out the immutable label: %q", d.Reason)
	}
}

func TestDecideRejectsDeliveryModeMutation(t *testing.T) {
	d, _ := Decide(Request{
		OldLabels:        map[string]string{LabelDeliveryMode: "stream"},
		NewLabels:        map[string]string{LabelDeliveryMode: "task"},
		UserInfoUsername: GatewayServiceAccount,
	})
	if d.Allowed {
		t.Errorf("lenny.dev/delivery-mode mutation must be rejected")
	}
}

func TestDecideRejectsEgressProfileMutation(t *testing.T) {
	d, _ := Decide(Request{
		OldLabels:        map[string]string{LabelEgressProfile: "internet"},
		NewLabels:        map[string]string{LabelEgressProfile: "isolated"},
		UserInfoUsername: GatewayServiceAccount,
	})
	if d.Allowed {
		t.Errorf("lenny.dev/egress-profile mutation must be rejected")
	}
}

func TestDecideRejectsImmutableLabelUnsetting(t *testing.T) {
	d, _ := Decide(Request{
		OldLabels:        map[string]string{LabelManaged: "true"},
		NewLabels:        map[string]string{},
		UserInfoUsername: GatewayServiceAccount,
	})
	if d.Allowed {
		t.Errorf("removing lenny.dev/managed must be rejected")
	}
}

// Initial assignment: unset → tenant-id allowed only for gateway SA.
func TestDecideAllowsTenantInitialAssignmentByGateway(t *testing.T) {
	d, err := Decide(Request{
		OldLabels:        map[string]string{LabelManaged: "true"},
		NewLabels:        map[string]string{LabelManaged: "true", LabelTenantID: "acme"},
		UserInfoUsername: GatewayServiceAccount,
	})
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if !d.Allowed {
		t.Errorf("gateway SA must be able to initial-assign tenant-id")
	}
}

func TestDecideRejectsTenantInitialAssignmentByOtherSA(t *testing.T) {
	cases := []string{
		WarmPoolControllerSA,
		PoolScalingControllerSA,
		"system:serviceaccount:lenny-system:other",
		"alice@acme.com",
	}
	for _, user := range cases {
		t.Run(user, func(t *testing.T) {
			d, _ := Decide(Request{
				OldLabels:        map[string]string{LabelManaged: "true"},
				NewLabels:        map[string]string{LabelManaged: "true", LabelTenantID: "acme"},
				UserInfoUsername: user,
			})
			if d.Allowed {
				t.Errorf("user %q must not be permitted to set tenant-id", user)
			}
			if !strings.Contains(d.Reason, "tenant_label_immutable") {
				t.Errorf("Reason should mention tenant_label_immutable: %q", d.Reason)
			}
		})
	}
}

// Pool return: tenant_id → unassigned allowed only for WarmPoolController.
func TestDecideAllowsTenantResetByWarmPoolController(t *testing.T) {
	d, err := Decide(Request{
		OldLabels:        map[string]string{LabelTenantID: "acme"},
		NewLabels:        map[string]string{LabelTenantID: UnassignedTenantID},
		UserInfoUsername: WarmPoolControllerSA,
	})
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if !d.Allowed {
		t.Errorf("WarmPoolController SA must be able to reset tenant-id to unassigned")
	}
}

func TestDecideRejectsTenantResetByOtherSA(t *testing.T) {
	for _, user := range []string{GatewayServiceAccount, "system:serviceaccount:lenny-system:other"} {
		t.Run(user, func(t *testing.T) {
			d, _ := Decide(Request{
				OldLabels:        map[string]string{LabelTenantID: "acme"},
				NewLabels:        map[string]string{LabelTenantID: UnassignedTenantID},
				UserInfoUsername: user,
			})
			if d.Allowed {
				t.Errorf("user %q must not reset tenant-id to unassigned", user)
			}
		})
	}
}

// tenant_id → different tenant_id is always rejected.
func TestDecideRejectsCrossTenantRewrite(t *testing.T) {
	for _, user := range []string{GatewayServiceAccount, WarmPoolControllerSA, PoolScalingControllerSA, "alice"} {
		t.Run(user, func(t *testing.T) {
			d, _ := Decide(Request{
				OldLabels:        map[string]string{LabelTenantID: "acme"},
				NewLabels:        map[string]string{LabelTenantID: "globex"},
				UserInfoUsername: user,
			})
			if d.Allowed {
				t.Errorf("cross-tenant rewrite must be rejected (user %q)", user)
			}
		})
	}
}

// tenant_id → unset is rejected (only "unassigned" sentinel is legal).
func TestDecideRejectsTenantUnset(t *testing.T) {
	d, _ := Decide(Request{
		OldLabels:        map[string]string{LabelTenantID: "acme"},
		NewLabels:        map[string]string{},
		UserInfoUsername: WarmPoolControllerSA,
	})
	if d.Allowed {
		t.Errorf("unsetting tenant-id (empty new value) must be rejected")
	}
}

// unassigned → tenant_id is rejected (a pod returned to the pool may
// only be reassigned by going through the normal claim flow, which
// creates a fresh pod or re-uses an idle pod whose tenant-id is unset).
func TestDecideRejectsReassignFromUnassigned(t *testing.T) {
	d, _ := Decide(Request{
		OldLabels:        map[string]string{LabelTenantID: UnassignedTenantID},
		NewLabels:        map[string]string{LabelTenantID: "acme"},
		UserInfoUsername: GatewayServiceAccount,
	})
	if d.Allowed {
		t.Errorf("unassigned → tenant-id must be rejected")
	}
}

func TestDecideRejectsNilNewLabels(t *testing.T) {
	_, err := Decide(Request{NewLabels: nil})
	if !errors.Is(err, ErrMissingNewLabels) {
		t.Errorf("expected ErrMissingNewLabels, got %v", err)
	}
}

// CREATE path: OldLabels nil. Initial tenant-id assignment must still
// go through the gateway-SA gate.
func TestDecideCreateRejectsNonGatewayInitialTenant(t *testing.T) {
	d, _ := Decide(Request{
		OldLabels:        nil,
		NewLabels:        map[string]string{LabelManaged: "true", LabelTenantID: "acme"},
		UserInfoUsername: WarmPoolControllerSA,
	})
	if d.Allowed {
		t.Errorf("CREATE with tenant-id set by non-gateway SA must be rejected")
	}
}
