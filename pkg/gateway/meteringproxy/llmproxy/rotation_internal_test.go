// SPDX-License-Identifier: MIT

package llmproxy

import (
	"testing"

	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/seam"
)

// TestRotationOfCarriesTheFaultedLeaseIdentityAndTheNextPool checks that
// the rotation request the fallback hands the session core names the
// faulted lease's tenant, session, provider, and coordination generation,
// the pool the chain selected, and the fault trigger.
// spec: GWSEAM-RotateCredential
func TestRotationOfCarriesTheFaultedLeaseIdentityAndTheNextPool(t *testing.T) {
	faulted := credential.Lease{
		LeaseID: "cl-1", TenantID: "acme", SessionID: "s_1",
		Provider: credential.ProviderAnthropicDirect, PoolID: "pool-a",
		CoordinationGeneration: 4,
	}
	got := rotationOf(faulted, "pool-b", credential.TriggerFaultAuthExpired)
	want := seam.CredentialRotation{
		TenantID: "acme", SessionID: "s_1", Provider: "anthropic_direct",
		CoordinationGeneration: 4, NextPool: "pool-b", Trigger: string(credential.TriggerFaultAuthExpired),
	}
	if got != want {
		t.Fatalf("rotationOf = %+v, want %+v", got, want)
	}
}
