// SPDX-License-Identifier: MIT

package drain_readiness_test

import (
	"testing"

	dr "github.com/lennylabs/lenny/pkg/admission/drain_readiness"
)

// spec: §12.5 — the lenny-drain-readiness webhook blocks a planned node
// drain while the artifact store is degraded.

func TestAdmitsWhenMinIOHealthy(t *testing.T) {
	d := dr.Decide(dr.Request{MinIO: dr.MinIOHealthy})
	if !d.Allowed {
		t.Fatalf("eviction rejected with a healthy MinIO probe: %s", d.Reason)
	}
	if d.Forced {
		t.Error("a healthy-MinIO admission was marked forced")
	}
	if d.Code != 200 {
		t.Errorf("code = %d, want 200", d.Code)
	}
}

func TestBlocksWhenMinIOUnhealthy(t *testing.T) {
	d := dr.Decide(dr.Request{MinIO: dr.MinIOUnhealthy})
	if d.Allowed {
		t.Fatal("eviction admitted with an unhealthy MinIO probe")
	}
	if d.Code != 403 {
		t.Errorf("code = %d, want 403", d.Code)
	}
	if d.Reason != dr.RejectionMessage {
		t.Errorf("reason = %q, want the §12.5 message", d.Reason)
	}
}

func TestBlocksWhenMinIOUnreachable(t *testing.T) {
	// §12.5: a 503 OR an unreachable endpoint blocks the drain.
	d := dr.Decide(dr.Request{MinIO: dr.MinIOUnreachable})
	if d.Allowed {
		t.Fatal("eviction admitted though the drain-readiness endpoint was unreachable")
	}
	if d.Reason != dr.RejectionMessage {
		t.Errorf("reason = %q, want the §12.5 message", d.Reason)
	}
}

func TestDrainForceOverrideAdmitsEvenWhenMinIOUnhealthy(t *testing.T) {
	d := dr.Decide(dr.Request{DrainForced: true, MinIO: dr.MinIOUnhealthy})
	if !d.Allowed {
		t.Fatal("the drain-force override did not admit the eviction")
	}
	if !d.Forced {
		t.Error("a drain-force admission was not marked forced — no node.drain.forced audit would fire")
	}
	if d.Code != 200 {
		t.Errorf("code = %d, want 200", d.Code)
	}
}

func TestDrainForceOverrideWithHealthyMinIOIsStillForced(t *testing.T) {
	// The override is evaluated ahead of the health check, so a forced
	// drain is always reported forced regardless of MinIO state.
	d := dr.Decide(dr.Request{DrainForced: true, MinIO: dr.MinIOHealthy})
	if !d.Allowed || !d.Forced {
		t.Errorf("decision = %+v, want an allowed, forced admission", d)
	}
}

func TestBlocksWhenMinIOStatusUnset(t *testing.T) {
	// A zero-value MinIO status is not "healthy", so the fail-closed
	// branch rejects the eviction.
	d := dr.Decide(dr.Request{})
	if d.Allowed {
		t.Error("eviction admitted with an unset MinIO status — fail-closed expected")
	}
}
