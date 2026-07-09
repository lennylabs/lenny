// SPDX-License-Identifier: MIT

//go:build e2e_cloud

// Tier-6 live tests that exercise the per-release GCP and Azure
// resources deploy/terraform/cloud/{gcp,azure} provision, mirroring
// aws_resources_test.go's AWS S3 / AWS KMS round-trip against the
// pkg/blobstore/{gcs,azureblob} Store implementations and the
// pkg/kms/{gcp,azure} Provider implementations. Each test requires
// LENNY_CLOUD_PROVIDER set to the matching provider plus the
// Terraform-output env vars the helpers below read
// (LENNY_GCP_KMS_KEY_ID / LENNY_GCP_ARTIFACT_BUCKET /
// LENNY_AZURE_KEY_VAULT_KEY_ID / LENNY_AZURE_ARTIFACT_CONTAINER_URL).
// scripts/cloud/{gcp,azure}/up.sh writes those env vars into a
// `.env` file the test runner sources before the invocation.

package tier6_e2e_cloud_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"

	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/blobstore/azureblob"
	"github.com/lennylabs/lenny/pkg/blobstore/gcs"
	"github.com/lennylabs/lenny/pkg/kms"
	kmsazure "github.com/lennylabs/lenny/pkg/kms/azure"
	kmsgcp "github.com/lennylabs/lenny/pkg/kms/gcp"
)

// spec: 12.6 (tier 6 — E2E on cloud: "cloud_kms | T4 per-tenant keys
// against the provider's native KMS: Cloud KMS on GKE, AWS KMS on
// EKS, Azure Key Vault on AKS. KMS probe, key rotation,
// key-unavailable fail-closed validated for each")
// diagnosis: TestCloudKMSGCP exercises pkg/kms/gcp.Provider end-to-end
// against the Cloud KMS key the deploy/terraform/cloud/gcp module
// emits as the kms_key_id output. A successful WrapDEK + UnwrapDEK
// round-trip plus the cross-alias rejection confirms the GCP
// Workload Identity binding, the AdditionalAuthenticatedData alias
// binding, and the pkg/kms/gcp error mapping all match the
// documented §12.6 interface-parity contract already exercised
// against AWS KMS in TestCloudKMS.
func TestCloudKMSGCP(t *testing.T) {
	p := requireCloud(t)
	if p != "gcp" {
		t.Logf("TestCloudKMSGCP: GCP Cloud KMS test runs against gcp; LENNY_CLOUD_PROVIDER=%q", p)
		return
	}
	keyID := requireEnv(t, "LENNY_GCP_KMS_KEY_ID")
	if keyID == "" {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	prov, err := kmsgcp.New(ctx, kmsgcp.Config{
		AliasToKeyName: map[string]string{
			"platform:tier6-test": keyID,
		},
	})
	if err != nil {
		t.Fatalf("kmsgcp.New: %v", err)
	}

	dek := make([]byte, kms.DEKSize)
	if _, err := rand.Read(dek); err != nil {
		t.Fatalf("rand.Read: %v", err)
	}

	wrapped, err := prov.WrapDEK(ctx, "platform:tier6-test", dek)
	if err != nil {
		t.Fatalf("WrapDEK: %v", err)
	}
	if len(wrapped.Ciphertext) == 0 {
		t.Fatal("WrapDEK returned empty ciphertext")
	}
	got, err := prov.UnwrapDEK(ctx, "platform:tier6-test", wrapped)
	if err != nil {
		t.Fatalf("UnwrapDEK: %v", err)
	}
	if !bytes.Equal(got, dek) {
		t.Errorf("round-trip mismatch: got %x, want %x", got, dek)
	}

	// Cross-alias rejection: a wrapped DEK under platform:tier6-test
	// must not unwrap under a different alias. Cloud KMS's
	// AdditionalAuthenticatedData is the cryptographic binding.
	prov.SetAlias("tenant:wrong", keyID)
	if _, err := prov.UnwrapDEK(ctx, "tenant:wrong", wrapped); err == nil {
		t.Error("UnwrapDEK under wrong alias should fail (AAD mismatch)")
	}
}

// spec: 12.6 (tier 6 — E2E on cloud: "cloud_csi | ArtifactStore
// against the provider's native object storage: GCS on GKE, S3 on
// EKS, Azure Blob Storage on AKS. The same interface tests pass
// against every backend")
// diagnosis: TestCloudCSIGCS exercises pkg/blobstore/gcs.Store
// end-to-end against the GCS bucket the deploy/terraform/cloud/gcp
// module emits as the artifact_bucket output. A successful Put + Get
// + Stat + SoftDelete round-trip confirms the bucket IAM binding and
// the blobstore/gcs error mapping match the documented contract
// already exercised against S3 in TestCloudCSI, satisfying the
// "same interface tests pass against every backend" claim for GCS.
func TestCloudCSIGCS(t *testing.T) {
	p := requireCloud(t)
	if p != "gcp" {
		t.Logf("TestCloudCSIGCS: GCS test runs against gcp; LENNY_CLOUD_PROVIDER=%q", p)
		return
	}
	bucket := requireEnv(t, "LENNY_GCP_ARTIFACT_BUCKET")
	if bucket == "" {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	store, err := gcs.New(ctx, gcs.Config{Bucket: bucket})
	if err != nil {
		t.Fatalf("gcs.New: %v", err)
	}

	u := blobstore.URI{
		TenantID:  "acme",
		SessionID: "tier6-session-" + time.Now().UTC().Format("20060102T150405"),
		PartID:    "part1",
	}
	payload := []byte("tier-6 e2e_cloud round-trip payload (GCS)")

	if _, err := store.Put(u, "application/octet-stream", bytes.NewReader(payload)); err != nil {
		t.Fatalf("Put: %v", err)
	}
	// Best-effort cleanup so the bucket does not accumulate test
	// objects across runs. SoftDelete writes the tombstone tag;
	// HardPrune with zero retention drives the matching delete
	// against the object the test just put in.
	t.Cleanup(func() {
		_ = store.SoftDelete(u)
		store.HardPrune(time.Now(), 0)
	})

	info, body, err := store.Get(u)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer body.Close()
	if info.Size != int64(len(payload)) {
		t.Errorf("info.Size = %d, want %d", info.Size, len(payload))
	}
	got, err := io.ReadAll(body)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Errorf("body mismatch: got %q, want %q", got, payload)
	}

	stat, err := store.Stat(u)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if stat.Size != int64(len(payload)) {
		t.Errorf("stat.Size = %d, want %d", stat.Size, len(payload))
	}

	if err := store.SoftDelete(u); err != nil {
		t.Fatalf("SoftDelete: %v", err)
	}
	if _, _, err := store.Get(u); err != blobstore.ErrNotFound {
		t.Errorf("Get after SoftDelete: %v, want ErrNotFound", err)
	}
}

// azureCredential resolves an Azure credential through the standard
// chain (managed identity, workload identity, env vars, az login),
// mirroring the loadAzureCredential helper the managed_azure*_test.go
// files each inline. Skips (via t.Logf + empty return handled by the
// caller) rather than t.Fatal, matching loadAWSConfig's
// unauthenticated-CLI diagnosis.
func azureCredential(t *testing.T) (cred *azidentity.DefaultAzureCredential, ok bool) {
	t.Helper()
	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		t.Logf("azureCredential: build Azure credential: %v (run `az login`)", err)
		return nil, false
	}
	return cred, true
}

// parseAzureKeyVaultKeyID splits a versioned Key Vault key identifier
// ("https://<vault>.vault.azure.net/keys/<name>/<version>") into the
// vault URL, key name, and key version the pkg/kms/azure.Config
// AliasToKey map wants.
func parseAzureKeyVaultKeyID(keyID string) (vaultURL, name, version string, err error) {
	u, err := url.Parse(keyID)
	if err != nil {
		return "", "", "", fmt.Errorf("parse %q: %w", keyID, err)
	}
	segs := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(segs) < 3 || segs[0] != "keys" {
		return "", "", "", fmt.Errorf("key id %q does not match https://<vault>/keys/<name>/<version>", keyID)
	}
	return u.Scheme + "://" + u.Host, segs[1], segs[2], nil
}

// parseAzureContainerURL splits an artifact-container URL
// ("https://<account>.blob.core.windows.net/<container>") into the
// service URL and container name the pkg/blobstore/azureblob.Config
// wants.
func parseAzureContainerURL(containerURL string) (serviceURL, container string, err error) {
	u, err := url.Parse(containerURL)
	if err != nil {
		return "", "", fmt.Errorf("parse %q: %w", containerURL, err)
	}
	container = strings.Trim(u.Path, "/")
	if container == "" {
		return "", "", fmt.Errorf("container URL %q carries no container path segment", containerURL)
	}
	return u.Scheme + "://" + u.Host, container, nil
}

// spec: 12.6 (tier 6 — E2E on cloud: "cloud_kms | T4 per-tenant keys
// against the provider's native KMS: Cloud KMS on GKE, AWS KMS on
// EKS, Azure Key Vault on AKS. KMS probe, key rotation,
// key-unavailable fail-closed validated for each")
// diagnosis: TestCloudKMSAzure exercises pkg/kms/azure.Provider
// end-to-end against the Key Vault key the
// deploy/terraform/cloud/azure module emits as the key_vault_key_id
// output. A successful WrapDEK + UnwrapDEK round-trip plus the
// cross-alias rejection confirms the Azure Workload Identity
// binding, the AdditionalAuthenticatedData alias binding, and the
// pkg/kms/azure error mapping all match the documented §12.6
// interface-parity contract already exercised against AWS KMS in
// TestCloudKMS.
func TestCloudKMSAzure(t *testing.T) {
	p := requireCloud(t)
	if p != "azure" {
		t.Logf("TestCloudKMSAzure: Azure Key Vault test runs against azure; LENNY_CLOUD_PROVIDER=%q", p)
		return
	}
	keyID := requireEnv(t, "LENNY_AZURE_KEY_VAULT_KEY_ID")
	if keyID == "" {
		return
	}
	vaultURL, name, version, err := parseAzureKeyVaultKeyID(keyID)
	if err != nil {
		t.Fatalf("parseAzureKeyVaultKeyID: %v", err)
	}
	cred, ok := azureCredential(t)
	if !ok {
		return
	}

	prov, err := kmsazure.New(kmsazure.Config{
		VaultURL:   vaultURL,
		Credential: cred,
		AliasToKey: map[string]kmsazure.KeyRef{
			"platform:tier6-test": {Name: name, Version: version},
		},
	})
	if err != nil {
		t.Fatalf("kmsazure.New: %v", err)
	}

	dek := make([]byte, kms.DEKSize)
	if _, err := rand.Read(dek); err != nil {
		t.Fatalf("rand.Read: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	wrapped, err := prov.WrapDEK(ctx, "platform:tier6-test", dek)
	if err != nil {
		t.Fatalf("WrapDEK: %v", err)
	}
	if len(wrapped.Ciphertext) == 0 {
		t.Fatal("WrapDEK returned empty ciphertext")
	}
	got, err := prov.UnwrapDEK(ctx, "platform:tier6-test", wrapped)
	if err != nil {
		t.Fatalf("UnwrapDEK: %v", err)
	}
	if !bytes.Equal(got, dek) {
		t.Errorf("round-trip mismatch: got %x, want %x", got, dek)
	}

	// Cross-alias rejection: a wrapped DEK under platform:tier6-test
	// must not unwrap under a different alias. Key Vault's
	// AdditionalAuthenticatedData is the cryptographic binding.
	prov.SetAlias("tenant:wrong", kmsazure.KeyRef{Name: name, Version: version})
	if _, err := prov.UnwrapDEK(ctx, "tenant:wrong", wrapped); err == nil {
		t.Error("UnwrapDEK under wrong alias should fail (AAD mismatch)")
	}
}

// spec: 12.6 (tier 6 — E2E on cloud: "cloud_csi | ArtifactStore
// against the provider's native object storage: GCS on GKE, S3 on
// EKS, Azure Blob Storage on AKS. The same interface tests pass
// against every backend")
// diagnosis: TestCloudCSIAzureBlob exercises
// pkg/blobstore/azureblob.Store end-to-end against the container the
// deploy/terraform/cloud/azure module emits as the
// artifact_container_url output. A successful Put + Get + Stat +
// SoftDelete round-trip confirms the storage-account RBAC role
// assignment and the blobstore/azureblob error mapping match the
// documented contract already exercised against S3 in TestCloudCSI,
// satisfying the "same interface tests pass against every backend"
// claim for Azure Blob Storage.
func TestCloudCSIAzureBlob(t *testing.T) {
	p := requireCloud(t)
	if p != "azure" {
		t.Logf("TestCloudCSIAzureBlob: Azure Blob test runs against azure; LENNY_CLOUD_PROVIDER=%q", p)
		return
	}
	containerURL := requireEnv(t, "LENNY_AZURE_ARTIFACT_CONTAINER_URL")
	if containerURL == "" {
		return
	}
	serviceURL, container, err := parseAzureContainerURL(containerURL)
	if err != nil {
		t.Fatalf("parseAzureContainerURL: %v", err)
	}
	cred, ok := azureCredential(t)
	if !ok {
		return
	}

	store, err := azureblob.New(azureblob.Config{
		ServiceURL: serviceURL,
		Credential: cred,
		Container:  container,
	})
	if err != nil {
		t.Fatalf("azureblob.New: %v", err)
	}

	u := blobstore.URI{
		TenantID:  "acme",
		SessionID: "tier6-session-" + time.Now().UTC().Format("20060102T150405"),
		PartID:    "part1",
	}
	payload := []byte("tier-6 e2e_cloud round-trip payload (Azure Blob)")

	if _, err := store.Put(u, "application/octet-stream", bytes.NewReader(payload)); err != nil {
		t.Fatalf("Put: %v", err)
	}
	// Best-effort cleanup so the container does not accumulate test
	// objects across runs. SoftDelete writes the tombstone tag;
	// HardPrune with zero retention drives the matching delete
	// against the object the test just put in.
	t.Cleanup(func() {
		_ = store.SoftDelete(u)
		store.HardPrune(time.Now(), 0)
	})

	info, body, err := store.Get(u)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer body.Close()
	if info.Size != int64(len(payload)) {
		t.Errorf("info.Size = %d, want %d", info.Size, len(payload))
	}
	got, err := io.ReadAll(body)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Errorf("body mismatch: got %q, want %q", got, payload)
	}

	stat, err := store.Stat(u)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if stat.Size != int64(len(payload)) {
		t.Errorf("stat.Size = %d, want %d", stat.Size, len(payload))
	}

	if err := store.SoftDelete(u); err != nil {
		t.Fatalf("SoftDelete: %v", err)
	}
	if _, _, err := store.Get(u); err != blobstore.ErrNotFound {
		t.Errorf("Get after SoftDelete: %v, want ErrNotFound", err)
	}
}
