// SPDX-License-Identifier: MIT

package preflight

import "strings"

// T4DefaultEncryptionCheck is the §17.6 install/upgrade backstop to the
// gateway-startup T4 default-encryption assertion. On the GCS V4
// signed-URL and Azure SAS checkpoint PUT paths the presigned capability
// signs no encryption header, so a workspaceTier T4 tenant's per-tenant
// encryption rests on a backend default the mint cannot bind per request:
// a per-tenant GCS bucket-default CMEK, or an Azure container-level
// default encryption scope pinned with DenyEncryptionScopeOverride. A
// gcs or azure deployment serving a T4 tenant without that default would
// silently write T4 checkpoints under the deployment-wide key, defeating
// the §12.9 cryptographic-erasure property. Because the mint issues no
// encryption header there and cannot fail closed at request time, the
// gate fails closed at install/upgrade instead.
//
// The SigV4 backends (minio, s3) fold the SSE-KMS headers into the
// signature and fail closed at request time, so the gate does not apply
// to them; a deployment serving no T4 tenant has no per-tenant default to
// verify.
//
// spec: §12.5 line 315 (fail-closed T4); §17.9.7 (object-store
// backend-invariant requirements); §17.6 (Checks performed).
type T4DefaultEncryptionCheck struct {
	// Provider is the objectStorage.provider value (minio | s3 | gcs |
	// azure). Only gcs and azure are gated.
	Provider string
	// ServesT4Tenant is true when the deployment serves any workspaceTier
	// T4 tenant. False leaves the check a no-op: there is no per-tenant
	// default to verify.
	ServesT4Tenant bool
	// GCSBucketDefaultCMEK is the objectStorage.gcs.bucketDefaultCmek
	// value: the per-tenant bucket-default CMEK the T4 checkpoint PUT
	// inherits on GCS.
	GCSBucketDefaultCMEK string
	// AzureDefaultEncryptionScope is the
	// objectStorage.azure.defaultEncryptionScope value: the container-level
	// default encryption scope the T4 chunk PUT lands under on Azure.
	AzureDefaultEncryptionScope string
	// AzureDenyEncryptionScopeOverride is the
	// objectStorage.azure.denyEncryptionScopeOverride value: override
	// prevention pinning the container default so a chunk PUT cannot land
	// under any other scope.
	AzureDenyEncryptionScopeOverride bool
}

// Decide fails the install/upgrade closed when a gcs or azure backend
// serving a T4 tenant does not declare the required backend-default
// encryption. It mirrors the gateway-startup assertion so the two
// enforcement points stay in lock-step.
func (c T4DefaultEncryptionCheck) Decide() Decision {
	if !c.ServesT4Tenant {
		return Decision{Passed: true}
	}
	switch strings.ToLower(strings.TrimSpace(c.Provider)) {
	case "gcs":
		if strings.TrimSpace(c.GCSBucketDefaultCMEK) == "" {
			return Decision{Passed: false, Reason: "CHECKPOINT_T4_DEFAULT_ENCRYPTION_MISSING: " +
				"objectStorage.provider=gcs serves a workspaceTier T4 tenant but declares no " +
				"per-tenant bucket-default CMEK; set objectStorage.gcs.bucketDefaultCmek — the GCS " +
				"V4 signed URL cannot carry a per-request CMEK, so the T4 checkpoint PUT inherits " +
				"the bucket default and the install fails closed without it (§12.5 line 315, §17.6)"}
		}
	case "azure":
		if strings.TrimSpace(c.AzureDefaultEncryptionScope) == "" {
			return Decision{Passed: false, Reason: "CHECKPOINT_T4_DEFAULT_ENCRYPTION_MISSING: " +
				"objectStorage.provider=azure serves a workspaceTier T4 tenant but declares no " +
				"container default encryption scope; set objectStorage.azure.defaultEncryptionScope — " +
				"the Azure SAS carries no encryption scope, so the T4 chunk PUT lands under the " +
				"container default and the install fails closed without it (§12.5 line 315, §17.6)"}
		}
		if !c.AzureDenyEncryptionScopeOverride {
			return Decision{Passed: false, Reason: "CHECKPOINT_T4_DEFAULT_ENCRYPTION_MISSING: " +
				"objectStorage.provider=azure serves a workspaceTier T4 tenant with a container " +
				"default encryption scope but no override prevention; set " +
				"objectStorage.azure.denyEncryptionScopeOverride=true so a chunk PUT cannot land " +
				"under any other scope (§12.5 line 315, §17.6)"}
		}
	}
	return Decision{Passed: true}
}
