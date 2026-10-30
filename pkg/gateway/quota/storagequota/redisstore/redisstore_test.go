// SPDX-License-Identifier: MIT

package redisstore_test

import (
	"context"
	"errors"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/quota/storagequota"
	"github.com/lennylabs/lenny/pkg/gateway/quota/storagequota/redisstore"
	"github.com/lennylabs/lenny/pkg/redisconn/clustertest"
)

// spec: §12.4 Redis HA and Failure Modes (`sq:{tenant_id}`: single-key
// GET/INCRBY/SET/EVAL; Redis Cluster at Tier 3); §11.2. The reserve and
// adjust scripts name one key, so they run on a cluster under the
// cross-slot hook; a reservation over the limit is refused and leaves the
// counter unchanged.
func TestStorageQuotaScriptsRunOnClusterUnderSlotGuard_spec_12_4(t *testing.T) {
	c := clustertest.New(t, 3)
	s := redisstore.New(c.Client)
	ctx := context.Background()

	if _, err := s.Reserve(ctx, "acme", 60, 100); err != nil {
		t.Fatalf("Reserve within limit: %v", err)
	}
	if prior, err := s.Reserve(ctx, "acme", 60, 100); !errors.Is(err, storagequota.ErrQuotaExceeded) || prior != 60 {
		t.Fatalf("Reserve over limit = %d, %v; want 60, ErrQuotaExceeded", prior, err)
	}
	if err := s.Adjust(ctx, "acme", -10); err != nil {
		t.Fatalf("Adjust: %v", err)
	}
	if used, err := s.Used(ctx, "acme"); err != nil || used != 50 {
		t.Fatalf("Used = %d, %v; want 50, nil", used, err)
	}
	if err := s.Set(ctx, "globex", -5); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if used, err := s.Used(ctx, "globex"); err != nil || used != 0 {
		t.Fatalf("Used(globex) = %d, %v; want 0, nil", used, err)
	}
}
