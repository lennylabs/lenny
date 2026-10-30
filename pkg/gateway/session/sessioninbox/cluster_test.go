// SPDX-License-Identifier: MIT

package sessioninbox

import (
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/storage/rediskeys"
	"github.com/lennylabs/lenny/pkg/redisconn/slotguard"
)

// spec: §12.4 Redis HA and Failure Modes (the `t:{tenant_id}:` prefix
// hashes a tenant's keys to one slot); §7.2 (durable inbox and DLQ). The
// inbox and DLQ keys lead with the hash-tagged tenant prefix, so every key
// of a tenant, across sessions, maps to the tenant's slot.
func TestInboxAndDLQKeysShareTenantSlot_spec_12_4(t *testing.T) {
	want := slotguard.Slot(rediskeys.TenantPrefix("acme"))
	for _, k := range []string{InboxKey("acme", "s1"), DLQKey("acme", "s1"), InboxKey("acme", "s2")} {
		if !strings.HasPrefix(k, rediskeys.TenantPrefix("acme")) {
			t.Errorf("key %q lacks the hash-tagged tenant prefix", k)
		}
		if got := slotguard.Slot(k); got != want {
			t.Errorf("key %q maps to slot %d, want the tenant slot %d", k, got, want)
		}
	}
	if tenant, ok := rediskeys.TenantOf(DLQKey("globex", "s")); !ok || tenant != "globex" {
		t.Errorf("TenantOf(dlqKey) = %q, %v; want globex", tenant, ok)
	}
}
