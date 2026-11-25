// SPDX-License-Identifier: MIT

//go:build security

package security_test

import (
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/gateway"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
	"github.com/lennylabs/lenny/tests/testinfra/tcpfault"
	tokensvc "github.com/lennylabs/lenny/tests/testinfra/tokenservice"
)

// revBoundPropagation bounds a replica's refusal of a revoked bearer while
// its Redis link is up: the event-bus propagation plus margin.
const revBoundPropagation = 5 * time.Second

// revBoundPeriodic bounds a replica's refusal of a revoked bearer whose
// event it missed while its Redis link stays down: the 30 s periodic
// rehydration plus margin for the read.
const revBoundPeriodic = 35 * time.Second

// revBoundReconnect bounds a replica's refusal of a revoked bearer whose
// event it missed, measured from its Redis link coming back: the reconnect
// and the rehydration it triggers. It is a sixth of the 30 s period, so a
// periodic rehydration lands inside it on at most one run in six.
const revBoundReconnect = 5 * time.Second

// revBoundReplicas is two gateway replicas over one Postgres and one Redis.
// Replica B reaches Redis through linkB, so a case can cut B's link alone.
type revBoundReplicas struct {
	a, b  *gateway.Process
	linkB *tcpfault.Proxy
}

// startRevBoundReplicas starts both replicas and bootstraps acme.
func startRevBoundReplicas(t *testing.T) *revBoundReplicas {
	t.Helper()
	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: filepath.Join(schematest.RepoRoot(t), "migrations"),
	})
	rd := containers.StartRedis(t, containers.RedisOptions{})
	ts := tokensvc.StartWith(t, "--postgres-dsn="+pg.DSN, "--kms-provider=local", opsRevAlias)
	args := func(redisURL string) []string {
		return []string{
			"--no-environment-policy", "allow-all", "--dev-mode",
			"--postgres-dsn=" + pg.DSN, "--postgres-billing-audit-ddl-dsn=" + pg.DSN,
			"--redis-url=" + redisURL,
			"--tenancy-mode=multi", "--multi-tenant=true",
			"--token-service-http-url=" + ts.BaseURL(),
			"--kms-provider=local", opsRevAlias,
		}
	}
	r := &revBoundReplicas{}
	var redisB string
	r.linkB, redisB = tcpfault.StartURL(t, "redis://"+rd.Addr+"/0")
	r.a = gateway.StartWith(t, args("redis://"+rd.Addr+"/0")...)
	r.b = gateway.StartWith(t, args(redisB)...)
	r.admin(t, r.a, http.MethodPost, "/v1/admin/bootstrap", map[string]any{
		"tenants": []map[string]any{{"id": "acme", "displayName": "Acme Corp"}},
	}, http.StatusOK)
	return r
}

// admin issues an admin request on gw as a platform-admin of the default
// tenant and fails the test unless it answers want.
func (r *revBoundReplicas) admin(t *testing.T, gw *gateway.Process, method, path string, body any, want int) map[string]any {
	t.Helper()
	code, out := spoofCall(t, method, gw.BaseURL()+path, "", map[string]string{
		"X-Lenny-Tenant-ID": auth.DefaultTenantID,
		"X-Lenny-User-ID":   "carol@acme.com",
		"X-Lenny-Roles":     "platform-admin",
	}, body)
	if code != want {
		t.Fatalf("%s %s on %s: status %d, want %d: %v\n%s", method, path, gw.BaseURL(), code, want, out, gw.Logs())
	}
	return out
}

// bearer creates subject in acme and starts, on replica A, an impersonation
// session for it. The session's bearer is an issued_tokens row that any
// replica revokes, and its id is the bearer's jti.
func (r *revBoundReplicas) bearer(t *testing.T, subject string) (string, string) {
	t.Helper()
	r.admin(t, r.a, http.MethodPost, "/v1/admin/users", map[string]any{
		"tenantId": "acme", "subject": subject, "roles": []string{"user"},
	}, http.StatusCreated)
	out := r.admin(t, r.a, http.MethodPost, "/v1/admin/impersonation", map[string]any{
		"targetTenantId": "acme", "targetUserId": subject,
		"reason": "revocation bound probe", "ticketId": "revocation-bound", "durationSeconds": 600,
	}, http.StatusOK)
	tok, _ := out["bearerToken"].(string)
	if tok == "" {
		t.Fatalf("impersonation of %s returned no bearer: %v", subject, out)
	}
	for name, gw := range map[string]*gateway.Process{"A": r.a, "B": r.b} {
		if code, body := spoofCall(t, http.MethodGet, gw.BaseURL()+"/v1/admin/me", tok, nil, nil); code != http.StatusOK {
			t.Fatalf("replica %s answered %d %v for %s before its revocation, want 200", name, code, body, subject)
		}
	}
	return tok, tokenJTI(t, tok)
}

// revoke ends the session id on replica A, which revokes its bearer in
// issued_tokens and publishes the jti.
func (r *revBoundReplicas) revoke(t *testing.T, id string) {
	t.Helper()
	r.admin(t, r.a, http.MethodDelete, "/v1/admin/impersonation/"+id, nil, http.StatusOK)
}

// awaitRefused polls GET /v1/admin/me with bearer on gw until it answers
// 401 or bound passes, and returns how long it took.
func awaitRefused(t *testing.T, name string, gw *gateway.Process, bearer string, bound time.Duration) time.Duration {
	t.Helper()
	start := time.Now()
	for {
		code, body := spoofCall(t, http.MethodGet, gw.BaseURL()+"/v1/admin/me", bearer, nil, nil)
		if code == http.StatusUnauthorized {
			return time.Since(start)
		}
		if time.Since(start) > bound {
			t.Fatalf("%s answered %d %v for %s after the revocation, want 401", name, code, body, bound)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// spec: §13.3 (Credential Flow) "Authoritative durability for revocation", §13.3 (Credential Flow) "Token rotation and revocation — no grace period"
// diagnosis: a bearer revoked on replica A was still admitted on a replica
// after the bound §13.3 states. A failure with Redis up means the revoking
// replica did not publish the jti or the peer did not apply it. A failure
// with B's Redis link held down means B's periodic rehydration of
// issued_tokens does not run every 30 seconds. A failure after B's link
// comes back, within 5 seconds, means B does not rehydrate when its
// revocation subscription reconnects, and waits for the periodic read.
func TestRevokedBearerIsRefusedOnEveryReplicaWithinTheRevocationBound(t *testing.T) {
	t.Parallel()
	gateway.SkipUnlessAvailable(t)
	r := startRevBoundReplicas(t)

	t.Run("with Redis up", func(t *testing.T) {
		tok, id := r.bearer(t, "alice@acme.com")
		r.revoke(t, id)
		awaitRefused(t, "replica A", r.a, tok, revBoundPropagation)
		awaitRefused(t, "replica B", r.b, tok, revBoundPropagation)
	})

	t.Run("with the peer's Redis link held down", func(t *testing.T) {
		tok, id := r.bearer(t, "bob@acme.com")
		r.linkB.Cut()
		defer r.linkB.Restore(t)
		r.revoke(t, id)
		awaitRefused(t, "replica A", r.a, tok, revBoundPropagation)
		took := awaitRefused(t, "replica B with its Redis link down", r.b, tok, revBoundPeriodic)
		t.Logf("replica B refused the bearer %s after the revocation, with its Redis link down", took.Round(time.Millisecond))
	})

	t.Run("with the peer's Redis link restored after the revocation", func(t *testing.T) {
		// B's periodic rehydration can run between the revocation and the
		// restore, which would apply the revocation without the reconnect.
		// The attempt is repeated with a fresh bearer until B still admits
		// the bearer when its link comes back, so the refusal measured
		// after the restore is the reconnect's.
		for attempt, subject := range []string{"dave@acme.com", "erin@acme.com", "frank@acme.com"} {
			tok, id := r.bearer(t, subject)
			r.linkB.Cut()
			r.revoke(t, id)
			awaitRefused(t, "replica A", r.a, tok, revBoundPropagation)
			code, _ := spoofCall(t, http.MethodGet, r.b.BaseURL()+"/v1/admin/me", tok, nil, nil)
			r.linkB.Restore(t)
			if code == http.StatusUnauthorized {
				t.Logf("attempt %d: replica B's periodic rehydration applied the revocation before the restore; retrying", attempt+1)
				continue
			}
			took := awaitRefused(t, "replica B after its Redis link came back", r.b, tok, revBoundReconnect)
			t.Logf("replica B refused the bearer %s after its Redis link came back", took.Round(time.Millisecond))
			return
		}
		t.Fatal("replica B's periodic rehydration applied the revocation before the restore on every attempt")
	})
}
