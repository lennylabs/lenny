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
