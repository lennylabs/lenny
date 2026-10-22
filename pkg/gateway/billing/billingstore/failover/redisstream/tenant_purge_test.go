// SPDX-License-Identifier: MIT

// Coverage for the tenant-deletion Phase 4 erasure of the Redis-stream
// half of the billing write-ahead buffer: PurgeTenant destroys the
// tenant's consumer group and deletes its stream, leaves other tenants'
// streams, is idempotent, and keeps the tenant in the replica's known set
// so a later Flush on the same Tier does not recreate the stream.
package redisstream

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/gateway/billing/billingstore"
	"github.com/lennylabs/lenny/pkg/redisconn/clustertest"
	"github.com/lennylabs/lenny/pkg/redisconn/slotguard"
)

// recordingInserter is an Inserter that records every replayed event.
type recordingInserter struct {
	mu     sync.Mutex
	events []billingstore.Event
}

func (r *recordingInserter) InsertFromStream(_ context.Context, e billingstore.Event, _ string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, e)
	return nil
}

func (r *recordingInserter) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.events)
}

func publishN(t *testing.T, tier *Tier, tenant string, n int) {
	t.Helper()
	for i := 1; i <= n; i++ {
		e := billingstore.Event{TenantID: tenant, UserID: "alice", SequenceNumber: uint64(i), EventType: billingstore.EventSessionCreated}
		if err := tier.Publish(context.Background(), e); err != nil {
			t.Fatalf("Publish(%s): %v", tenant, err)
		}
	}
}

func keyExists(t *testing.T, client redis.UniversalClient, key string) bool {
	t.Helper()
	n, err := client.Exists(context.Background(), key).Result()
	if err != nil {
		t.Fatalf("Exists(%s): %v", key, err)
	}
	return n == 1
}

// spec: 12.8 (Compliance Interfaces: Tenant deletion lifecycle, Phase 4).
// The purge removes acme's stream and group, leaves globex's, and a
// repeated purge and a purge of a tenant with no stream succeed with 0.
func TestPurgeTenant_RemovesOnlyTenantStream_spec_12_8(t *testing.T) {
	ctx := context.Background()
	tier, client := newPurgeTier(t)
	publishN(t, tier, "acme", 3)
	publishN(t, tier, "globex", 2)

	n, err := tier.PurgeTenant(ctx, "acme")
	if err != nil || n != 3 {
		t.Fatalf("PurgeTenant = (%d, %v), want (3, nil)", n, err)
	}
	if keyExists(t, client, streamKey("acme")) {
		t.Fatal("acme stream key survived the purge")
	}
	if l, _ := client.XLen(ctx, streamKey("globex")).Result(); l != 2 {
		t.Fatalf("globex stream len = %d, want 2", l)
	}
	groups, err := client.XInfoGroups(ctx, streamKey("globex")).Result()
	if err != nil || len(groups) != 1 {
		t.Fatalf("globex groups = (%v, %v), want the billing-flusher group", groups, err)
	}
	if n, err := tier.PurgeTenant(ctx, "acme"); err != nil || n != 0 {
		t.Fatalf("repeated PurgeTenant = (%d, %v), want (0, nil)", n, err)
	}
	if n, err := tier.PurgeTenant(ctx, "initech"); err != nil || n != 0 {
		t.Fatalf("PurgeTenant of a tenant with no stream = (%d, %v), want (0, nil)", n, err)
	}
}

// spec: 12.8 (Compliance Interfaces: Tenant deletion lifecycle, Phase 4).
// A stream that exists without the consumer group (a publish from a
// replica whose ensureGroup already ran, after another replica's purge)
// is purged successfully.
func TestPurgeTenant_StreamWithoutGroup_spec_12_8(t *testing.T) {
	ctx := context.Background()
	tier, client := newPurgeTier(t)
	if err := client.XAdd(ctx, &redis.XAddArgs{Stream: streamKey("acme"), Values: map[string]any{"event": "{}"}}).Err(); err != nil {
		t.Fatalf("XAdd: %v", err)
	}
	if n, err := tier.PurgeTenant(ctx, "acme"); err != nil || n != 1 {
		t.Fatalf("PurgeTenant = (%d, %v), want (1, nil)", n, err)
	}
	if keyExists(t, client, streamKey("acme")) {
		t.Fatal("acme stream key survived the purge")
	}
}

// spec: 12.8 (Compliance Interfaces: Tenant deletion lifecycle, Phase 4).
// After the purge, a Flush on the same Tier leaves no stream key whatever
// it returns: the purge keeps the tenant in the known set, so Flush does
// not recreate the stream and its group through XGROUP CREATE MKSTREAM.
func TestPurgeTenant_FlushAfterPurgeLeavesNoStream_spec_12_8(t *testing.T) {
	ctx := context.Background()
	mr := miniredis.RunT(t)
	client := slotguard.Install(redis.NewClient(&redis.Options{Addr: mr.Addr()}))
	t.Cleanup(func() { _ = client.Close() })
	ins := &recordingInserter{}
	tier, err := New(Options{Client: client, ConsumerName: "replica-1", Inserter: ins})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	publishN(t, tier, "acme", 2)

	if _, err := tier.PurgeTenant(ctx, "acme"); err != nil {
		t.Fatalf("PurgeTenant: %v", err)
	}
	_, _ = tier.Flush(ctx, "acme")
	_, _ = tier.Reclaim(ctx, "acme", 0)
	if keyExists(t, client, streamKey("acme")) {
		t.Fatal("Flush after the purge recreated the acme stream")
	}
	if ins.count() != 0 {
		t.Fatalf("Flush after the purge replayed %d purged events", ins.count())
	}
}

// spec: 12.8 (Compliance Interfaces: Tenant deletion lifecycle, Phase 4).
func TestPurgeTenant_RejectsEmptyTenant_spec_12_8(t *testing.T) {
	tier, _ := newPurgeTier(t)
	if _, err := tier.PurgeTenant(context.Background(), ""); err == nil {
		t.Fatal("empty tenant should error")
	}
}

// spec: 12.8 (Compliance Interfaces: Tenant deletion lifecycle, Phase 4).
// A Redis error other than a missing stream is wrapped and returned.
func TestPurgeTenant_SurfacesRedisError_spec_12_8(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	tier, err := New(Options{Client: client, ConsumerName: "replica-1"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	mr.SetError("LOADING redis is loading")
	if _, err := tier.PurgeTenant(context.Background(), "acme"); err == nil {
		t.Fatal("PurgeTenant swallowed a Redis error")
	}
}

// spec: 12.8 (Compliance Interfaces: Tenant deletion lifecycle, Phase 4).
func TestIsMissingStream_spec_12_8(t *testing.T) {
	if !isMissingStream(errors.New("ERR The XGROUP subcommand requires the key to exist. Note that for CREATE you may want to use the MKSTREAM option to create an empty stream automatically.")) {
		t.Fatal("missing-key reply not recognized")
	}
	for _, err := range []error{nil, errors.New("NOGROUP No such consumer group"), errors.New("LOADING")} {
		if isMissingStream(err) {
			t.Fatalf("isMissingStream(%v) = true, want false", err)
		}
	}
}

// spec: 12.8 (Compliance Interfaces: Tenant deletion lifecycle, Phase 4);
// 12.4 Redis HA and Failure Modes. On a multi-master cluster each purge
// command routes to the tenant's master.
func TestPurgeTenant_ClusterRoutesToTenantMaster_spec_12_8(t *testing.T) {
	ctx := context.Background()
	cl := clustertest.New(t, 3)
	tier, err := New(Options{Client: cl.Client, ConsumerName: "replica-1"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	publishN(t, tier, "acme", 2)
	publishN(t, tier, "globex", 2)
	if n, err := tier.PurgeTenant(ctx, "acme"); err != nil || n != 2 {
		t.Fatalf("PurgeTenant = (%d, %v), want (2, nil)", n, err)
	}
	if keyExists(t, cl.Client, streamKey("acme")) || !keyExists(t, cl.Client, streamKey("globex")) {
		t.Fatal("cluster purge removed the wrong stream")
	}
}
