// SPDX-License-Identifier: MIT

package providerflags

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/lennylabs/lenny/pkg/kms"
)

// spec: 10.2 (Authentication)
// On a cloud provider a platform:jwt-* alias must name an immutable key
// version; only the immutable forms pass.
func TestSigningReferenceImmutabilityTable_spec_10_2(t *testing.T) {
	cases := []struct {
		provider, ref string
		ok            bool
	}{
		{ProviderAWS, "alias/lenny-jwt", false},
		{ProviderAWS, "arn:aws:kms:us-east-1:111122223333:alias/lenny-jwt", false},
		{ProviderAWS, "arn:aws:kms:us-east-1:111122223333:key/1234abcd-12ab-34cd-56ef-1234567890ab", true},
		{ProviderAWS, "1234abcd-12ab-34cd-56ef-1234567890ab", true},
		{ProviderAWS, "arn:aws:kms:us-east-1:111122223333:key/", false},
		{ProviderAWS, "", false},
		{ProviderGCP, "projects/p/locations/global/keyRings/r/cryptoKeys/jwt", false},
		{ProviderGCP, "projects/p/locations/global/keyRings/r/cryptoKeys/jwt/cryptoKeyVersions/3", true},
		{ProviderGCP, "projects/p/locations/global/keyRings/r/cryptoKeys/jwt/cryptoKeyVersions/", false},
		{ProviderAzure, "jwt@", false},
		{ProviderAzure, "jwt", false},
		{ProviderAzure, "jwt@v1", true},
		{ProviderAzure, "@v1", false},
	}
	for _, alias := range []string{kms.AliasJWTSigning, kms.AliasJWTVerify} {
		for _, c := range cases {
			err := validateSigningReferences(c.provider, map[string]string{alias: c.ref})
			if c.ok && err != nil {
				t.Errorf("%s %s=%q rejected: %v", c.provider, alias, c.ref, err)
			}
			if !c.ok && !errors.Is(err, ErrRepointableSigningReference) {
				t.Errorf("%s %s=%q err=%v, want ErrRepointableSigningReference", c.provider, alias, c.ref, err)
			}
		}
	}
	// Only the JWT aliases are held to the rule: a KEK alias may use an
	// AWS alias name.
	if err := validateSigningReferences(ProviderAWS, map[string]string{"tenant:acme": "alias/lenny/tenant-acme"}); err != nil {
		t.Errorf("KEK alias rejected: %v", err)
	}
}

// spec: 10.2 (Authentication)
// Resolve refuses a repointable signing reference before it builds any
// cloud client, so startup fails without reaching the provider.
func TestResolveRejectsRepointableSigningReferenceOnCloud_spec_10_2(t *testing.T) {
	cases := []Options{
		{Provider: ProviderAWS, AliasToKey: map[string]string{kms.AliasJWTSigning: "alias/lenny-jwt"}},
		{Provider: ProviderGCP, AliasToKey: map[string]string{kms.AliasJWTVerify: "projects/p/locations/l/keyRings/r/cryptoKeys/k"}},
		{Provider: ProviderAzure, AzureVaultURL: "https://x.vault.azure.net", AliasToKey: map[string]string{kms.AliasJWTSigning: "jwt@"}},
	}
	for _, opts := range cases {
		if _, err := Resolve(context.Background(), opts); !errors.Is(err, ErrRepointableSigningReference) {
			t.Errorf("Resolve(%s) err=%v, want ErrRepointableSigningReference", opts.Provider, err)
		}
	}
}

// spec: 10.2 (Authentication), 17.4 (Local Development Mode (lenny-dev))
// Under local the rule does not apply, and the platform:jwt-* entries
// reach the provider: two processes sharing one master key and one
// mapping derive one key, and a different mapping derives another. The
// local branch ignored the alias map before, so the mapped reference is
// the discriminating input.
func TestResolveLocalPassesJWTAliasReferences_spec_10_2(t *testing.T) {
	keyFile := filepath.Join(t.TempDir(), "master.key")
	resolve := func(m map[string]string) kms.Provider {
		t.Helper()
		p, err := Resolve(context.Background(), Options{Provider: ProviderLocal, MasterKeyFile: keyFile, AliasToKey: m})
		if err != nil {
			t.Fatalf("Resolve(local): %v", err)
		}
		return p
	}
	pubOf := func(p kms.Provider) []byte {
		t.Helper()
		pub, err := p.PublicKey(context.Background(), kms.AliasJWTSigning)
		if err != nil {
			t.Fatalf("PublicKey: %v", err)
		}
		b, _ := pub.Bytes()
		return b
	}
	repointable := map[string]string{kms.AliasJWTSigning: "alias/anything"}
	a, b := pubOf(resolve(repointable)), pubOf(resolve(repointable))
	if !bytes.Equal(a, b) {
		t.Fatal("two local providers sharing a master key and mapping derived different keys")
	}
	unmapped := pubOf(resolve(nil))
	if bytes.Equal(a, unmapped) {
		t.Fatal("the local provider ignored the platform:jwt-signing mapping")
	}
	// A non-JWT entry does not reach the signing derivation.
	if !bytes.Equal(pubOf(resolve(map[string]string{"tenant:acme": "x"})), unmapped) {
		t.Fatal("a non-JWT alias entry changed the signing key")
	}
}
