// SPDX-License-Identifier: MIT

//go:build e2e_cloud

// Tier-6 live tests of the §10.2 ES256 signer and key set against a real
// asymmetric P-256 key on each cloud provider. Each test names the key
// through an environment variable that the deploy/terraform/cloud
// modules do not yet emit, so without it the test skips as blocked on
// T-17.5.2 (cloud KMS pluggability has no provisioned live coverage).

package tier6_e2e_cloud_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/pkg/kms"
	"github.com/lennylabs/lenny/pkg/kms/providerflags"
)

// requireSigningKey returns the named environment variable or skips the
// test as blocked on T-17.5.2.
func requireSigningKey(t *testing.T, name string) string {
	t.Helper()
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		t.Skipf("blocked: T-17.5.2: %s names no provisioned P-256 signing key version; create one and export its immutable reference", name)
	}
	return v
}

// assertSignVerifies resolves the provider through the binaries' flag
// seam, loads the key set the way every process does at startup, signs
// tokens with the real P-256 key through the remote ES256 signer built
// from the key set's signing entry, and verifies each through the key
// set.
func assertSignVerifies(t *testing.T, opts providerflags.Options) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	p, err := providerflags.Resolve(ctx, opts)
	if err != nil {
		t.Fatalf("Resolve(%s): %v", opts.Provider, err)
	}
	ks, err := jwt.LoadKeySet(ctx, p, opts.AliasToKey)
	if err != nil {
		t.Fatalf("LoadKeySet: %v", err)
	}
	signer, err := jwt.NewRemoteSigner(p, ks.Signing())
	if err != nil {
		t.Fatalf("NewRemoteSigner: %v", err)
	}
	for _, subject := range []string{"alice@acme.com", "bob@acme.com", "carol@acme.com"} {
		tok, err := signer.Sign(ctx, jwt.Claims{
			Issuer:   "https://lenny.tier6.test/gateway",
			Subject:  subject,
			TenantID: "acme",
			Expiry:   time.Now().Add(5 * time.Minute).Unix(),
			IssuedAt: time.Now().Unix(),
			Typ:      auth.TokenUserBearer,
		})
		if err != nil {
			t.Fatalf("Sign: %v", err)
		}
		claims, err := ks.Verify(tok)
		if err != nil {
			t.Fatalf("the token the provider signed for %q does not verify through the key set: %v", subject, err)
		}
		if claims.Subject != subject {
			t.Fatalf("verified subject = %q, want %q", claims.Subject, subject)
		}
	}
}

// spec: 10.2 (Authentication)
// diagnosis: AWS KMS did not sign with the ECC_NIST_P256 key the
// platform:jwt-signing alias names, or its DER signature did not convert
// to a verifiable r||s signature. Check the key spec, the Sign and
// GetPublicKey IAM grants, and pkg/kms/aws.
func TestCloudKMSJWTSignAWS(t *testing.T) {
	ref := requireSigningKey(t, "LENNY_AWS_JWT_SIGNING_KEY_ARN")
	assertSignVerifies(t, providerflags.Options{
		Provider:   providerflags.ProviderAWS,
		AWSRegion:  os.Getenv("LENNY_AWS_REGION"),
		AliasToKey: map[string]string{kms.AliasJWTSigning: ref},
	})
}

// spec: 10.2 (Authentication)
// diagnosis: Cloud KMS did not sign with the EC_SIGN_P256_SHA256
// CryptoKeyVersion the platform:jwt-signing alias names, or its DER
// signature did not convert to a verifiable r||s signature. Check the
// version's algorithm, the signer and public-key-viewer roles, and
// pkg/kms/gcp.
func TestCloudKMSJWTSignGCP(t *testing.T) {
	ref := requireSigningKey(t, "LENNY_GCP_JWT_SIGNING_KEY_VERSION")
	assertSignVerifies(t, providerflags.Options{
		Provider:   providerflags.ProviderGCP,
		AliasToKey: map[string]string{kms.AliasJWTSigning: ref},
	})
}

// spec: 10.2 (Authentication)
// diagnosis: Azure Key Vault did not sign with the versioned EC P-256 key
// the platform:jwt-signing alias names, or its r||s signature did not
// verify. Check the key type and curve, the sign and get key
// permissions, and pkg/kms/azure.
func TestCloudKMSJWTSignAzure(t *testing.T) {
	keyID := requireSigningKey(t, "LENNY_AZURE_JWT_SIGNING_KEY_ID")
	vaultURL, name, version, err := parseAzureKeyVaultKeyID(keyID)
	if err != nil {
		t.Fatalf("parseAzureKeyVaultKeyID: %v", err)
	}
	assertSignVerifies(t, providerflags.Options{
		Provider:      providerflags.ProviderAzure,
		AzureVaultURL: vaultURL,
		AliasToKey:    map[string]string{kms.AliasJWTSigning: name + "@" + version},
	})
}
