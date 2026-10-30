// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration coverage for the §8.3 BUDGET_KEYS_EXPIRED reservation
// detection and the §8.6 grant record the reservation's token cap reads.
//
// The denialpg case pins that the delegation_tree_budget row's cumulative
// ext_* columns are what Grants returns. The compose case drives the real
// cmd/lenny-gateway binary over Postgres and Redis: a delegation creates the
// tree's budget keys, the test deletes the tokens sentinel, and the next
// lenny/delegate_task must fail with the permanent INVALID_STATE_TRANSITION,
// fail the root with failure_class budget_keys_expired, cancel every
// descendant, and raise the gateway's detection counter by one.
package tier4_integration_test

import (
	"bufio"
	"context"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/leasecontrol"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/leasecontrol/denialpg"
	sessionstorepg "github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/pgstore"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/gateway"
)

// spec: 8.6 (Extension state), 8.3
// diagnosis: denialpg.Grants does not read the delegation_tree_budget row's
// cumulative ext_* columns. Either a tree with no row read non-zero, or two
// grants did not sum, so the delegate_task token cap reads a ceiling that
// differs from the grants the extension path committed.
func TestDenialStoreGrantsReadsCumulativeExtColumns_spec_8_6(t *testing.T) {
	t.Parallel()
	pg := sharedDatabase(t)
	ctx := context.Background()
	const tenant, root = "acme", "00000000-0000-4000-8000-0000000083b1"
	if _, err := pg.Pool.Exec(ctx, `INSERT INTO tenants (id, genesis_nonce) VALUES ($1, '\x00') ON CONFLICT DO NOTHING`, tenant); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	store := denialpg.New(pg.Pool)
	got, err := store.Grants(ctx, tenant, root)
	if err != nil || got != (leasecontrol.Dimensions{}) {
		t.Fatalf("Grants for a missing row = %+v, %v, want zero", got, err)
	}
	limit := leasecontrol.Dimensions{Tokens: 1 << 40, Seconds: 1 << 30}
	for _, g := range []leasecontrol.Dimensions{{Tokens: 1000, Seconds: 60}, {Tokens: 2500}} {
		if _, err := store.Grant(ctx, tenant, root, g, limit); err != nil {
			t.Fatalf("Grant %+v: %v", g, err)
		}
	}
	got, err = store.Grants(ctx, tenant, root)
	if err != nil {
		t.Fatalf("Grants: %v", err)
	}
	if got.Tokens != 3500 || got.Seconds != 60 {
		t.Fatalf("Grants after two grants = %+v, want Tokens 3500 Seconds 60", got)
	}
}

// spec: 8.3 (BUDGET_KEYS_EXPIRED detection and tree cleanup), 16.1, 15.2.1
// diagnosis: an expired tree budget did not end the tree through the gateway.
// Either the next lenny/delegate_task after the tokens sentinel was deleted
// was admitted (the scripts recreated the keys), returned a retryable code,
// the root did not read back failed with failure_class budget_keys_expired, a
// descendant stayed live, or the gateway's
// lenny_delegation_budget_keys_expired_total did not rise by one (the
// detection counter is not wired into the session server's cleanup).
func TestDelegateTaskAfterBudgetKeysExpireFailsTheTree_spec_8_3(t *testing.T) {
	t.Parallel()
	pg := sharedDatabase(t)
	rd := containers.StartRedis(t, containers.RedisOptions{})
	gw := gateway.StartWith(t, "--no-environment-policy", "allow-all", "--dev-mode",
		"--postgres-dsn="+pg.DSN, "--redis-url=redis://"+rd.Addr+"/0")
	c := mcpClient{t: t, base: gw.BaseURL()}
	ctx := context.Background()

	root := c.runningSession()
	child := c.delegateChild(root, "echo-child")
	sentinel := "{" + root + "}:dlg:tokens"
	if n, err := rd.Client.Exists(ctx, sentinel).Result(); err != nil || n != 1 {
		t.Fatalf("the first delegation did not create %s (%d, %v)", sentinel, n, err)
	}
	before := scrapeKeysExpired(t, gw.BaseURL())
	if err := rd.Client.Del(ctx, sentinel).Err(); err != nil {
		t.Fatalf("delete sentinel: %v", err)
	}

	rpc := c.callTool("lenny/delegate_task", `{"parentSessionId":"`+root+`","target":"echo-grandchild"}`)
	text, isErr := toolResultText(t, rpc)
	if !isErr {
		t.Fatalf("delegate_task after expiry succeeded: %s", text)
	}
	env := lennyErrorEnvelope(t, rpc)
	if env["code"] != "INVALID_STATE_TRANSITION" || env["retryable"] != false {
		t.Fatalf("error envelope = %v, want INVALID_STATE_TRANSITION with retryable false", env)
	}

	sessions := sessionstorepg.New(pg.Pool)
	deadline := time.Now().Add(30 * time.Second)
	for {
		r, rerr := sessions.Get(ctx, mcpTenant, root)
		ch, cerr := sessions.Get(ctx, mcpTenant, child)
		if rerr == nil && cerr == nil && r.State == session.StateFailed && ch.State == session.StateCancelled {
			if r.FailureClass != session.FailureClassBudgetKeysExpired {
				t.Fatalf("root failure_class = %q, want budget_keys_expired", r.FailureClass)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("root %s (%v) / child %s (%v) did not reach failed / cancelled", r.State, rerr, ch.State, cerr)
		}
		time.Sleep(200 * time.Millisecond)
	}
	if after := scrapeKeysExpired(t, gw.BaseURL()); after-before != 1 {
		t.Fatalf("lenny_delegation_budget_keys_expired_total for tenant %s rose by %v, want 1", mcpTenant, after-before)
	}
}

// scrapeKeysExpired sums lenny_delegation_budget_keys_expired_total over every
// pool for the MCP tenant from the gateway's /metrics exposition.
func scrapeKeysExpired(t *testing.T, base string) float64 {
	t.Helper()
	resp, err := http.Get(base + "/metrics")
	if err != nil {
		t.Fatalf("GET /metrics: %v", err)
	}
	defer resp.Body.Close()
	var sum float64
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "lenny_delegation_budget_keys_expired_total{") ||
			!strings.Contains(line, `tenant_id="`+mcpTenant+`"`) {
			continue
		}
		v, err := strconv.ParseFloat(line[strings.LastIndex(line, " ")+1:], 64)
		if err != nil {
			t.Fatalf("parse %q: %v", line, err)
		}
		sum += v
	}
	return sum
}
