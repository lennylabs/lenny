// SPDX-License-Identifier: MIT

package backup

import (
	"testing"

	"github.com/lennylabs/lenny/pkg/observability/audit"
)

// TestAuditEventTypesAreCatalogued_spec_16_7 asserts every §25.11
// backup/restore audit event the Service emits is a recognized §16.7
// catalog entry, so an audit-sink validator does not discard the rows
// as unknown. spec: §16.7 / §25.11 line 4343.
func TestAuditEventTypesAreCatalogued_spec_16_7(t *testing.T) {
	for _, typ := range auditEventTypes() {
		if !audit.IsKnownEventType(audit.EventType(typ)) {
			t.Errorf("emitted audit event %q is not a known §16.7 catalog type", typ)
		}
	}
}

// TestAuditFieldsWithOperationID pins the F-OID-1 correlation helper: a
// non-empty caller operationId is merged onto the backup/restore audit
// event under the "operationId" key so the §25.17 watchdog resolves the
// operation, while an empty operationId leaves the fields untouched so a
// request without the X-Lenny-Operation-ID header records no spurious
// empty field. Both branches are asserted; the empty case would regress
// silently if the guard were dropped, recording operationId:"".
//
// spec: §25.1 line 121 (operationId on every request audit event), §25.2
// line 350 (operationId propagated to audit events). (F-OID-1)
func TestAuditFieldsWithOperationID(t *testing.T) {
	// Non-empty: the key is set to the caller operationId.
	withID := auditFieldsWithOperationID("op-123", map[string]any{"tenant": "acme"})
	if withID["operationId"] != "op-123" {
		t.Errorf("operationId = %v, want op-123", withID["operationId"])
	}
	if withID["tenant"] != "acme" {
		t.Errorf("existing field dropped: %v", withID)
	}

	// Empty: the fields are returned unchanged, with no operationId key.
	empty := auditFieldsWithOperationID("", map[string]any{"tenant": "acme"})
	if _, present := empty["operationId"]; present {
		t.Errorf("empty operationId recorded a spurious operationId field: %v", empty)
	}
	if empty["tenant"] != "acme" {
		t.Errorf("existing field dropped on the empty path: %v", empty)
	}
}
