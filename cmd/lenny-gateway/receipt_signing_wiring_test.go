// SPDX-License-Identifier: MIT

package main

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/kms"
	"github.com/lennylabs/lenny/pkg/kms/providerflags"
	"github.com/lennylabs/lenny/pkg/uploadtoken"
)

// TestBuildReceiptSigningTrustsTheRetiredKeysTheSettingLists drives the
// gateway startup step that loads the redaction receipt keys: with
// --audit-retired-receipt-key-ids listing two retired key identifiers, the
// trusted set holds the key platform:audit-signing names and both retired
// keys, each read from the KMS by its identifier, and a gateway with the
// setting empty trusts the signing key alone.
//
// spec: §12.8 (Compliance Interfaces)
func TestBuildReceiptSigningTrustsTheRetiredKeysTheSettingLists(t *testing.T) {
	master := filepath.Join(t.TempDir(), "master.key")
	build := func(retired string) []string {
		t.Helper()
		f := tokenSigningFlags(master, "", "", "")
		f.kmsOpts.AliasToKey[kms.AliasAuditSigning] = "audit-key-v3"
		f.auditRetiredReceiptKeyIDs = &retired
		provider, err := providerflags.Resolve(t.Context(), *f.kmsOpts)
		if err != nil {
			t.Fatal(err)
		}
		w := &gatewayWiring{f: f}
		w.kmsProvider = provider
		w.buildReceiptSigning()
		if w.receiptSigner == nil || w.receiptSigner.KeyID() != "audit-key-v3" {
			t.Fatalf("receipt signer = %v, want one signing under audit-key-v3", w.receiptSigner)
		}
		var ids []string
		for _, k := range w.receiptKeys.Keys() {
			ids = append(ids, k.KeyID)
		}
		return ids
	}
	if got := build(" audit-key-v1, audit-key-v2 "); len(got) != 3 || got[0] != "audit-key-v1" || got[1] != "audit-key-v2" || got[2] != "audit-key-v3" {
		t.Errorf("trusted keys with two retired keys listed = %v, want audit-key-v1, audit-key-v2, and audit-key-v3", got)
	}
	if got := build(""); len(got) != 1 || got[0] != "audit-key-v3" {
		t.Errorf("trusted keys with no retired key listed = %v, want audit-key-v3 alone", got)
	}
}

// TestTheUploadTokenKeyRingSeesTheLifetimeTokensCarry checks the value the
// gateway passes to the upload-token ring for a replaced key's
// verification time: the configured --max-created-state-timeout-seconds,
// and the issuer's default lifetime when the setting is not positive, so a
// non-positive setting cannot shorten the verification time below the
// lifetime tokens carry.
//
// spec: §7.1 (Normal Flow)
func TestTheUploadTokenKeyRingSeesTheLifetimeTokensCarry(t *testing.T) {
	for seconds, want := range map[int]time.Duration{
		900: 15 * time.Minute,
		300: 5 * time.Minute,
		0:   uploadtoken.DefaultTTL,
		-1:  uploadtoken.DefaultTTL,
	} {
		if got := uploadTokenLifetime(seconds); got != want {
			t.Errorf("uploadTokenLifetime(%d) = %s, want %s", seconds, got, want)
		}
	}
}
