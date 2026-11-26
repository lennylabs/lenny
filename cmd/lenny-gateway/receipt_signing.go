// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"log"

	"github.com/lennylabs/lenny/pkg/storage/deadletterredaction"
)

// buildReceiptSigning loads the redaction receipt signer and the receipt
// keys a chain verifier trusts from the KMS provider that
// buildTokenSigningStores resolved, and records both on the accumulator.
// The signer signs inside the KMS under platform:audit-signing; the key set
// holds the public keys of platform:audit-signing and the optional
// platform:audit-verify. A process that cannot load them does not start,
// as for the JWT key set. The audit store's chain verification, the
// periodic integrity check, the audit-events chain walk, and the receipt
// key route read the key set; the erasure job's dead-letter redaction
// signs with the signer.
//
// spec: §12.8 (Compliance Interfaces)
func (w *gatewayWiring) buildReceiptSigning() {
	keys, signer, err := deadletterredaction.LoadReceiptSigning(context.Background(), w.kmsProvider, w.f.kmsOpts.AliasToKey)
	if err != nil {
		log.Fatalf("lenny-gateway: §12.8 redaction receipt signing keys: %v", err)
	}
	log.Printf("lenny-gateway: §12.8 redaction receipt signing key %s loaded (%d trusted key(s))",
		signer.KeyID(), len(keys.Keys()))
	w.receiptKeys = keys
	w.receiptSigner = signer
}
