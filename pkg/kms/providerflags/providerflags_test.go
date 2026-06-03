// SPDX-License-Identifier: MIT

package providerflags

import (
	"context"
	"errors"
	"flag"
	"path/filepath"
	"testing"
)

// spec: F-4.3.11 / F-17.5.2 — Resolve picks the in-process local
// provider when the operator does not override.
func TestResolveDefaultsToLocal(t *testing.T) {
	p, err := Resolve(context.Background(), Options{})
	if err != nil {
		t.Fatalf("Resolve(local): %v", err)
	}
	if p == nil {
		t.Fatal("nil provider")
	}
}

// spec: F-4.3.11 / F-17.5.2 — production refuses to use the local
// KEK because it cannot decrypt persisted ciphertext after a restart.
func TestResolveRejectsLocalInProduction(t *testing.T) {
	_, err := Resolve(context.Background(), Options{Provider: ProviderLocal, Environment: "prod"})
	if !errors.Is(err, ErrLocalForbidden) {
		t.Fatalf("Resolve(local, prod) err=%v, want ErrLocalForbidden", err)
	}
}

// spec: §17.4 line 163 — a local provider with a master-key file is
// seeded from the persisted key, so two Resolves over the same file
// derive the same KEK (the property that lets state survive a restart).
func TestResolveLocalWithMasterKeyFilePersists_spec_17_4_163(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kms", "master.key")
	ctx := context.Background()
	const alias = "platform:token-service-signing"

	p1, err := Resolve(ctx, Options{Provider: ProviderLocal, MasterKeyFile: path})
	if err != nil {
		t.Fatalf("Resolve(local, file): %v", err)
	}
	dek := make([]byte, 32)
	wrapped, err := p1.WrapDEK(ctx, alias, dek)
	if err != nil {
		t.Fatalf("wrap: %v", err)
	}
	p2, err := Resolve(ctx, Options{Provider: ProviderLocal, MasterKeyFile: path})
	if err != nil {
		t.Fatalf("second Resolve: %v", err)
	}
	if _, err := p2.UnwrapDEK(ctx, alias, wrapped); err != nil {
		t.Fatalf("unwrap across restart: %v", err)
	}
}

// spec: §17.4 line 163 — the file-backed master key is still rejected in
// production; the persistence knob does not weaken the prod guard.
func TestResolveRejectsLocalWithMasterKeyFileInProduction(t *testing.T) {
	path := filepath.Join(t.TempDir(), "master.key")
	_, err := Resolve(context.Background(), Options{Provider: ProviderLocal, Environment: "prod", MasterKeyFile: path})
	if !errors.Is(err, ErrLocalForbidden) {
		t.Fatalf("Resolve(local+file, prod) err=%v, want ErrLocalForbidden", err)
	}
}

// spec: §17.5 — unknown providers fail loudly so a typo doesn't
// silently fall back to local.
func TestResolveRejectsUnknownProvider(t *testing.T) {
	_, err := Resolve(context.Background(), Options{Provider: "wat"})
	if err == nil {
		t.Fatal("Resolve(unknown) returned nil error")
	}
}

// spec: F-4.3.11 — Bind registers the --kms-* flag surface so a Helm
// value change reaches the binary.
func TestBindRegistersFlags(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	opts, finalize := Bind(fs, func(string) string { return "" }, Options{})
	if err := fs.Parse([]string{
		"--kms-provider=aws",
		"--kms-aws-region=us-east-1",
		"--kms-master-key-file=/var/lib/lenny/master.key",
		"--kms-alias=platform:token-service-signing=alias/lenny/token-service",
	}); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if err := finalize(); err != nil {
		t.Fatalf("finalize: %v", err)
	}
	if opts.Provider != "aws" {
		t.Errorf("Provider=%q, want aws", opts.Provider)
	}
	if opts.MasterKeyFile != "/var/lib/lenny/master.key" {
		t.Errorf("MasterKeyFile=%q, want /var/lib/lenny/master.key", opts.MasterKeyFile)
	}
	if opts.AWSRegion != "us-east-1" {
		t.Errorf("AWSRegion=%q, want us-east-1", opts.AWSRegion)
	}
	if got := opts.AliasToKey["platform:token-service-signing"]; got != "alias/lenny/token-service" {
		t.Errorf("AliasToKey=%v, want one entry", opts.AliasToKey)
	}
}

// spec: F-4.3.11 — environment variable defaults to the LENNY_KMS_*
// env vars so the chart can drive the binary via env.
func TestBindEnvDefaults(t *testing.T) {
	envValues := map[string]string{
		"LENNY_KMS_PROVIDER": "gcp",
		"LENNY_KMS_ALIAS":    "platform:foo=projects/p/locations/global/keyRings/r/cryptoKeys/k",
	}
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	opts, finalize := Bind(fs, func(k string) string { return envValues[k] }, Options{})
	if err := fs.Parse(nil); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if err := finalize(); err != nil {
		t.Fatalf("finalize: %v", err)
	}
	if opts.Provider != "gcp" {
		t.Errorf("Provider=%q, want gcp", opts.Provider)
	}
	if _, ok := opts.AliasToKey["platform:foo"]; !ok {
		t.Errorf("alias missing; map=%v", opts.AliasToKey)
	}
}

// spec: F-4.3.11 — malformed alias entries fail the finalizer so the
// binary refuses to start with a misconfiguration.
func TestParseAliasesRejectsMalformed(t *testing.T) {
	_, err := ParseAliases("missing-equals")
	if err == nil {
		t.Errorf("ParseAliases accepted malformed entry")
	}
	_, err = ParseAliases("=valuewithoutalias")
	if err == nil {
		t.Errorf("ParseAliases accepted empty-alias entry")
	}
	m, err := ParseAliases("a=1, b=2 , c=alias/x")
	if err != nil {
		t.Fatalf("ParseAliases: %v", err)
	}
	if m["a"] != "1" || m["b"] != "2" || m["c"] != "alias/x" {
		t.Errorf("map=%v, want a=1,b=2,c=alias/x", m)
	}
}

// spec: F-4.3.11 — empty alias map is rejected on AWS/GCP/Azure
// (cloud providers need at least one mapping before Resolve can build
// the SDK client).
func TestResolveRequiresAliasMapForCloudProviders(t *testing.T) {
	for _, prov := range []string{ProviderAWS, ProviderGCP} {
		_, err := Resolve(context.Background(), Options{Provider: prov, AliasToKey: nil})
		if err == nil {
			t.Errorf("Resolve(%s) accepted empty alias map", prov)
		}
	}
	_, err := Resolve(context.Background(), Options{Provider: ProviderAzure, AzureVaultURL: "https://x.vault.azure.net"})
	if err == nil {
		t.Errorf("Resolve(azure) accepted empty alias map")
	}
}
