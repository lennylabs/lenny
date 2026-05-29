// SPDX-License-Identifier: MIT

package playground

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// spec: §27.8 line 241 — the propagation pub/sub payload carries the
// origin replica id and the publish instant so a peer can compute the
// end-to-end latency. encode/parse must round-trip. F-27.6.6.
func TestEncodeParseRevocationMsgRoundTrip_spec_27_8_241(t *testing.T) {
	const replica = "replica-A"
	const jti = "jti-abc_DEF-123"
	nano := time.Date(2026, 5, 29, 12, 0, 0, 0, time.UTC).UnixNano()

	gotReplica, gotNano, gotJTI, tsOK := parseRevocationMsg(encodeRevocationMsg(replica, nano, jti))
	if !tsOK {
		t.Fatal("round-trip parse returned tsOK=false")
	}
	if gotReplica != replica || gotNano != nano || gotJTI != jti {
		t.Fatalf("round-trip = (%q, %d, %q), want (%q, %d, %q)", gotReplica, gotNano, gotJTI, replica, nano, jti)
	}
}

// A payload with no envelope (a bare jti) recovers the jti so the
// negative cache is still warmed, but reports tsOK=false so no
// propagation sample is recorded for it. F-27.6.6.
func TestParseRevocationMsgBareJTI(t *testing.T) {
	replica, nano, jti, tsOK := parseRevocationMsg("just-a-jti")
	if tsOK {
		t.Fatal("bare jti parsed tsOK=true")
	}
	if replica != "" || nano != 0 || jti != "just-a-jti" {
		t.Fatalf("bare parse = (%q, %d, %q), want ('', 0, 'just-a-jti')", replica, nano, jti)
	}
}

// A non-numeric timestamp segment is treated as unparseable (tsOK=false)
// while still recovering the trailing jti segment. F-27.6.6.
func TestParseRevocationMsgBadTimestamp(t *testing.T) {
	_, _, jti, tsOK := parseRevocationMsg("replica-A|not-a-number|jti-x")
	if tsOK {
		t.Fatal("bad timestamp parsed tsOK=true")
	}
	if jti != "jti-x" {
		t.Fatalf("jti = %q, want jti-x", jti)
	}
}

// The jti is the last segment via SplitN(payload, "|", 3), so a jti
// that itself contained the delimiter would be preserved intact. This
// guards the parser against ever truncating jti material. F-27.6.6.
func TestParseRevocationMsgJTIKeepsTail(t *testing.T) {
	replica, _, jti, tsOK := parseRevocationMsg("rep|123|a|b")
	if !tsOK || replica != "rep" || jti != "a|b" {
		t.Fatalf("parse = (%q, %q, tsOK=%v), want ('rep', 'a|b', true)", replica, jti, tsOK)
	}
}

// spec: §27.6 line 204 / §10.2 tenant format — the pattern-subscription
// consume loop extracts the tenant from a concrete channel name. Only a
// well-formed t:{tenant}:pg:revocations channel yields a tenant.
// F-27.6.7.
func TestTenantFromRevocationChannel_spec_27_6_204(t *testing.T) {
	cases := []struct {
		channel string
		want    string
		ok      bool
	}{
		{"t:acme:pg:revocations", "acme", true},
		{"t:globex-1_2:pg:revocations", "globex-1_2", true},
		{"t::pg:revocations", "", false},
		{"t:acme:evt:lifecycle", "", false},
		{"acme:pg:revocations", "", false},
		{"garbage", "", false},
		{"", "", false},
	}
	for _, c := range cases {
		got, ok := tenantFromRevocationChannel(c.channel)
		if got != c.want || ok != c.ok {
			t.Errorf("tenantFromRevocationChannel(%q) = (%q, %v), want (%q, %v)", c.channel, got, ok, c.want, c.ok)
		}
	}
}

// A peer-published message warms the per-tenant negative cache and
// records exactly one §27.8 pubsub_delivered propagation sample. The
// recovered jti is then visible to IsBearerRevoked through the cache
// without a Redis round-trip. spec: §27.8 line 241. F-27.6.6.
func TestHandleRevocationMessagePeerWarmsCacheAndSamples_spec_27_8_241(t *testing.T) {
	now := time.Date(2026, 5, 29, 12, 0, 0, 0, time.UTC)
	store := &RedisSessionStore{
		now:       func() time.Time { return now },
		replicaID: "replica-self",
		cache:     map[string]time.Time{},
	}
	var samples []struct {
		outcome string
		seconds float64
	}
	store.propObserver = func(outcome string, seconds float64) {
		samples = append(samples, struct {
			outcome string
			seconds float64
		}{outcome, seconds})
	}

	publishNano := now.Add(-120 * time.Millisecond).UnixNano()
	store.handleRevocationMessage("t:acme:pg:revocations",
		encodeRevocationMsg("replica-peer", publishNano, "jti-peer"))

	// Cache warmed: IsBearerRevoked short-circuits on the cache (nil
	// client would panic on a miss, so a hit is the only way this passes).
	revoked, err := store.IsBearerRevoked(context.Background(), "acme", "jti-peer")
	if err != nil || !revoked {
		t.Fatalf("IsBearerRevoked(acme, jti-peer) = %v, %v; want true, nil", revoked, err)
	}
	if len(samples) != 1 {
		t.Fatalf("recorded %d samples, want 1", len(samples))
	}
	if samples[0].outcome != "pubsub_delivered" {
		t.Fatalf("sample outcome = %q, want pubsub_delivered", samples[0].outcome)
	}
	if samples[0].seconds < 0.10 || samples[0].seconds > 0.20 {
		t.Fatalf("sample latency = %.3fs, want ~0.120s", samples[0].seconds)
	}
}

// A message this replica published itself is not a cross-replica
// observation: the cache is still warmed (the writer benefits from its
// own fan-out) but no propagation sample is recorded. F-27.6.6.
func TestHandleRevocationMessageSelfPublishNotSampled(t *testing.T) {
	now := time.Date(2026, 5, 29, 12, 0, 0, 0, time.UTC)
	store := &RedisSessionStore{
		now:       func() time.Time { return now },
		replicaID: "replica-self",
		cache:     map[string]time.Time{},
	}
	var sampled bool
	store.propObserver = func(string, float64) { sampled = true }

	store.handleRevocationMessage("t:acme:pg:revocations",
		encodeRevocationMsg("replica-self", now.UnixNano(), "jti-own"))

	revoked, _ := store.IsBearerRevoked(context.Background(), "acme", "jti-own")
	if !revoked {
		t.Fatal("self-published revocation did not warm the cache")
	}
	if sampled {
		t.Fatal("self-published revocation recorded a propagation sample")
	}
}

// A message on an unrecognised channel is ignored: no cache mutation,
// no sample. F-27.6.7.
func TestHandleRevocationMessageBadChannelIgnored(t *testing.T) {
	store := &RedisSessionStore{
		now:       time.Now,
		replicaID: "replica-self",
		cache:     map[string]time.Time{},
	}
	var sampled bool
	store.propObserver = func(string, float64) { sampled = true }

	store.handleRevocationMessage("t:acme:evt:lifecycle",
		encodeRevocationMsg("replica-peer", time.Now().UnixNano(), "jti-x"))

	if len(store.cache) != 0 {
		t.Fatalf("bad-channel message warmed %d cache entries, want 0", len(store.cache))
	}
	if sampled {
		t.Fatal("bad-channel message recorded a propagation sample")
	}
}

// A negative publish-to-receive delta (clock skew where the publish
// stamp is in the receiver's future) clamps to zero rather than
// recording a negative latency. F-27.6.6.
func TestHandleRevocationMessageNegativeLatencyClamped(t *testing.T) {
	now := time.Date(2026, 5, 29, 12, 0, 0, 0, time.UTC)
	store := &RedisSessionStore{
		now:       func() time.Time { return now },
		replicaID: "replica-self",
		cache:     map[string]time.Time{},
	}
	var got float64 = -1
	store.propObserver = func(_ string, seconds float64) { got = seconds }

	future := now.Add(50 * time.Millisecond).UnixNano()
	store.handleRevocationMessage("t:acme:pg:revocations",
		encodeRevocationMsg("replica-peer", future, "jti-skew"))

	if got != 0 {
		t.Fatalf("clamped latency = %v, want 0", got)
	}
}

// spec: §27.6 line 204 / §27.3.1 line 96 — a single PSUBSCRIBE warms the
// negative cache for a tenant that did not exist at subscribe time. A
// RevokeSession on the (sole, self-publishing) replica must become
// visible to IsBearerRevoked through the pub/sub-warmed cache for an
// arbitrary tenant, proving the pattern subscription is not bound to a
// startup tenant list. F-27.6.6, F-27.6.7.
func TestSubscribeAllRevocationsWarmsCacheForArbitraryTenant_spec_27_6_204(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	// Two distinct stores model two replicas sharing one Redis: A
	// publishes, B observes. B must observe A's revocation for a tenant
	// neither store was told about at startup.
	publisher := NewRedisSessionStore(client)
	subscriber := NewRedisSessionStore(client)
	var mu sync.Mutex
	var outcomes []string
	subscriber.propObserver = func(outcome string, _ float64) {
		mu.Lock()
		outcomes = append(outcomes, outcome)
		mu.Unlock()
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go subscriber.SubscribeAllRevocations(ctx)

	const tenant = "tenant-provisioned-late"
	const jti = "jti-late"
	// miniredis drops a message published before the PSUBSCRIBE has
	// registered, so re-publish on each poll until the subscriber's
	// pub/sub-warmed negative cache converges. RevokeSession's marker
	// write is idempotent, so re-publishing is safe.
	waitForCondition(t, 3*time.Second, func() bool {
		if err := publisher.RevokeSession(ctx, tenant, "sess-late", []string{jti}, time.Minute); err != nil {
			t.Fatalf("RevokeSession: %v", err)
		}
		subscriber.cacheMu.RLock()
		_, ok := subscriber.cache[revokedKey(tenant, jti)]
		subscriber.cacheMu.RUnlock()
		return ok
	})

	revoked, err := subscriber.IsBearerRevoked(ctx, tenant, jti)
	if err != nil || !revoked {
		t.Fatalf("peer IsBearerRevoked = %v, %v; want true, nil", revoked, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(outcomes) == 0 || outcomes[0] != "pubsub_delivered" {
		t.Fatalf("peer outcomes = %v, want first=pubsub_delivered", outcomes)
	}
}

// waitForCondition polls cond until it returns true or the timeout
// elapses, failing the test on timeout. It absorbs the inherent pub/sub
// delivery latency of the miniredis-backed end-to-end test.
func waitForCondition(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met before timeout")
}
