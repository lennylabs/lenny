// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/lennylabs/lenny/pkg/kms"
	"github.com/lennylabs/lenny/pkg/kms/providerflags"
	kmsstub "github.com/lennylabs/lenny/tests/testinfra/stubs/kms"
)

// stubResolver returns a resolver that hands back provider and counts
// its calls.
func stubResolver(provider kms.Provider, calls *int) kmsResolver {
	return func(context.Context, providerflags.Options) (kms.Provider, error) {
		*calls++
		return provider, nil
	}
}

// TestLoadOpsKeySetRefusesLocalInProduction pins that --production
// refuses a provider that resolves to local, including an unset
// --kms-provider, before anything is resolved, whatever --environment says.
//
// spec: 17.4 (Dev mode guard rails), 25.4 (The lenny-ops Service: Startup and Readiness)
func TestLoadOpsKeySetRefusesLocalInProduction(t *testing.T) {
	for _, provider := range []string{"", "local", " LOCAL "} {
		t.Run("provider="+provider, func(t *testing.T) {
			calls := 0
			opts := providerflags.Options{Provider: provider, Environment: "staging"}
			_, err := loadOpsKeySet(context.Background(), opts, true, stubResolver(nil, &calls))
			if !errors.Is(err, errLocalProviderInProduction) {
				t.Fatalf("err = %v, want errLocalProviderInProduction", err)
			}
			if calls != 0 {
				t.Errorf("resolver called %d times, want 0", calls)
			}
		})
	}
}

// TestLoadOpsKeySetFailureIsFatalInEveryMode pins that a key set that
// cannot load is an error with and without --production.
//
// spec: 10.2 (Authentication) "Verifier key set and rotation", 25.4 (The lenny-ops Service: Startup and Readiness)
func TestLoadOpsKeySetFailureIsFatalInEveryMode(t *testing.T) {
	for _, production := range []bool{false, true} {
		stub := kmsstub.New(t)
		stub.SetPublicKeyError(kms.ErrUnknownKEK)
		calls := 0
		opts := providerflags.Options{Provider: providerflags.ProviderAWS}
		_, err := loadOpsKeySet(context.Background(), opts, production, stubResolver(stub.AsProvider(), &calls))
		if !errors.Is(err, kms.ErrUnknownKEK) {
			t.Errorf("production=%v: err = %v, want the PublicKey error", production, err)
		}
	}
}

// TestLoadOpsKeySetResolveFailureIsFatal pins that a provider resolution
// failure is returned.
//
// spec: 10.2 (Authentication) "Verifier key set and rotation"
func TestLoadOpsKeySetResolveFailureIsFatal(t *testing.T) {
	want := errors.New("no credentials")
	resolve := func(context.Context, providerflags.Options) (kms.Provider, error) { return nil, want }
	if _, err := loadOpsKeySet(context.Background(), providerflags.Options{Provider: "aws"}, true, resolve); !errors.Is(err, want) {
		t.Fatalf("err = %v, want the resolve error", err)
	}
}

// TestLoadOpsKeySetSharesTheGatewayKeyUnderLocal pins that a development
// lenny-ops on the local provider derives its key set from the shared
// master key file, so it verifies a token a gateway with the same file
// signed.
//
// spec: 10.2 (Authentication) "Development", 17.4
func TestLoadOpsKeySetSharesTheGatewayKeyUnderLocal(t *testing.T) {
	opts := providerflags.Options{
		Provider:      providerflags.ProviderLocal,
		MasterKeyFile: filepath.Join(t.TempDir(), "master.key"),
		AliasToKey:    map[string]string{kms.AliasJWTSigning: "local-key-1"},
	}
	opsKeySet, err := loadOpsKeySet(context.Background(), opts, false, providerflags.Resolve)
	if err != nil {
		t.Fatalf("loadOpsKeySet: %v", err)
	}
	gatewayKeySet, err := loadOpsKeySet(context.Background(), opts, false, providerflags.Resolve)
	if err != nil {
		t.Fatalf("second load: %v", err)
	}
	if opsKeySet.Signing().KeyID != gatewayKeySet.Signing().KeyID {
		t.Fatalf("kid %s != %s: processes sharing a master key file must share the key",
			opsKeySet.Signing().KeyID, gatewayKeySet.Signing().KeyID)
	}
}

// TestBuildKeySetRecordsTheStartupKeySet pins that the startup step
// loads the §10.2 key set from the parsed KMS flags and records it on the
// wiring, so the authentication gate built after it verifies against the
// same key set a gateway sharing the master key file signs with.
//
// spec: 10.2 (Authentication) "Verifier key set and rotation", 25.4 (The lenny-ops Service: Startup and Readiness)
func TestBuildKeySetRecordsTheStartupKeySet(t *testing.T) {
	opts := providerflags.Options{
		Provider:      providerflags.ProviderLocal,
		MasterKeyFile: filepath.Join(t.TempDir(), "master.key"),
		AliasToKey:    map[string]string{kms.AliasJWTSigning: "local-key-1"},
	}
	production := false
	w := &opsWiring{
		f:               &opsFlags{production: &production, bearerTrustHMACKeyFile: strPtr(""), kmsOpts: &opts},
		opsWiringFields: opsWiringFields{ctx: context.Background()},
	}
	w.buildKeySet()
	if w.jwtKeySet == nil {
		t.Fatal("buildKeySet recorded no key set")
	}
	want, err := loadOpsKeySet(context.Background(), opts, false, providerflags.Resolve)
	if err != nil {
		t.Fatalf("loadOpsKeySet: %v", err)
	}
	if got := w.jwtKeySet.Signing().KeyID; got != want.Signing().KeyID {
		t.Errorf("recorded signing kid %s, want %s from the shared master key file", got, want.Signing().KeyID)
	}
}
