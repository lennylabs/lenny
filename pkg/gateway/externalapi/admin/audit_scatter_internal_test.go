// SPDX-License-Identifier: MIT

package admin

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/audit"
)

// TestCrossCursorRoundTrip confirms the §25.9 cross-tenant pagination
// cursor encodes and decodes the (tenant, seq) marker, including a tenant
// id containing a colon (the encoder splits on the last colon).
func TestCrossCursorRoundTrip(t *testing.T) {
	for _, c := range []crossCursor{
		{tenant: "acme", seq: 7},
		{tenant: "tenant:with:colons", seq: 42},
		{tenant: "", seq: 1},
	} {
		enc := encodeCrossCursor(c)
		got, ok := decodeCrossCursor(nil, enc)
		if !ok {
			t.Fatalf("decode(%q) ok=false", enc)
		}
		if got != c {
			t.Errorf("round-trip = %+v, want %+v", got, c)
		}
	}
}

// TestAfterCross verifies the (tenant_id, seq) total-order comparison the
// cross-tenant page uses to resume after a cursor.
func TestAfterCross(t *testing.T) {
	cur := crossCursor{tenant: "acme", seq: 5}
	cases := []struct {
		tenant string
		seq    uint64
		want   bool
	}{
		{"acme", 5, false},  // equal — already returned
		{"acme", 6, true},   // same tenant, later seq
		{"acme", 4, false},  // same tenant, earlier seq
		{"globex", 1, true}, // later tenant, any seq
		{"abc", 99, false},  // earlier tenant, any seq
	}
	for _, tc := range cases {
		if got := afterCross(cur, tc.tenant, tc.seq); got != tc.want {
			t.Errorf("afterCross(%q,%d) = %v, want %v", tc.tenant, tc.seq, got, tc.want)
		}
	}
}

// TestCrossTenantIntegritiesVerifiesPerTenant confirms each tenant's
// §11.7 chain is verified independently: a tamper in one tenant's chain
// does not mark another tenant's rows broken.
func TestCrossTenantIntegritiesVerifiesPerTenant(t *testing.T) {
	cs := audit.NewChainSet()
	ts := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	a1 := cs.Append("acme", "e1", json.RawMessage(`{}`), ts)
	a2 := cs.Append("acme", "e2", json.RawMessage(`{}`), ts)
	g1 := cs.Append("globex", "e1", json.RawMessage(`{}`), ts)

	// Tamper acme's second row so its chain link breaks.
	a2.Hash = "deadbeef"
	rows := []audit.Row{a1, a2, g1}

	got := crossTenantIntegrities(rows)
	if v := got[crossTenantKey("globex", g1.Seq)]; v != audit.ChainVerified {
		t.Errorf("globex row integrity = %q, want verified (isolated from acme tamper)", v)
	}
	// acme's tamper is detected within acme's own chain.
	if got[crossTenantKey("acme", a2.Seq)] == audit.ChainVerified {
		t.Errorf("acme tampered row reported verified, want broken/unchecked")
	}
}

// TestScatterCacheKeyVariesByQuery confirms the §25.9 line 3709 cache key
// distinguishes queries by their parameters so a different page or filter
// is not served a stale entry.
func TestScatterCacheKeyVariesByQuery(t *testing.T) {
	base := auditQueryFilter{
		since:     time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		until:     time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC),
		eventType: "session.created",
	}
	k1 := scatterCacheKey(base, 100, "")
	if scatterCacheKey(base, 100, "") != k1 {
		t.Errorf("same query produced different keys")
	}
	if scatterCacheKey(base, 50, "") == k1 {
		t.Errorf("different limit produced same key")
	}
	other := base
	other.eventType = "session.completed"
	if scatterCacheKey(other, 100, "") == k1 {
		t.Errorf("different eventType produced same key")
	}
	if scatterCacheKey(base, 100, "cursorX") == k1 {
		t.Errorf("different cursor produced same key")
	}
}
