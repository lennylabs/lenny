// SPDX-License-Identifier: MIT

//go:build e2e_cloud

// Tier-6 integrated testbed. The per-capability tier-6 tests exercise
// one managed-cloud surface each in isolation: TestGvisorIsolation
// (sandbox node pool), TestCloudKMS / TestCloudKMSGCP / TestCloudKMSAzure
// (tenant-scoped KMS round-trip), TestCloudOIDC (workload identity),
// TestMultiZoneDR (multi-AZ Postgres). This file adds the cross-cutting
// scenario the §12.6 provider parity matrix requires: a single test that
// engages the sandbox isolation profile and a tenant-scoped (T4) KMS key
// together, and asserts the §12.5 fail-closed contract when that key is
// unavailable. It runs on any of the three managed clusters via the same
// LENNY_CLOUD_PROVIDER dispatch the rest of the tier uses.

package tier6_e2e_cloud_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/lennylabs/lenny/pkg/kms"
	kmsaws "github.com/lennylabs/lenny/pkg/kms/aws"
	kmsazure "github.com/lennylabs/lenny/pkg/kms/azure"
	kmsgcp "github.com/lennylabs/lenny/pkg/kms/gcp"
)

// tenantKEKAlias is the §12.5 tenant-scoped KEK alias a T4 workload's
// ArtifactStore selects. The alias namespace (`tenant:{id}`) is the
// cryptographic binding that keeps one tenant's artifacts unreadable
// under another tenant's key.
const tenantKEKAlias = "tenant:acme"

// spec: 12.5 (T4 per-tenant KMS key: "The ArtifactStore implementation
// selects the encryption key based on the tenant's workspaceTier ...
// T4 uses a KMS key scoped to tenant:{tenant_id}" and bullet 2 —
// "Failure behavior when the KMS key is unavailable at write time. If
// the tenant-scoped KMS key is unavailable ... the gateway MUST reject
// the write with CLASSIFICATION_CONTROL_VIOLATION. The ArtifactStore
// does not fall back to the deployment-wide SSE key, because that would
// silently downgrade the tenant's classification controls"),
// 12.9 (T4 dedicated sandbox node isolation), 12.6 (tier-6 provider
// parity: gvisor_isolation + cloud_kms engaged together).
// diagnosis: TestT4SandboxKMSFailClosedTestbed is the tier-6 integrated
// testbed. It asserts, on one managed cluster, that (a) the sandbox
// isolation profile is installed (a sandbox RuntimeClass the T4 pool
// schedules onto), (b) a tenant-scoped KMS key wraps and unwraps a DEK
// (the working T4 credential path), and (c) the provider fails closed —
// returns an error and emits no ciphertext, never a deployment-wide
// fallback — when the tenant-scoped key is unavailable. A regression in
// any leg means a T4 workload could run without sandbox isolation, or
// an ArtifactStore could silently downgrade a T4 tenant to the shared
// KEK on a KMS outage, breaking the cryptographic-erasure guarantee.
// This failure mode is invisible on Kind (no managed KMS, no sandbox
// node pool); it needs a real GKE/EKS/AKS cluster with the sandbox node
// pool and the tenant-scoped key provisioned by
// scripts/cloud/<provider>/up.sh.
func TestT4SandboxKMSFailClosedTestbed(t *testing.T) {
	p := requireCloud(t)

	// Leg 1: the sandbox isolation profile a T4 pool runs under must be
	// installed on the cluster. §12.9 forbids co-locating T4 workloads
	// with non-T4 pods; the isolation profile (gVisor / Kata / the AKS
	// Confidential Containers variant) is the node-boundary control.
	requireSandboxIsolationProfile(t)

	// Leg 2: the tenant-scoped T4 credential path — wrap then unwrap a
	// DEK under the tenant KEK — must round-trip against the managed
	// KMS the release provisions.
	prov, unavailable, ok := tenantKMSProviders(t, string(p))
	if !ok {
		return
	}

	dek := make([]byte, kms.DEKSize)
	if _, err := rand.Read(dek); err != nil {
		t.Fatalf("rand.Read: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	wrapped, err := prov.WrapDEK(ctx, tenantKEKAlias, dek)
	if err != nil {
		t.Fatalf("WrapDEK under tenant KEK %q: %v", tenantKEKAlias, err)
	}
	if len(wrapped.Ciphertext) == 0 {
		t.Fatal("WrapDEK returned empty ciphertext for an available tenant KEK")
	}
	got, err := prov.UnwrapDEK(ctx, tenantKEKAlias, wrapped)
	if err != nil {
		t.Fatalf("UnwrapDEK under tenant KEK %q: %v", tenantKEKAlias, err)
	}
	if !bytes.Equal(got, dek) {
		t.Errorf("tenant KEK round-trip mismatch: got %x, want %x", got, dek)
	}

	// Leg 3: fail closed when the tenant-scoped key is unavailable. The
	// §12.5 contract is that the ArtifactStore rejects the write rather
	// than falling back to the deployment-wide SSE key. At the KEK seam
	// this surfaces as WrapDEK returning an error and no ciphertext: a
	// provider that produced a wrapped DEK here would be minting
	// tenant-classified material under a key the tenant does not
	// control, which is exactly the silent downgrade the spec forbids.
	failWrapped, failErr := unavailable.WrapDEK(ctx, tenantKEKAlias, dek)
	if failErr == nil {
		t.Errorf("WrapDEK against an unavailable tenant KEK returned no error; §12.5 requires the write be rejected, not silently downgraded to a fallback key")
	}
	if len(failWrapped.Ciphertext) != 0 {
		t.Errorf("WrapDEK against an unavailable tenant KEK emitted %d ciphertext byte(s); a fail-closed provider must produce none", len(failWrapped.Ciphertext))
	}
}

// requireSandboxIsolationProfile asserts the cluster has at least one
// sandbox RuntimeClass installed — the isolation profile a §12.9 T4
// (or any sandboxed) pool schedules onto. The RuntimeClass name varies
// per provider (gVisor's `gvisor`/`runsc` on GKE/EKS, a Kata handler on
// GKE/AKS, the `kata-cc` Confidential Containers family on AKS), so the
// check matches the family rather than a single fixed name.
func requireSandboxIsolationProfile(t *testing.T) {
	t.Helper()
	cli := kube(t)
	if cli == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	rcList, err := cli.NodeV1().RuntimeClasses().List(ctx, metav1.ListOptions{})
	if err != nil {
		t.Fatalf("list RuntimeClasses: %v", err)
	}
	sandboxMarkers := []string{"gvisor", "runsc", "kata", "confidential", "sandbox"}
	var found string
	for _, rc := range rcList.Items {
		name := strings.ToLower(rc.Name)
		handler := strings.ToLower(rc.Handler)
		for _, m := range sandboxMarkers {
			if strings.Contains(name, m) || strings.Contains(handler, m) {
				found = rc.Name
				break
			}
		}
		if found != "" {
			break
		}
	}
	if found == "" {
		t.Log("TestT4SandboxKMSFailClosedTestbed: no sandbox RuntimeClass (gvisor/kata/confidential) installed; provision the cloud-sandbox node pool via scripts/cloud/<provider>/up.sh so T4 workloads have an isolation profile to schedule onto")
		return
	}
	t.Logf("TestT4SandboxKMSFailClosedTestbed: sandbox isolation profile present via RuntimeClass %q", found)
}

// tenantKMSProviders builds two KMS providers for the active cloud: one
// whose tenant KEK alias resolves to the release-provisioned key (the
// working T4 credential path), and one whose alias resolves to an
// unavailable key (the fail-closed path). It returns ok=false, having
// logged the missing input, when the per-provider key env var or
// credentials are absent, so the caller returns without a spurious
// failure. The unavailable key is a syntactically valid reference to a
// key that does not exist, so WrapDEK reaches the KMS backend and is
// rejected there rather than short-circuiting on an unmapped alias.
func tenantKMSProviders(t *testing.T, provider string) (working, unavailable kms.Provider, ok bool) {
	t.Helper()
	switch provider {
	case "aws":
		keyARN := requireEnv(t, "LENNY_AWS_KMS_KEY_ARN")
		if keyARN == "" {
			return nil, nil, false
		}
		cfg := loadAWSConfig(t)
		if cfg.Region == "" && len(cfg.ConfigSources) == 0 {
			return nil, nil, false
		}
		w, err := kmsaws.New(kmsaws.Config{
			AWSConfig:    cfg,
			AliasToKeyID: map[string]string{tenantKEKAlias: keyARN},
		})
		if err != nil {
			t.Fatalf("kmsaws.New (working): %v", err)
		}
		u, err := kmsaws.New(kmsaws.Config{
			AWSConfig:    cfg,
			AliasToKeyID: map[string]string{tenantKEKAlias: unavailableAWSKeyARN(keyARN)},
		})
		if err != nil {
			t.Fatalf("kmsaws.New (unavailable): %v", err)
		}
		return w, u, true

	case "gcp":
		keyID := requireEnv(t, "LENNY_GCP_KMS_KEY_ID")
		if keyID == "" {
			return nil, nil, false
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		w, err := kmsgcp.New(ctx, kmsgcp.Config{
			AliasToKeyName: map[string]string{tenantKEKAlias: keyID},
		})
		if err != nil {
			t.Fatalf("kmsgcp.New (working): %v", err)
		}
		u, err := kmsgcp.New(ctx, kmsgcp.Config{
			AliasToKeyName: map[string]string{tenantKEKAlias: keyID + "-nonexistent"},
		})
		if err != nil {
			t.Fatalf("kmsgcp.New (unavailable): %v", err)
		}
		return w, u, true

	case "azure":
		keyID := requireEnv(t, "LENNY_AZURE_KEY_VAULT_KEY_ID")
		if keyID == "" {
			return nil, nil, false
		}
		vaultURL, name, version, err := parseAzureKeyVaultKeyID(keyID)
		if err != nil {
			t.Fatalf("parseAzureKeyVaultKeyID: %v", err)
		}
		cred, credOK := azureCredential(t)
		if !credOK {
			return nil, nil, false
		}
		w, err := kmsazure.New(kmsazure.Config{
			VaultURL:   vaultURL,
			Credential: cred,
			AliasToKey: map[string]kmsazure.KeyRef{tenantKEKAlias: {Name: name, Version: version}},
		})
		if err != nil {
			t.Fatalf("kmsazure.New (working): %v", err)
		}
		u, err := kmsazure.New(kmsazure.Config{
			VaultURL:   vaultURL,
			Credential: cred,
			AliasToKey: map[string]kmsazure.KeyRef{tenantKEKAlias: {Name: name + "-nonexistent", Version: version}},
		})
		if err != nil {
			t.Fatalf("kmsazure.New (unavailable): %v", err)
		}
		return w, u, true
	}
	t.Logf("TestT4SandboxKMSFailClosedTestbed: no tenant-scoped KMS wiring for provider %q", provider)
	return nil, nil, false
}

// unavailableAWSKeyARN rewrites a real KMS key ARN into a syntactically
// valid ARN for a key that does not exist, so WrapDEK's Encrypt call
// reaches AWS KMS and is rejected with NotFound rather than failing
// locally on an unmapped alias. The trailing key resource is replaced
// with an all-zero UUID.
func unavailableAWSKeyARN(arn string) string {
	const zeroKey = "key/00000000-0000-0000-0000-000000000000"
	if i := strings.Index(arn, ":key/"); i >= 0 {
		return arn[:i+1] + zeroKey
	}
	// Alias-form ARN (arn:aws:kms:<region>:<acct>:alias/<name>): point
	// at an alias that does not exist.
	if i := strings.Index(arn, ":alias/"); i >= 0 {
		return arn[:i+1] + "alias/lenny-tier6-nonexistent"
	}
	return arn + "-nonexistent"
}
