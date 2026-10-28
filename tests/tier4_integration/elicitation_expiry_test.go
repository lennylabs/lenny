// SPDX-License-Identifier: MIT

//go:build integration

package tier4_integration_test

import (
	"context"
	"net/http"
	"testing"

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

// restErrorCode reads the error code of a decoded gateway error body, which
// carries it as error.code.
func restErrorCode(body map[string]any) string {
	e, _ := body["error"].(map[string]any)
	code, _ := e["code"].(string)
	return code
}
