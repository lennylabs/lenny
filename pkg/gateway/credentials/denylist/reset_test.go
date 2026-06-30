// SPDX-License-Identifier: MIT

package denylist_test

import (
	"testing"

	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/denylist"
)

// spec: §4.9 lines 1668-1673 — the startup deny-list rebuild constructs
// the authoritative set via Reset.

// TestResetSeedsAndReplaces confirms Reset installs exactly the given
// keys: it both adds entries the list lacked and drops entries the new
// set omits.
func TestResetSeedsAndReplaces(t *testing.T) {
	d := denylist.New()
	d.Revoke(poolKey("p", "stale")) // present before rebuild, absent from the new set

	keys := []credential.CredentialKey{
		poolKey("claude-prod", "key-1"),
		poolKey("claude-prod", "key-2"),
		userKey("acme", "ref-9"),
	}
	d.Reset(keys)

	if d.Len() != 3 {
		t.Fatalf("Len after Reset = %d, want 3", d.Len())
	}
	for _, k := range keys {
		if !d.Revoked(k) {
			t.Errorf("key %+v not on the deny list after Reset", k)
		}
	}
	if d.Revoked(poolKey("p", "stale")) {
		t.Error("Reset did not drop the stale entry omitted from the new set")
	}
}

// TestResetToEmptyClearsList confirms Reset with no keys empties the
// list, matching a rebuild that finds no revoked credentials.
func TestResetToEmptyClearsList(t *testing.T) {
	d := denylist.New()
	d.Revoke(poolKey("p", "key-1"))
	d.Reset(nil)
	if d.Len() != 0 {
		t.Fatalf("Len after Reset(nil) = %d, want 0", d.Len())
	}
	if d.Revoked(poolKey("p", "key-1")) {
		t.Error("Reset(nil) left a stale entry")
	}
}
