// SPDX-License-Identifier: MIT

package providerflags

import (
	"context"
	"errors"
	"flag"
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
