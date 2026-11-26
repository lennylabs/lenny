// SPDX-License-Identifier: MIT

// Package receiptkeys gives a test the redaction receipt signer and the
// trusted receipt key set a gateway loads at startup, over a local KMS
// provider. Two calls with the same master key file behave as two gateway
// replicas of one install: each signs receipts the other verifies.
package receiptkeys

import (
	"context"
	"testing"

	"github.com/lennylabs/lenny/pkg/audit"
	"github.com/lennylabs/lenny/pkg/kms"
	"github.com/lennylabs/lenny/pkg/storage/deadletterredaction"
)

// Load returns the receipt key set and the receipt signer of a process
// whose local KMS runs over the master key at keyFile, with the receipt
// alias mapping refs. A nil refs leaves the aliases unmapped, as a process
// started without --kms-alias does. An empty keyFile uses a random master
// key private to the call. retired lists the key identifiers of retired
// receipt keys the key set also trusts, as --audit-retired-receipt-key-ids
// does.
func Load(t testing.TB, keyFile string, refs map[string]string, retired ...string) (*audit.ReceiptKeySet, *deadletterredaction.KMSReceiptSigner) {
	t.Helper()
	var (
		local *kms.Local
		err   error
	)
	if keyFile == "" {
		local, err = kms.NewLocalRandom()
	} else {
		local, err = kms.NewLocalFromKeyFile(keyFile)
	}
	if err != nil {
		t.Fatalf("receiptkeys: local kms: %v", err)
	}
	for alias, ref := range refs {
		local.SetSigningReference(alias, ref)
	}
	keys, signer, err := deadletterredaction.LoadReceiptSigning(context.Background(), local, refs, retired)
	if err != nil {
		t.Fatalf("receiptkeys: load receipt signing: %v", err)
	}
	return keys, signer
}
