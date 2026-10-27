// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration test for the §13.3 Platform-admin impersonation
// paragraph against two replicas of the real cmd/lenny-gateway binary over
// one migrated Postgres and one Redis. An impersonation session is the
// issued_tokens row of the bearer it mints, so a session issued on one
// replica is listed and ended from the other, survives a restart of the
// issuing replica, is refused on both replicas once ended (with Redis up
// and with the issuing replica's Redis link cut), leaves the listing at
// exp without an ended event, is ended by a §11.4 full revoke of the target
// without an ended event, and yields one winner under concurrent ends.
//
// The full-revoke cascade on a platform-admin that started sessions is
// covered by TestFullRevokeOfPlatformAdminRevokesItsBearersOnEveryReplica_spec_13_3
// in impersonation_admin_cascade_test.go.
package tier4_integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/gateway"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
	"github.com/lennylabs/lenny/tests/testinfra/tcpfault"
	tokensvc "github.com/lennylabs/lenny/tests/testinfra/tokenservice"
)

// impPropagationBound bounds a peer replica's refusal of an ended bearer
// while its Redis link is up: the EventBus propagation plus margin, well
// inside the 30 s rehydrate.
const impPropagationBound = 5 * time.Second

// impCaller is the platform-admin that starts, lists, and ends every
// session in this file.
const impCaller = "carol@acme.com"

// impReplicas is two gateway replicas over one Postgres and one Redis.
// Replica A reaches Redis through redisLinkA, so a case can cut A's link
// alone; argsA restarts A with the same flags.
type impReplicas struct {
	pg         *containers.Postgres
	gwA, gwB   *gateway.Process
	argsA      []string
	redisLinkA *tcpfault.Proxy
}

// startImpReplicas starts the two replicas and bootstraps acme.
func startImpReplicas(t *testing.T) *impReplicas {
	t.Helper()
	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: filepath.Join(schematest.RepoRoot(t), "migrations"),
	})
	rd := containers.StartRedis(t, containers.RedisOptions{})
	ts := tokensvc.StartWith(t, "--postgres-dsn="+pg.DSN)
	key := writeTenancyTrustKey(t)
	link := tcpfault.Start(t, rd.Addr)
	r := &impReplicas{
		pg:         pg,
		argsA:      deletionGatewayArgs(pg.DSN, link.Addr(), key, ts.BaseURL()),
		redisLinkA: link,
	}
	r.gwA = gateway.StartWith(t, r.argsA...)
	r.gwB = gateway.StartWith(t, deletionGatewayArgs(pg.DSN, rd.Addr, key, ts.BaseURL())...)
	code, raw := deletionAdminDo(t, r.gwA.BaseURL(), http.MethodPost, "/v1/admin/bootstrap", map[string]any{
		"tenants": []map[string]any{{"id": "acme", "displayName": "Acme Corp"}},
	})
	if code != http.StatusOK {
		t.Fatalf("bootstrap: status %d: %s", code, raw)
	}
	return r
}

// replicas returns the base URLs of both replicas.
func (r *impReplicas) replicas() []string {
	return []string{r.gwA.BaseURL(), r.gwB.BaseURL()}
}

// session creates subject in acme and starts, on base, an impersonation
// session for it. It returns the bearer and its session id.
func (r *impReplicas) session(t *testing.T, base, subject string) (string, string) {
	t.Helper()
	createTenantUser(t, base, "acme", subject)
	bearer := mintImpersonationAs(t, base, impCaller, "acme", subject)
	return bearer, rotatedJTI(t, bearer)
}

// end issues DELETE /v1/admin/impersonation/{id} on base and returns the
// status and decoded body.
func (r *impReplicas) end(t *testing.T, base, id string) (int, map[string]any) {
	t.Helper()
	code, raw := cascadeAdminDo(t, base, impCaller, http.MethodDelete, "/v1/admin/impersonation/"+id, nil)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return code, out
}

// listed returns the session ids GET /v1/admin/impersonation lists on base.
func (r *impReplicas) listed(t *testing.T, base string) []string {
	t.Helper()
	code, raw := cascadeAdminDo(t, base, impCaller, http.MethodGet, "/v1/admin/impersonation", nil)
	var out struct {
		Items []struct {
			ID       string `json:"impersonationSessionId"`
			AdminSub string `json:"adminSub"`
		} `json:"items"`
	}
	if code != http.StatusOK || json.Unmarshal(raw, &out) != nil {
		t.Fatalf("list impersonation sessions on %s: status %d: %s", base, code, raw)
	}
	ids := make([]string, 0, len(out.Items))
	for _, it := range out.Items {
		ids = append(ids, it.ID)
	}
	return ids
}

// endedEvents counts the admin.impersonation_ended rows naming id.
func (r *impReplicas) endedEvents(t *testing.T, id string) int {
	t.Helper()
	var n int
	if err := r.pg.Pool.QueryRow(context.Background(),
		`SELECT count(*) FROM audit_log
		 WHERE event_type = 'admin.impersonation_ended' AND payload->>'impersonation_session_id' = $1`,
		id).Scan(&n); err != nil {
		t.Fatalf("count admin.impersonation_ended rows for %s: %v", id, err)
	}
	return n
}

// requireAdmitted fails the test unless every replica admits bearer, so a
// later refusal rests on the revocation rather than on a malformed token.
func requireAdmitted(t *testing.T, name, bearer string, replicas []string) {
	t.Helper()
	for _, base := range replicas {
		if code, _ := tenancyCall(t, http.MethodGet, base+"/v1/admin/me", bearer, nil, nil); code != http.StatusOK {
			t.Fatalf("%s on %s answered %d before its end, want 200", name, base, code)
		}
	}
}

// requireEndedOnOtherReplica ends on B the session A issued and asserts
// both replicas refuse its bearer within bound and one ended event exists.
// beforeEnd runs after both replicas admitted the bearer and before the end.
func (r *impReplicas) requireEndedOnOtherReplica(t *testing.T, subject string, bound time.Duration, beforeEnd func()) {
	t.Helper()
	bearer, id := r.session(t, r.gwA.BaseURL(), subject)
	requireAdmitted(t, subject, bearer, r.replicas())
	beforeEnd()
	code, body := r.end(t, r.gwB.BaseURL(), id)
	if code != http.StatusOK || body["impersonationSessionId"] != id || body["endedAt"] == nil {
		t.Fatalf("DELETE on the non-issuing replica: status %d body %v, want 200 naming %s", code, body, id)
	}
	awaitRefusedOnReplicasWithin(t, subject, bearer, r.replicas(), bound)
	if n := r.endedEvents(t, id); n != 1 {
		t.Errorf("admin.impersonation_ended rows for %s = %d, want 1", id, n)
	}
}

// endRace issues DELETE /v1/admin/impersonation/{id} on base without
// failing the test, so it runs off the test goroutine.
func endRace(ctx context.Context, base, id string) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, base+"/v1/admin/impersonation/"+id, http.NoBody)
	if err != nil {
		return 0, err
	}
	req.Header.Set("X-Lenny-Tenant-ID", "default")
	req.Header.Set("X-Lenny-User-ID", impCaller)
	req.Header.Set("X-Lenny-Roles", "platform-admin")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode, nil
}

// startShortSession starts, on base, a session of durationSeconds 60 for a
// new acme user subject and returns its bearer, id, and exp.
func (r *impReplicas) startShortSession(t *testing.T, base, subject string) (string, string, time.Time) {
	t.Helper()
	createTenantUser(t, base, "acme", subject)
	code, raw := cascadeAdminDo(t, base, impCaller, http.MethodPost, "/v1/admin/impersonation", map[string]any{
		"targetTenantId": "acme", "targetUserId": subject,
		"reason": "impersonation expiry probe", "ticketId": "expiry", "durationSeconds": 60,
	})
	var out struct {
		BearerToken string `json:"bearerToken"`
		ID          string `json:"impersonationSessionId"`
		ExpiresAt   string `json:"expiresAt"`
	}
	if code != http.StatusOK || json.Unmarshal(bytes.TrimSpace(raw), &out) != nil || out.BearerToken == "" {
		t.Fatalf("start 60 s session for %s: status %d: %s", subject, code, raw)
	}
	exp, err := time.Parse(time.RFC3339, out.ExpiresAt)
	if err != nil {
		t.Fatalf("expiresAt %q: %v", out.ExpiresAt, err)
	}
	return out.BearerToken, out.ID, exp
}

// spec: 13.3 (Platform-admin impersonation), 11.4 (User Invalidation), 15.1 (REST API), 16.7 (Section 25 Audit Events)
// diagnosis: a bearer admitted on either replica after DELETE means the end
// did not revoke the issued_tokens row or the ending replica did not push
// the jti to its peers; a failure only with A's Redis link cut means A does
// not rehydrate revocations from Postgres within 30 s. A session missing
// from B's listing, or a 404 from B after A restarts, means session state
// lives on the issuing replica rather than in issued_tokens. A target full
// revoke that leaves the bearer admitted means §11.4 step 5 does not reach
// the impersonation row; an ended event after it, or after expiry, means a
// writer other than a winning DELETE appends admin.impersonation_ended.
// Two 200s or two ended events under concurrent DELETE mean the end is not
// a conditional update on the active row.
func TestImpersonationSessionIsSharedAcrossReplicas_spec_13_3(t *testing.T) {
	gateway.SkipUnlessAvailable(t)
	r := startImpReplicas(t)
	// The short session is started first so its exp has passed by the time
	// the expiry case runs after the other cases.
	shortBearer, shortID, shortExp := r.startShortSession(t, r.gwA.BaseURL(), "olivia@acme.com")

	t.Run("listing on the non-issuing replica", func(t *testing.T) {
		_, id := r.session(t, r.gwA.BaseURL(), "judy@acme.com")
		if got := r.listed(t, r.gwB.BaseURL()); !slices.Contains(got, id) {
			t.Errorf("GET on B lists %v, want it to include %s issued on A", got, id)
		}
	})

	t.Run("end on the non-issuing replica with Redis up", func(t *testing.T) {
		r.requireEndedOnOtherReplica(t, "bob@acme.com", impPropagationBound, func() {})
	})

	t.Run("target full revoke", func(t *testing.T) {
		const target = "niaj@acme.com"
		bearer, id := r.session(t, r.gwA.BaseURL(), target)
		requireAdmitted(t, target, bearer, r.replicas())
		code, raw := cascadeAdminDo(t, r.gwB.BaseURL(), impCaller, http.MethodPost,
			"/v1/admin/users/"+target+"/invalidate", map[string]any{
				"tenantId": "acme", "mode": "full_revoke", "reason": "impersonation target revoke probe",
			})
		if code != http.StatusOK {
			t.Fatalf("full revoke of %s: status %d: %s", target, code, raw)
		}
		awaitRefusedOnReplicas(t, target, bearer, r.replicas())
		code, body := r.end(t, r.gwA.BaseURL(), id)
		errBody, _ := body["error"].(map[string]any)
		details, _ := errBody["details"].(map[string]any)
		if code != http.StatusConflict || errBody["code"] != "INVALID_STATE_TRANSITION" || details["currentState"] != "revoked" {
			t.Errorf("DELETE after the target full revoke: status %d body %v, want 409 INVALID_STATE_TRANSITION with currentState revoked", code, body)
		}
		if n := r.endedEvents(t, id); n != 0 {
			t.Errorf("admin.impersonation_ended rows for %s = %d after a full revoke, want 0", id, n)
		}
	})

	t.Run("concurrent end on both replicas", func(t *testing.T) {
		_, id := r.session(t, r.gwA.BaseURL(), "peggy@acme.com")
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		start := make(chan struct{})
		codes := make([]int, 2)
		errs := make([]error, 2)
		var wg sync.WaitGroup
		for i, base := range r.replicas() {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				codes[i], errs[i] = endRace(ctx, base, id)
			}()
		}
		close(start)
		wg.Wait()
		for i, err := range errs {
			if err != nil {
				t.Fatalf("DELETE %d: %v", i, err)
			}
		}
		slices.Sort(codes)
		if !slices.Equal(codes, []int{http.StatusOK, http.StatusConflict}) {
			t.Errorf("concurrent DELETE statuses = %v, want one 200 and one 409", codes)
		}
		if n := r.endedEvents(t, id); n != 1 {
			t.Errorf("admin.impersonation_ended rows for %s = %d, want 1", id, n)
		}
	})

	t.Run("end on the non-issuing replica with the issuing replica's Redis link cut", func(t *testing.T) {
		defer r.redisLinkA.Restore(t)
		r.requireEndedOnOtherReplica(t, "ivan@acme.com", cascadeRefusalBound, r.redisLinkA.Cut)
	})

	t.Run("expiry", func(t *testing.T) {
		// The §13.3 exp check allows one second of skew.
		if wait := time.Until(shortExp.Add(2 * time.Second)); wait > 0 {
			time.Sleep(wait)
		}
		for _, base := range r.replicas() {
			if code, _ := tenancyCall(t, http.MethodGet, base+"/v1/admin/me", shortBearer, nil, nil); code != http.StatusUnauthorized {
				t.Errorf("expired bearer on %s answered %d, want 401", base, code)
			}
			if got := r.listed(t, base); slices.Contains(got, shortID) {
				t.Errorf("GET on %s still lists the expired session %s", base, shortID)
			}
		}
		if n := r.endedEvents(t, shortID); n != 0 {
			t.Errorf("admin.impersonation_ended rows for the expired session = %d, want 0", n)
		}
	})

	t.Run("restart of the issuing replica", func(t *testing.T) {
		bearer, id := r.session(t, r.gwA.BaseURL(), "mallory@acme.com")
		r.gwA.Stop(t)
		r.gwA = gateway.StartWith(t, r.argsA...)
		if got := r.listed(t, r.gwB.BaseURL()); !slices.Contains(got, id) {
			t.Fatalf("GET on B after A restarted lists %v, want %s", got, id)
		}
		if code, body := r.end(t, r.gwB.BaseURL(), id); code != http.StatusOK {
			t.Fatalf("DELETE on B after A restarted: status %d body %v, want 200", code, body)
		}
		awaitRefusedOnReplicas(t, "mallory@acme.com", bearer, r.replicas())
		if n := r.endedEvents(t, id); n != 1 {
			t.Errorf("admin.impersonation_ended rows for %s = %d, want 1", id, n)
		}
	})
}
