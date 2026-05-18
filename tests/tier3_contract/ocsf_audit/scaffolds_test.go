// SPDX-License-Identifier: MIT

//go:build contract

// Tier-3 contract scaffolds for the §11.7 / §12.3.5 OCSF audit-event
// schema.
//
// TestOCSFTranslationCoversEveryEventType, TestOCSFRetranslationRetry,
// and TestOCSFSchemaVersionPin are implemented in ocsf_audit_test.go,
// which exercises the OCSF translator in pkg/audit/ocsf: it generates
// one of each catalog event type and asserts the §11.7 OCSF structural
// contract, walks the retranslation retry / dead-letter state machine,
// and pins the OCSF v1.1.0 wire version.

package ocsf_audit_test
