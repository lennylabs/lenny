// SPDX-License-Identifier: MIT

package playground

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/redisconn/clustertest"
	"github.com/lennylabs/lenny/pkg/redisconn/slotguard"
)

// errInjected is the failure failFanInHook returns.
var errInjected = errors.New("injected fan-in failure")

// failFanInHook fails every command, single or pipelined, whose first key
// is a `pg:sess-tenant:` fan-in index key, so a test can fail the
// platform-scoped step of PutSession or RevokeSession on its own.
type failFanInHook struct{}

func (failFanInHook) DialHook(next redis.DialHook) redis.DialHook { return next }

func (failFanInHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		if touchesFanIn(cmd) {
			cmd.SetErr(errInjected)
			return errInjected
		}
		return next(ctx, cmd)
	}
}

func (failFanInHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		for _, cmd := range cmds {
			if touchesFanIn(cmd) {
				return errInjected
			}
		}
		return next(ctx, cmds)
	}
}

func touchesFanIn(cmd redis.Cmder) bool {
	args := cmd.Args()
	return len(args) > 1 && strings.HasPrefix(fmt.Sprint(args[1]), "pg:sess-tenant:")
}

// spec: §27.3.1 ("Session record backing store"); §12.4 Redis HA and
// Failure Modes (the `t:{tenant_id}:` prefix hashes a tenant's keys to one
// slot). Every session-record operation, including the PutSession and
// RevokeSession transactions, runs under the cross-slot hook and on an
// in-process three-master cluster, whose client rejects a cross-slot MULTI
// with redis.ErrCrossSlot. Before the split, the transactions mixed the
// tenant-tagged keys with the untagged fan-in index and failed on both.
func TestSessionRecordOperationsStayInOneSlot_spec_27_3_1(t *testing.T) {
	mr := miniredis.RunT(t)
	single := slotguard.Install(redis.NewClient(&redis.Options{Addr: mr.Addr()}))
	t.Cleanup(func() { _ = single.Close() })
	clients := map[string]redis.UniversalClient{
		"single node with slot guard": single,
		"three-master cluster":        clustertest.New(t, 3).Client,
	}
	for name, client := range clients {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			s := NewRedisSessionStore(client)
			rec := SessionRecord{UserID: "alice", TenantID: "acme", IssuedAt: time.Now()}
			if err := s.PutSession(ctx, "acme", "sid-1", rec, time.Hour); err != nil {
				t.Fatalf("PutSession: %v", err)
			}
			if tenant, ok, err := s.TenantForSession(ctx, "sid-1"); err != nil || !ok || tenant != "acme" {
				t.Fatalf("TenantForSession = (%q, %v, %v), want (acme, true, nil)", tenant, ok, err)
			}
			if ids, err := s.SessionsForUser(ctx, "acme", "alice"); err != nil || len(ids) != 1 || ids[0] != "sid-1" {
				t.Fatalf("SessionsForUser = (%v, %v), want [sid-1]", ids, err)
			}
			if err := s.RevokeSession(ctx, "acme", "sid-1", []string{"jti-1", "jti-2", ""}, time.Hour); err != nil {
				t.Fatalf("RevokeSession: %v", err)
			}
			for _, jti := range []string{"jti-1", "jti-2"} {
				if revoked, err := s.IsBearerRevoked(ctx, "acme", jti); err != nil || !revoked {
					t.Fatalf("IsBearerRevoked(%s) = (%v, %v), want (true, nil)", jti, revoked, err)
				}
			}
			if _, ok, err := s.TenantForSession(ctx, "sid-1"); err != nil || ok {
				t.Fatalf("TenantForSession after revoke = (ok %v, %v), want a miss", ok, err)
			}
			if _, err := s.GetSession(ctx, "acme", "sid-1"); !errors.Is(err, errSessionNotFound) {
				t.Fatalf("GetSession after revoke err = %v, want errSessionNotFound", err)
			}
		})
	}
}

// spec: §27.3.1 ("Session record backing store"). PutSession writes the
// fan-in index after the tenant-tagged transaction. When the index write
// fails, PutSession returns the error, and the record it left behind is
// unreachable: TenantForSession misses, so a cookie carrying the id
// resolves to no session.
func TestPutSessionFanInFailureFailsClosed_spec_27_3_1(t *testing.T) {
	mr := miniredis.RunT(t)
	failing := slotguard.Install(redis.NewClient(&redis.Options{Addr: mr.Addr()}))
	failing.AddHook(failFanInHook{})
	clean := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = failing.Close(); _ = clean.Close() })
	ctx := context.Background()

	err := NewRedisSessionStore(failing).PutSession(ctx, "acme", "sid-1",
		SessionRecord{UserID: "alice", TenantID: "acme"}, time.Hour)
	if !errors.Is(err, errInjected) {
		t.Fatalf("PutSession err = %v, want the injected fan-in failure", err)
	}
	if _, ok, err := NewRedisSessionStore(clean).TenantForSession(ctx, "sid-1"); err != nil || ok {
		t.Fatalf("TenantForSession after a failed put = (ok %v, %v), want a miss", ok, err)
	}
}

// spec: §27.3.1 ("Session record backing store"); §27.6. RevokeSession
// commits the session delete and the revocation markers before it touches
// the fan-in index. When the index delete fails, RevokeSession returns the
// error, yet the revocation is already durable: the markers are set, the
// record is gone, and the leftover index entry resolves to a tenant with
// no session. A retry is idempotent and clears the index.
func TestRevokeSessionFanInFailureKeepsRevocationDurable_spec_27_3_1(t *testing.T) {
	mr := miniredis.RunT(t)
	failing := slotguard.Install(redis.NewClient(&redis.Options{Addr: mr.Addr()}))
	failing.AddHook(failFanInHook{})
	clean := slotguard.Install(redis.NewClient(&redis.Options{Addr: mr.Addr()}))
	t.Cleanup(func() { _ = failing.Close(); _ = clean.Close() })
	ctx := context.Background()
	good := NewRedisSessionStore(clean)

	if err := good.PutSession(ctx, "acme", "sid-1", SessionRecord{UserID: "alice", TenantID: "acme"}, time.Hour); err != nil {
		t.Fatalf("PutSession: %v", err)
	}
	err := NewRedisSessionStore(failing).RevokeSession(ctx, "acme", "sid-1", []string{"jti-1"}, time.Hour)
	if !errors.Is(err, errInjected) {
		t.Fatalf("RevokeSession err = %v, want the injected fan-in failure", err)
	}
	if revoked, err := good.IsBearerRevoked(ctx, "acme", "jti-1"); err != nil || !revoked {
		t.Fatalf("IsBearerRevoked after a partial revoke = (%v, %v), want (true, nil)", revoked, err)
	}
	if _, err := good.GetSession(ctx, "acme", "sid-1"); !errors.Is(err, errSessionNotFound) {
		t.Fatalf("GetSession after a partial revoke err = %v, want errSessionNotFound", err)
	}
	if err := good.RevokeSession(ctx, "acme", "sid-1", []string{"jti-1"}, time.Hour); err != nil {
		t.Fatalf("retried RevokeSession: %v", err)
	}
	if _, ok, err := good.TenantForSession(ctx, "sid-1"); err != nil || ok {
		t.Fatalf("TenantForSession after retry = (ok %v, %v), want a miss", ok, err)
	}
}

// spec: §27.6; §12.4 Redis HA and Failure Modes (Redis Cluster at Tier 3).
// The idle-session sweep's SCAN spans tenants, so on a cluster it runs on
// every master. Sessions of tenants whose slots sit on different masters
// are all returned; a SCAN on the cluster client alone would return only
// one master's sessions (F-12.4.29).
func TestIdleSessionsCoversEveryClusterMaster_spec_27_6(t *testing.T) {
	c := clustertest.New(t, 3)
	ctx := context.Background()
	s := NewRedisSessionStore(c.Client)
	old := time.Now().Add(-2 * time.Hour)

	var want []string
	masters := map[string]bool{}
	for i := 0; i < 12; i++ {
		tenant := fmt.Sprintf("tenant-%d", i)
		id := fmt.Sprintf("sid-%d", i)
		rec := SessionRecord{TenantID: tenant, UserID: "alice", IssuedAt: old, LastActivityAt: old}
		if err := s.PutSession(ctx, tenant, id, rec, 3*time.Hour); err != nil {
			t.Fatalf("PutSession(%s): %v", tenant, err)
		}
		want = append(want, tenant+"/"+id)
		masters[c.ServerFor(sessionKey(tenant, id)).Addr()] = true
	}
	// A fresh session and an invalidated one are not reclamation candidates.
	if err := s.PutSession(ctx, "tenant-0", "fresh", SessionRecord{TenantID: "tenant-0", LastActivityAt: time.Now()}, time.Hour); err != nil {
		t.Fatal(err)
	}
	if err := s.PutSession(ctx, "tenant-1", "dead", SessionRecord{TenantID: "tenant-1", LastActivityAt: old, Invalidated: true}, time.Hour); err != nil {
		t.Fatal(err)
	}
	if len(masters) < 2 {
		t.Fatalf("fixture: sessions landed on %d master(s), want them spread", len(masters))
	}

	refs, err := s.IdleSessions(ctx, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatalf("IdleSessions: %v", err)
	}
	var got []string
	for _, r := range refs {
		got = append(got, r.Tenant+"/"+r.ID)
	}
	sort.Strings(got)
	sort.Strings(want)
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("IdleSessions = %v, want %v", got, want)
	}
}
