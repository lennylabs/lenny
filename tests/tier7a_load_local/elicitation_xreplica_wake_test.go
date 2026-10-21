// SPDX-License-Identifier: MIT

//go:build load_local

// Tier-7a coverage for the cross-replica wake of a blocked
// lenny/request_elicitation call.
//
// On the multi-replica gateway topology the pod's MCP call that raised an
// elicitation blocks on the replica that serves the pod (replica A), while
// the human's respond or dismiss, or the session's departure from its pod
// that expires the elicitations it raised, can land on any replica
// (replica B). The tool-approval await answers the same problem with an
// in-process waiter registry plus a poll of the Postgres-backed shared
// interaction store. These cases build two independent registrations of the
// platform MCP tools, one per replica, that share nothing but the Postgres
// interaction store and the session rows, raise elicitations on A, resolve
// them on B through B's own respond and dismiss tools and through B's
// expiry, and measure how long A's blocked call takes to return after B's
// write commits.
//
// spec: §9.2 (Elicitation Chain), §8.5 (lenny/request_elicitation), §10.1
// (Horizontal Scaling), §7.3 (Retry and Resume).
package tier7a_load_local_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcp"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcptools"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/session/interactionstore"
	interactionpg "github.com/lennylabs/lenny/pkg/gateway/session/interactionstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// elicitXReplicaTimeout is the maxElicitationWait each replica applies. A
// waiter that only the timeout ends returns after it, so it is far above
// elicitXReplicaWakeBudget.
const elicitXReplicaTimeout = 20 * time.Second

// elicitXReplicaWakeBudget bounds the time from replica B's committed write
// to replica A's blocked call returning. A store poll wakes within a few
// poll intervals plus one Postgres round trip.
const elicitXReplicaWakeBudget = 2 * time.Second

// elicitXReplicaSessions is the number of elicitations raised and resolved
// concurrently in one run.
const elicitXReplicaSessions = 24

// elicitXReplicaTenant is the tenant every session belongs to.
const elicitXReplicaTenant = "acme"

// elicitResolution is how replica B ends one elicitation.
type elicitResolution int

const (
	elicitRespond elicitResolution = iota
	elicitDismiss
	elicitExpire
)

func (r elicitResolution) String() string {
	return [...]string{"respond", "dismiss", "expire"}[r]
}

// elicitReplica is one gateway replica's registration of the platform MCP
// tools.
type elicitReplica struct {
	handler http.Handler
}

// newElicitReplica registers the platform MCP tools for one replica over the
// shared stores. Every replica gets its own mcp.Server, so the only state
// the two replicas share is what a production deployment shares: the
// Postgres interaction store and the session rows.
func newElicitReplica(sessions sessionstore.Store, inter interactionstore.Store, idFn func() string) elicitReplica {
	srv := mcp.NewServer()
	mcptools.Register(srv, mcptools.Deps{
		Store:              sessions,
		Interactions:       inter,
		ElicitationTimeout: elicitXReplicaTimeout,
		IDFunc:             idFn,
		TenantID:           elicitXReplicaTenant,
	})
	return elicitReplica{handler: srv.Handler()}
}

// callTool invokes one MCP tool on the replica and returns the decoded
// tools/call result.
func (r elicitReplica) callTool(ctx context.Context, tool string, args any) (map[string]any, error) {
	argBytes, err := json.Marshal(args)
	if err != nil {
		return nil, err
	}
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"` + tool + `","arguments":` + string(argBytes) + `}}`
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader([]byte(body))).WithContext(ctx)
	rr := httptest.NewRecorder()
	r.handler.ServeHTTP(rr, req)
	var resp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		return nil, fmt.Errorf("decode %s response %q: %w", tool, rr.Body.String(), err)
	}
	result, _ := resp["result"].(map[string]any)
	if result == nil {
		return nil, fmt.Errorf("%s returned no result: %s", tool, rr.Body.String())
	}
	return result, nil
}

// resultText concatenates the text content blocks of a tools/call result.
func resultText(result map[string]any) string {
	var b strings.Builder
	content, _ := result["content"].([]any)
	for _, raw := range content {
		block, _ := raw.(map[string]any)
		if s, ok := block["text"].(string); ok {
			b.WriteString(s)
		}
	}
	return b.String()
}

// elicitWait is the outcome of one blocked request_elicitation on replica A.
type elicitWait struct {
	result   map[string]any
	err      error
	returned time.Time
}

// spec: §9.2 (Elicitation Chain), §8.5 (lenny/request_elicitation), §10.1 (Horizontal Scaling), §7.3 (Retry and Resume)
// diagnosis: a failure means a lenny/request_elicitation call blocked on one
// replica did not wake promptly when the elicitation was answered,
// dismissed, or expired through another replica, which shares only the
// Postgres interaction store with it. Either the waiter does not read the
// shared store (it waits for an in-process signal that the other replica
// never sends, and returns only at maxElicitationWait), or it read the
// resolution but returned the wrong outcome for it. Under -race it also
// catches a data race between the poll loop and the resolving writer.
func TestElicitationAnsweredOnAnotherReplicaWakesTheBlockedCall_spec_9_2(t *testing.T) {
	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: schematest.RepoRoot(t) + "/migrations",
	})
	ctx := context.Background()
	if _, err := pg.Pool.Exec(ctx, `INSERT INTO tenants (id, genesis_nonce) VALUES ($1, $2)`,
		elicitXReplicaTenant, []byte{0x01}); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	// Each replica opens its own store handle over the one database, as
	// each production replica builds its own pgstore over its own pool.
	interA := interactionpg.New(pg.Pool)
	interB := interactionpg.New(pg.Pool)
	sessions := memstore.New()

	// The elicitation IDs are assigned per session through the call's
	// elicitationId argument, so the replicas' ID functions are never used.
	idFn := func() string { return "unused" }
	replicaA := newElicitReplica(sessions, interA, idFn)
	replicaB := newElicitReplica(sessions, interB, idFn)

	rng := rand.New(rand.NewPCG(uint64(time.Now().UnixNano()), 85))
	resolutions := make([]elicitResolution, elicitXReplicaSessions)
	for i := range resolutions {
		resolutions[i] = elicitResolution(i % 3)
	}
	rng.Shuffle(len(resolutions), func(i, j int) { resolutions[i], resolutions[j] = resolutions[j], resolutions[i] })

	now := time.Now()
	for i := range elicitXReplicaSessions {
		if err := sessions.Create(ctx, sessionstore.Session{
			ID: elicitSessionID(i), TenantID: elicitXReplicaTenant, UserID: "alice",
			State: session.StateRunning, CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			t.Fatalf("seed session %d: %v", i, err)
		}
	}

	// Raise every elicitation on replica A. Each call blocks in A's waiter.
	waits := make([]chan elicitWait, elicitXReplicaSessions)
	for i := range elicitXReplicaSessions {
		waits[i] = make(chan elicitWait, 1)
		go func(i int) {
			res, err := replicaA.callTool(ctx, "lenny/request_elicitation", map[string]any{
				"sessionId":     elicitSessionID(i),
				"elicitationId": elicitID(i),
				"message":       fmt.Sprintf("approve deploy %d?", i),
				"schema":        map[string]any{"type": "object"},
			})
			waits[i] <- elicitWait{result: res, err: err, returned: time.Now()}
		}(i)
	}
	for i := range elicitXReplicaSessions {
		waitPendingElicitation(t, interB, i)
	}

	// Resolve on replica B in a random order with random gaps, so the
	// writes land at arbitrary points in A's poll cycles.
	order := rng.Perm(elicitXReplicaSessions)
	committed := make([]time.Time, elicitXReplicaSessions)
	for _, i := range order {
		time.Sleep(time.Duration(rng.IntN(15)) * time.Millisecond)
		committed[i] = resolveOnReplicaB(t, ctx, replicaB, interB, i, resolutions[i])
	}

	latencies := make([]time.Duration, 0, elicitXReplicaSessions)
	for i := range elicitXReplicaSessions {
		var w elicitWait
		select {
		case w = <-waits[i]:
		case <-time.After(elicitXReplicaTimeout + 5*time.Second):
			t.Fatalf("session %d: request_elicitation on replica A never returned after the %s on replica B", i, resolutions[i])
		}
		if w.err != nil {
			t.Errorf("session %d: request_elicitation transport error: %v", i, w.err)
			continue
		}
		lat := w.returned.Sub(committed[i])
		latencies = append(latencies, lat)
		if lat > elicitXReplicaWakeBudget {
			t.Errorf("session %d (%s): replica A returned %s after replica B committed, want < %s (the waiter woke only on its timeout)",
				i, resolutions[i], lat.Round(time.Millisecond), elicitXReplicaWakeBudget)
		}
		requireElicitOutcome(t, i, resolutions[i], w.result)
	}
	logWakeLatencies(t, latencies)
}

// resolveOnReplicaB ends elicitation i through replica B and returns the
// time its write committed. Respond and dismiss go through B's own MCP
// tools; expire runs the store expiry the session's departure from its pod
// runs on whichever replica drives that departure.
func resolveOnReplicaB(t *testing.T, ctx context.Context, b elicitReplica, inter interactionstore.Store, i int, how elicitResolution) time.Time {
	t.Helper()
	switch how {
	case elicitRespond, elicitDismiss:
		tool, args := "lenny/respond_to_elicitation", map[string]any{
			"sessionId": elicitSessionID(i), "elicitationId": elicitID(i),
			"response": map[string]any{"answer": fmt.Sprintf("yes-%d", i)},
		}
		if how == elicitDismiss {
			tool, args = "lenny/dismiss_elicitation", map[string]any{
				"sessionId": elicitSessionID(i), "elicitationId": elicitID(i), "reason": "not now",
			}
		}
		// The MCP resolution tools key the (tenant, session, user,
		// elicitation) triple on the authenticated caller, so the owner's
		// principal rides the call. spec: §9.2.
		owner := authmw.WithPrincipal(ctx, authmw.Principal{Subject: "alice", TenantID: elicitXReplicaTenant})
		res, err := b.callTool(owner, tool, args)
		at := time.Now()
		if err != nil || res["isError"] == true {
			t.Fatalf("session %d: %s on replica B = %v, %v", i, tool, resultText(res), err)
		}
		return at
	case elicitExpire:
		n, err := inter.ExpireRaisedBy(ctx, elicitXReplicaTenant, elicitSessionID(i))
		at := time.Now()
		if err != nil || n != 1 {
			t.Fatalf("session %d: ExpireRaisedBy on replica B = (%d, %v), want (1, nil)", i, n, err)
		}
		return at
	}
	t.Fatalf("unknown resolution %d", how)
	return time.Time{}
}

// requireElicitOutcome asserts replica A's blocked call returned the
// outcome replica B's resolution decided.
func requireElicitOutcome(t *testing.T, i int, how elicitResolution, result map[string]any) {
	t.Helper()
	text := resultText(result)
	switch how {
	case elicitRespond:
		if result["isError"] == true || !strings.Contains(text, fmt.Sprintf("yes-%d", i)) {
			t.Errorf("session %d: request_elicitation = %q, want the response replica B recorded", i, text)
		}
	case elicitDismiss:
		if result["isError"] == true || !strings.Contains(text, `"dismissed":true`) {
			t.Errorf("session %d: request_elicitation = %q, want the dismissal replica B recorded", i, text)
		}
	case elicitExpire:
		// spec: §9.2 — the runtime waiting on an elicitation its session's
		// departure expired gets an error rather than an answer.
		if result["isError"] != true || strings.Contains(text, "ELICITATION_TIMEOUT") {
			t.Errorf("session %d: request_elicitation after expiry = %q (isError=%v), want an error other than the timeout", i, text, result["isError"])
		}
	}
}

// waitPendingElicitation blocks until replica A has recorded elicitation i
// as pending in the shared store.
func waitPendingElicitation(t *testing.T, inter interactionstore.Store, i int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		got, err := inter.Get(context.Background(), elicitXReplicaTenant, elicitSessionID(i), "alice", elicitID(i))
		if err == nil && got.Phase == interactionstore.PhasePending {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("elicitation %d was never recorded as pending", i)
}

// logWakeLatencies logs the median and maximum wake latency.
func logWakeLatencies(t *testing.T, lat []time.Duration) {
	t.Helper()
	if len(lat) == 0 {
		return
	}
	sorted := append([]time.Duration(nil), lat...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	t.Logf("cross-replica wake latency over %d elicitations: p50=%s max=%s",
		len(sorted), sorted[len(sorted)/2].Round(time.Microsecond), sorted[len(sorted)-1].Round(time.Microsecond))
}

func elicitSessionID(i int) string { return fmt.Sprintf("sess-x%02d", i) }

func elicitID(i int) string { return fmt.Sprintf("elic-x%02d", i) }
