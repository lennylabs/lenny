// SPDX-License-Identifier: MIT

//go:build integration

package tier4_integration_test

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/interactionstore"
	interactionpg "github.com/lennylabs/lenny/pkg/gateway/session/interactionstore/pgstore"
)

// elicitationExpiryStore starts Postgres with the production migrations,
// seeds the fixture tenant, and returns the Postgres interaction store.
func elicitationExpiryStore(t *testing.T) interactionstore.Store {
	t.Helper()
	pg := sharedDatabase(t)
	if _, err := pg.Pool.Exec(context.Background(),
		`INSERT INTO tenants (id, genesis_nonce) VALUES ($1, $2)`, "acme", []byte{0x01}); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	return interactionpg.New(pg.Pool)
}

// spec: 9.2 (Elicitation Chain), 7.3 (Retry and Resume), 28.5.1
// (Gateway-to-pod)
// diagnosis: a session whose runtime hung left its pod through the failure
// edge into resume_pending, and the elicitation its pod raised was still
// answerable afterwards, or an elicitation a child raised that the chain
// holds for the session was expired with it. A client could then answer an
// elicitation whose runtime is gone, and the answer would reach no one; or a
// child still waiting on its answer would never receive one.
func TestFailureEdgeExpiresTheElicitationsTheSessionRaised_spec_9_2(t *testing.T) {
	t.Parallel()
	interactions := elicitationExpiryStore(t)
	rt := newHeartbeatRuntime()
	const id = "sess-elicit"
	f := newStreamFailureFixture(t, rt, id, session.StateRunning, 0, streamFailureReplica{interactions: interactions})

	ctx := context.Background()
	for _, in := range []interactionstore.Interaction{
		{ID: "raised", Kind: interactionstore.KindElicitation, SessionID: id, TenantID: "acme", Detail: map[string]any{interactionstore.DetailOriginPod: id}},
		{ID: "raised-dismiss", Kind: interactionstore.KindElicitation, SessionID: id, TenantID: "acme", Detail: map[string]any{interactionstore.DetailOriginPod: id}},
		{ID: "child-held", Kind: interactionstore.KindElicitation, SessionID: id, TenantID: "acme", Detail: map[string]any{interactionstore.DetailOriginPod: "sess-child"}},
	} {
		if err := interactions.Put(ctx, in); err != nil {
			t.Fatalf("Put %s: %v", in.ID, err)
		}
	}

	if code, resp := f.post(t, "turn-1"); code != http.StatusOK {
		t.Fatalf("first message: status %d, body %v", code, resp)
	}
	rt.stopAcking()
	f.awaitState(t, session.StateResumePending)
	awaitElicitationsExpired(t, interactions, id, "raised", "raised-dismiss")

	base := f.base + "/v1/sessions/" + id + "/elicitations/"
	if code, resp := multiTurnPost(t, base+"raised/respond", `{"response":{"ok":true}}`); code != http.StatusNotFound || restErrorCode(resp) != "ELICITATION_NOT_FOUND" {
		t.Errorf("respond to the raised elicitation = %d %v, want 404 ELICITATION_NOT_FOUND", code, resp)
	}
	if code, resp := multiTurnPost(t, base+"raised-dismiss/dismiss", `{}`); code != http.StatusNotFound || restErrorCode(resp) != "ELICITATION_NOT_FOUND" {
		t.Errorf("dismiss of the raised elicitation = %d %v, want 404 ELICITATION_NOT_FOUND", code, resp)
	}
	held, err := interactions.Get(ctx, "acme", id, "", "child-held")
	if err != nil || held.Phase != interactionstore.PhasePending {
		t.Errorf("the child's elicitation held for the session = (%v, %v), want pending", held.Phase, err)
	}
}

// awaitElicitationsExpired waits, bounded by streamFailureWait, until every
// named elicitation of the session is gone from the interaction store.
//
// The gateway commits the failure edge's state write and then expires the
// elicitations the session raised as a separate step (afterRecoveryEdge in
// pkg/gateway/sessionserver/failure.go), so a reader can observe
// resume_pending before the expiry has run. §9.2 states the expiry as a
// consequence of the committed failure rather than as part of the state
// write: a resolution recorded before the expiry is returned to the runtime
// as for any pending elicitation, and an elicitation the expiry misses stays
// pending. The wait reads the store rather than calling respond or dismiss,
// because a respond that lands before the expiry resolves the elicitation,
// and the expiry, which deletes only pending rows, would then leave it.
//
// spec: 9.2 (Elicitation Chain) **Expiry when the session leaves its pod:**
func awaitElicitationsExpired(t *testing.T, store interactionstore.Store, sessionID string, ids ...string) {
	t.Helper()
	deadline := time.Now().Add(streamFailureWait)
	for _, eid := range ids {
		for {
			_, err := store.Get(context.Background(), "acme", sessionID, "", eid)
			if errors.Is(err, interactionstore.ErrNotFound) {
				break
			}
			if err != nil {
				t.Fatalf("read elicitation %s: %v", eid, err)
			}
			if time.Now().After(deadline) {
				t.Fatalf("elicitation %s of session %s still pending %s after the failure edge, want expired",
					eid, sessionID, streamFailureWait)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
}

// restErrorCode reads the error code of a decoded gateway error body, which
// carries it as error.code.
func restErrorCode(body map[string]any) string {
	e, _ := body["error"].(map[string]any)
	code, _ := e["code"].(string)
	return code
}
