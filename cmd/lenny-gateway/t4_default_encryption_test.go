// SPDX-License-Identifier: MIT

package main

import (
	"strings"
	"testing"

	blobproviderflags "github.com/lennylabs/lenny/pkg/blobstore/providerflags"
)

// spec: 12.5 line 315 (fail-closed T4), 12.5 line 321-325 (per-provider
// T4 mint invariants), 17.9.7 (object-store backend-invariant
// requirements).
//
// TestT4DefaultEncryptionAssertionFailsClosed pins the gateway-startup
// fail-closed replacement for the SigV4 signature binding that the GCS V4
// signed-URL and Azure SAS checkpoint PUT paths cannot carry per request.
// On these two backends the presigned capability signs no encryption
// header, so a workspaceTier T4 tenant's per-tenant encryption rests on a
// backend default: a per-tenant GCS bucket-default CMEK, or an Azure
// container-level default encryption scope pinned with
// DenyEncryptionScopeOverride. Before this gate existed a gcs/azure
// deployment serving a T4 tenant without that default could boot and
// silently write T4 checkpoints under the deployment-wide key, defeating
// the §12.9 cryptographic-erasure property. The deny-path cases below fail
// against pre-fix code, which had no such assertion.
func TestT4DefaultEncryptionAssertionFailsClosed(t *testing.T) {
	cases := []struct {
		name          string
		provider      string
		servesT4      bool
		cfg           t4DefaultEncryptionConfig
		wantErr       bool
		wantSubstring string
	}{
		{
			name:          "gcs serving T4 without bucket-default CMEK fails closed",
			provider:      blobproviderflags.ProviderGCS,
			servesT4:      true,
			cfg:           t4DefaultEncryptionConfig{},
			wantErr:       true,
			wantSubstring: "bucketDefaultCmek",
		},
		{
			name:     "gcs serving T4 with bucket-default CMEK boots",
			provider: blobproviderflags.ProviderGCS,
			servesT4: true,
			cfg:      t4DefaultEncryptionConfig{gcsBucketDefaultCMEK: "projects/acme/locations/us/keyRings/lenny/cryptoKeys/tenant-alice"},
			wantErr:  false,
		},
		{
			name:          "gcs serving T4 with whitespace-only CMEK fails closed",
			provider:      blobproviderflags.ProviderGCS,
			servesT4:      true,
			cfg:           t4DefaultEncryptionConfig{gcsBucketDefaultCMEK: "   "},
			wantErr:       true,
			wantSubstring: "bucketDefaultCmek",
		},
		{
			name:          "azure serving T4 without encryption scope fails closed",
			provider:      blobproviderflags.ProviderAzure,
			servesT4:      true,
			cfg:           t4DefaultEncryptionConfig{azureDenyEncryptionScopeOverride: true},
			wantErr:       true,
			wantSubstring: "defaultEncryptionScope",
		},
		{
			name:          "azure serving T4 with scope but no override prevention fails closed",
			provider:      blobproviderflags.ProviderAzure,
			servesT4:      true,
			cfg:           t4DefaultEncryptionConfig{azureDefaultEncryptionScope: "lenny-tenant-alice"},
			wantErr:       true,
			wantSubstring: "denyEncryptionScopeOverride",
		},
		{
			name:     "azure serving T4 with scope and override prevention boots",
			provider: blobproviderflags.ProviderAzure,
			servesT4: true,
			cfg: t4DefaultEncryptionConfig{
				azureDefaultEncryptionScope:      "lenny-tenant-alice",
				azureDenyEncryptionScopeOverride: true,
			},
			wantErr: false,
		},
		{
			name:     "gcs serving no T4 tenant boots without CMEK",
			provider: blobproviderflags.ProviderGCS,
			servesT4: false,
			cfg:      t4DefaultEncryptionConfig{},
			wantErr:  false,
		},
		{
			name:     "azure serving no T4 tenant boots without scope",
			provider: blobproviderflags.ProviderAzure,
			servesT4: false,
			cfg:      t4DefaultEncryptionConfig{},
			wantErr:  false,
		},
		{
			// The SigV4 backends fold the SSE-KMS key into the signature
			// and fail closed at request time, so this startup gate does
			// not apply to them even when a T4 tenant is served.
			name:     "minio serving T4 is not gated at startup",
			provider: blobproviderflags.ProviderMinIO,
			servesT4: true,
			cfg:      t4DefaultEncryptionConfig{},
			wantErr:  false,
		},
		{
			name:     "s3 serving T4 is not gated at startup",
			provider: blobproviderflags.ProviderS3,
			servesT4: true,
			cfg:      t4DefaultEncryptionConfig{},
			wantErr:  false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validateT4DefaultEncryption(c.provider, c.servesT4, c.cfg)
			if c.wantErr && err == nil {
				t.Fatalf("validateT4DefaultEncryption(%q, %v, %+v) = nil, want error", c.provider, c.servesT4, c.cfg)
			}
			if !c.wantErr && err != nil {
				t.Fatalf("validateT4DefaultEncryption(%q, %v, %+v) = %v, want nil", c.provider, c.servesT4, c.cfg, err)
			}
			if c.wantErr && c.wantSubstring != "" && !strings.Contains(err.Error(), c.wantSubstring) {
				t.Fatalf("error %q does not name the missing config key %q", err.Error(), c.wantSubstring)
			}
		})
	}
}
