// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"testing"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/billing/billingstore"
	"github.com/lennylabs/lenny/pkg/session/sessionstore/memstore"
)

// TestSessionCompletedAppendedAfterErasureCarriesNoUserID pins the
// session.completed billing event of the terminal pipeline: appended after
// user erasure has pseudonymized the user's billing events, it carries no
// user id, so no event in the store is keyed to the erased user's raw id and
// the verification count finds none. It keeps the tenant, the session id, the
// environment, the experiment and variant, and the labels, and the session's
// session.created event, which carries the pseudonym, joins it on session_id.
//
// spec: §11.2.1 (Billing Event Stream), §12.8 (Compliance Interfaces)
func TestSessionCompletedAppendedAfterErasureCarriesNoUserID(t *testing.T) {
	ctx := context.Background()
	billing := billingstore.NewMemory()
	srv := New(memstore.New(), Options{DevTenantHeader: true, Billing: billing})
	t.Cleanup(srv.Close)

	sess := enrolledSession(session.StateCreated)
	sess.Environment = "env-prod"
	sess.Labels = map[string]string{"team": "payments"}
	srv.recordSessionCreated(ctx, sess)
	if n, err := billing.PseudonymizeUser(ctx, "acme", sess.UserID, []byte("salt")); err != nil || n != 1 {
		t.Fatalf("pseudonymize = (%d, %v), want the session.created event rewritten", n, err)
	}

	sess.State = session.StateCompleted
	srv.recordSessionCompleted(ctx, session.StateRunning, sess)

	if n, err := billing.CountUser(ctx, "acme", sess.UserID); err != nil || n != 0 {
		t.Errorf("events keyed to the raw user id after the late append = (%d, %v), want 0", n, err)
	}
	events, err := billing.Since(ctx, "acme", 0, 0)
	if err != nil {
		t.Fatalf("Since: %v", err)
	}
	var created, completed *billingstore.Event
	for i := range events {
		switch events[i].EventType {
		case billingstore.EventSessionCreated:
			created = &events[i]
		case billingstore.EventSessionCompleted:
			completed = &events[i]
		}
	}
	if created == nil || completed == nil {
		t.Fatalf("billing events = %+v, want a session.created and a session.completed event", events)
	}
	if completed.UserID != "" {
		t.Errorf("session.completed user_id = %q, want empty", completed.UserID)
	}
	if completed.TenantID != "acme" || completed.SessionID != sess.ID || completed.EnvironmentID != "env-prod" ||
		completed.ExperimentID != "exp-checkout" || completed.VariantID != "treatment" || completed.Labels["team"] != "payments" {
		t.Errorf("session.completed = %+v, want the tenant, session, environment, experiment, variant, and labels kept", *completed)
	}
	if created.SessionID != completed.SessionID || created.UserID == "" || created.UserID == sess.UserID {
		t.Errorf("session.created = %+v, want the pseudonymized user on the same session_id", *created)
	}
}
