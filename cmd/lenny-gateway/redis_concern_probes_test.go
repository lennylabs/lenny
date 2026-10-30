// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/gateway/operability/health"
	"github.com/lennylabs/lenny/pkg/gateway/storage/redistopology"
	"github.com/lennylabs/lenny/pkg/redisconn"
	"github.com/lennylabs/lenny/pkg/storerouter"
)

// splitTopology builds a base Redis and a per-concern split in which each
// concern in dedicated points at its own miniredis (concerns that share a
// server in dedicated share one URL). It returns the base server, the
// concern servers, and the resolved clients.
func splitTopology(t *testing.T, dedicated map[storerouter.RedisConcern]string) (*miniredis.Miniredis, map[string]*miniredis.Miniredis, redis.UniversalClient, *redistopology.Clients) {
	t.Helper()
	baseSrv := miniredis.RunT(t)
	base := redis.NewClient(&redis.Options{Addr: baseSrv.Addr()})
	t.Cleanup(func() { _ = base.Close() })
	servers := map[string]*miniredis.Miniredis{}
	urls := map[storerouter.RedisConcern]string{}
	for concern, group := range dedicated {
		srv, ok := servers[group]
		if !ok {
			srv = miniredis.RunT(t)
			servers[group] = srv
		}
		urls[concern] = "redis://" + srv.Addr()
	}
	clients, err := redistopology.Build(base, urls, redisconn.Config{AllowInsecure: true})
	if err != nil {
		t.Fatalf("redistopology.Build: %v", err)
	}
	t.Cleanup(func() { _ = clients.Close() })
	return baseSrv, servers, base, clients
}

// spec: §12.4 Redis HA and Failure Modes ("Logical separation of Redis
// concerns"), §12.6 Interface Design (RedisConcern). A recovery probe for a
// split concern reads that concern's instance: with the Quota instance down
// and the base up the probe reports down, and with the base down and the
// Quota instance up it reports up. An unsplit concern follows the base.
func TestConcernRedisProbeReadsTheConcernInstance_spec_12_4(t *testing.T) {
	baseSrv, servers, _, clients := splitTopology(t, map[storerouter.RedisConcern]string{
		storerouter.RedisConcernQuota: "quota",
	})
	ctx := context.Background()
	quotaProbe := concernRedisProbe(clients, storerouter.RedisConcernQuota)
	coordProbe := concernRedisProbe(clients, storerouter.RedisConcernCoordination)
	if !quotaProbe(ctx) || !coordProbe(ctx) {
		t.Fatal("both probes must report up while every instance is up")
	}

	servers["quota"].Close()
	if quotaProbe(ctx) {
		t.Error("quota probe reported up while the quota instance is down")
	}
	if !coordProbe(ctx) {
		t.Error("coordination probe (unsplit, base) reported down while the base is up")
	}

	if err := servers["quota"].Restart(); err != nil {
		t.Fatalf("restart quota: %v", err)
	}
	baseSrv.Close()
	if !quotaProbe(ctx) {
		t.Error("quota probe reported down while the quota instance is up and only the base is down")
	}
	if coordProbe(ctx) {
		t.Error("coordination probe (unsplit, base) reported up while the base is down")
	}
}

// spec: §12.4 Redis HA and Failure Modes ("Logical separation of Redis
// concerns"), §12.6 Interface Design (RedisConcern). With no Redis wired the
// concern resolves to no client, and the probe reports down rather than
// panicking.
func TestConcernRedisProbeWithoutRedisReportsDown_spec_12_4(t *testing.T) {
	var clients *redistopology.Clients
	if concernRedisProbe(clients, storerouter.RedisConcernQuota)(context.Background()) {
		t.Fatal("probe with no Redis reported up")
	}
}

type recordingRegistrar struct{ checkers []health.Checker }

func (r *recordingRegistrar) Register(c health.Checker) { r.checkers = append(r.checkers, c) }

func (r *recordingRegistrar) byName() map[string]health.Checker {
	out := map[string]health.Checker{}
	for _, c := range r.checkers {
		out[c.Name()] = c
	}
	return out
}

// spec: §12.4 Redis HA and Failure Modes ("Logical separation of Redis
// concerns"), §12.6 Interface Design (RedisConcern), §25.3. The health
// aggregator receives the base client as "redis" and each distinct
// dedicated concern client exactly once: two concerns that share a URL
// share one component, and an unsplit concern adds none. A down concern
// instance shows as an unhealthy component while the base stays healthy.
func TestRegisterRedisHealthRegistersEachDistinctConcernClient_spec_12_4(t *testing.T) {
	_, servers, base, clients := splitTopology(t, map[storerouter.RedisConcern]string{
		storerouter.RedisConcernQuota:       "shared",
		storerouter.RedisConcernDelegation:  "shared",
		storerouter.RedisConcernSessionData: "session",
	})
	reg := &recordingRegistrar{}
	registerRedisHealth(reg, base, clients)

	got := reg.byName()
	want := []string{"redis", "redis-quota-delegation", "redis-session_data"}
	if len(reg.checkers) != len(want) {
		names := make([]string, 0, len(reg.checkers))
		for _, c := range reg.checkers {
			names = append(names, c.Name())
		}
		t.Fatalf("registered %v, want %v", names, want)
	}
	for _, name := range want {
		if _, ok := got[name]; !ok {
			t.Fatalf("component %q not registered; got %v", name, got)
		}
	}

	ctx := context.Background()
	servers["session"].Close()
	if s := got["redis-session_data"].Check(ctx).Status; s != health.StatusUnhealthy {
		t.Errorf("redis-session_data status = %s with its instance down, want unhealthy", s)
	}
	if s := got["redis"].Check(ctx).Status; s != health.StatusHealthy {
		t.Errorf("redis status = %s with the base up, want healthy", s)
	}
	if s := got["redis-quota-delegation"].Check(ctx).Status; s != health.StatusHealthy {
		t.Errorf("redis-quota-delegation status = %s with its instance up, want healthy", s)
	}
}

// spec: §12.4 Redis HA and Failure Modes ("Logical separation of Redis
// concerns"), §12.6 Interface Design (RedisConcern). An unsplit topology
// registers only the base component, and no Redis registers nothing.
func TestRegisterRedisHealthUnsplitAndAbsent_spec_12_4(t *testing.T) {
	_, _, base, clients := splitTopology(t, nil)
	reg := &recordingRegistrar{}
	registerRedisHealth(reg, base, clients)
	if len(reg.checkers) != 1 || reg.checkers[0].Name() != "redis" {
		t.Fatalf("unsplit topology registered %d components, want only redis", len(reg.checkers))
	}
	none := &recordingRegistrar{}
	registerRedisHealth(none, nil, nil)
	if len(none.checkers) != 0 {
		t.Fatalf("no Redis registered %d components, want 0", len(none.checkers))
	}
}

// spec: §12.4 Redis HA and Failure Modes ("Logical separation of Redis
// concerns"), §12.6 Interface Design (RedisConcern). Each concern-specific
// recovery probe and the dual-store monitor are wired to the concern whose
// keys they guard: storage quota and token-usage checkpoint to Quota, the
// delegation budget to Delegation, and the §10.1 dual-store monitor to
// Coordination. No probe pings the base client directly. The wiring sits in
// the gateway's composition root, which needs Postgres and Redis to run, so
// this test reads the wiring source; the probe behavior itself is pinned by
// TestConcernRedisProbeReadsTheConcernInstance_spec_12_4.
func TestRecoveryProbesNameTheirConcern_spec_12_4(t *testing.T) {
	cases := []struct {
		file, anchor, concern string
	}{
		{"redis_quota.go", "storagequota.RecoveryReconciler{", "RedisConcernQuota"},
		{"reconciler_workers.go", "delegationbudget.Reconciler{", "RedisConcernDelegation"},
		{"reconciler_workers.go", "quotacheckpoint.Reconciler{", "RedisConcernQuota"},
		{"metricsbackfill.go", "dualstore.Monitor{", "RedisConcernCoordination"},
	}
	for _, tc := range cases {
		src, err := os.ReadFile(tc.file)
		if err != nil {
			t.Fatal(err)
		}
		s := string(src)
		i := strings.Index(s, tc.anchor)
		if i < 0 {
			t.Fatalf("%s: anchor %q not found", tc.file, tc.anchor)
		}
		block := s[i:]
		if end := strings.Index(block, "\n\t\t}\n"); end > 0 {
			block = block[:end]
		}
		want := "concernRedisProbe(w.concernRedis, storerouter." + tc.concern + ")"
		if tc.file == "redis_quota.go" {
			want = "concernRedisProbe(concernRedis, storerouter." + tc.concern + ")"
		}
		if !strings.Contains(block, want) {
			t.Errorf("%s %s: probe is not %s", tc.file, tc.anchor, want)
		}
	}
	basePing := regexp.MustCompile(`PingWithTimeout\((w\.)?redisClient,\s*2\*time\.Second\)`)
	for _, f := range []string{"redis_quota.go", "reconciler_workers.go", "metricsbackfill.go"} {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if loc := basePing.FindIndex(src); loc != nil {
			t.Errorf("%s still pings the base client in a recovery probe: %s", f, src[loc[0]:loc[1]])
		}
	}
}
