// SPDX-License-Identifier: MIT

package preflight_test

import (
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/preflight"
)

// TestT4DefaultEncryptionCheckFailsClosed pins the §17.6 install/upgrade
// backstop to the gateway-startup T4 default-encryption assertion. On the
// GCS V4 signed-URL and Azure SAS checkpoint PUT paths the presigned
// capability signs no encryption header, so a workspaceTier T4 tenant's
// per-tenant encryption rests on a backend default the mint cannot bind
// per request. Before this gate existed a gcs/azure deployment serving a
// T4 tenant without that default could install and silently write T4
// checkpoints under the deployment-wide key, defeating the §12.9
// cryptographic-erasure property. The deny-path cases below fail against
// pre-fix code, which registered no such check.
//
// spec: §12.5 line 315 (fail-closed T4); §17.9.7; §17.6 (Checks performed).
func TestT4DefaultEncryptionCheckFailsClosed(t *testing.T) {
	cases := []struct {
		name          string
		check         preflight.T4DefaultEncryptionCheck
		wantPass      bool
		wantSubstring string
	}{
		{
			name: "gcs serving T4 without bucket-default CMEK fails closed",
			check: preflight.T4DefaultEncryptionCheck{
				Provider:       "gcs",
				ServesT4Tenant: true,
			},
			wantPass:      false,
			wantSubstring: "objectStorage.gcs.bucketDefaultCmek",
		},
		{
			name: "gcs serving T4 with whitespace-only CMEK fails closed",
			check: preflight.T4DefaultEncryptionCheck{
				Provider:             "gcs",
				ServesT4Tenant:       true,
				GCSBucketDefaultCMEK: "   ",
			},
			wantPass:      false,
			wantSubstring: "objectStorage.gcs.bucketDefaultCmek",
		},
		{
			name: "gcs serving T4 with bucket-default CMEK passes",
			check: preflight.T4DefaultEncryptionCheck{
				Provider:             "gcs",
				ServesT4Tenant:       true,
				GCSBucketDefaultCMEK: "projects/acme/locations/us/keyRings/lenny/cryptoKeys/tenant-alice",
			},
			wantPass: true,
		},
		{
			name: "azure serving T4 without encryption scope fails closed",
			check: preflight.T4DefaultEncryptionCheck{
				Provider:                         "azure",
				ServesT4Tenant:                   true,
				AzureDenyEncryptionScopeOverride: true,
			},
			wantPass:      false,
			wantSubstring: "objectStorage.azure.defaultEncryptionScope",
		},
		{
			name: "azure serving T4 with scope but no override prevention fails closed",
			check: preflight.T4DefaultEncryptionCheck{
				Provider:                    "azure",
				ServesT4Tenant:              true,
				AzureDefaultEncryptionScope: "lenny-tenant-alice",
			},
			wantPass:      false,
			wantSubstring: "objectStorage.azure.denyEncryptionScopeOverride",
		},
		{
			name: "azure serving T4 with scope and override prevention passes",
			check: preflight.T4DefaultEncryptionCheck{
				Provider:                         "azure",
				ServesT4Tenant:                   true,
				AzureDefaultEncryptionScope:      "lenny-tenant-alice",
				AzureDenyEncryptionScopeOverride: true,
			},
			wantPass: true,
		},
		{
			name: "gcs serving no T4 tenant passes without CMEK",
			check: preflight.T4DefaultEncryptionCheck{
				Provider:       "gcs",
				ServesT4Tenant: false,
			},
			wantPass: true,
		},
		{
			name: "azure serving no T4 tenant passes without scope",
			check: preflight.T4DefaultEncryptionCheck{
				Provider:       "azure",
				ServesT4Tenant: false,
			},
			wantPass: true,
		},
		{
			// The SigV4 backends fold the SSE-KMS headers into the
			// signature and fail closed at request time, so the gate does
			// not apply to them even when a T4 tenant is served.
			name: "minio serving T4 is not gated",
			check: preflight.T4DefaultEncryptionCheck{
				Provider:       "minio",
				ServesT4Tenant: true,
			},
			wantPass: true,
		},
		{
			name: "s3 serving T4 is not gated",
			check: preflight.T4DefaultEncryptionCheck{
				Provider:       "s3",
				ServesT4Tenant: true,
			},
			wantPass: true,
		},
		{
			// Provider comparison is case-insensitive so an uppercase chart
			// value still trips the gate.
			name: "uppercase GCS provider serving T4 without CMEK fails closed",
			check: preflight.T4DefaultEncryptionCheck{
				Provider:       "GCS",
				ServesT4Tenant: true,
			},
			wantPass:      false,
			wantSubstring: "objectStorage.gcs.bucketDefaultCmek",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := c.check.Decide()
			if d.Passed != c.wantPass {
				t.Fatalf("Decide() passed=%v, want %v (reason: %q)", d.Passed, c.wantPass, d.Reason)
			}
			if !c.wantPass && !strings.Contains(d.Reason, "CHECKPOINT_T4_DEFAULT_ENCRYPTION_MISSING") {
				t.Errorf("failure reason %q does not carry the T4 default-encryption error code", d.Reason)
			}
			if c.wantSubstring != "" && !strings.Contains(d.Reason, c.wantSubstring) {
				t.Errorf("reason %q does not name the missing config key %q", d.Reason, c.wantSubstring)
			}
		})
	}
}
