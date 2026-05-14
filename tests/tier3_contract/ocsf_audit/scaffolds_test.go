// SPDX-License-Identifier: MIT

//go:build contract

// Tier-3 contract scaffolds for §12.3.5 OCSF audit-event schema.
// Every audit event Lenny emits has an OCSF translation. The suite
// generates one of each event type and asserts the translated event
// passes OCSF schema validation. The OCSF retranslation API is
// exercised: a failed translation is retried and the audit row
// transitions to success or dead-letter.

package ocsf_audit_test

import "testing"

// TestOCSFTranslationCoversEveryEventType — generate one of each
// audit event type and assert the OCSF translation passes schema
// validation.
func TestOCSFTranslationCoversEveryEventType(t *testing.T) {
	t.Skip("not implemented: §12.3.5 OCSF translation — requires the OCSF translator + the OCSF JSON schema fixture under tests/testdata/ocsf/ + a generator that exercises every event type from pkg/audit")
}

// TestOCSFRetranslationRetry — a failed translation is retried by the
// retranscribe worker and the audit row transitions to success or
// dead-letter.
func TestOCSFRetranslationRetry(t *testing.T) {
	t.Skip("not implemented: §12.3.5 OCSF retranslation — requires the retranscribe worker + the failure-injection harness + the dead-letter transition contract")
}

// TestOCSFSchemaVersionPin — the OCSF schema version Lenny emits
// against is pinned and the test fails when the schema bundle moves
// without an explicit update.
func TestOCSFSchemaVersionPin(t *testing.T) {
	t.Skip("not implemented: §12.3.5 OCSF schema version pin — requires the OCSF schema fixture + a version-pin assertion against pkg/audit's declared OCSF version")
}
