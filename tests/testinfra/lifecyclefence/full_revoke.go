// SPDX-License-Identifier: MIT

package lifecyclefence

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/environment/userstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// commitHookUsers is the user store the admin router writes through. After
// each committed Update it runs the hook armed for that user, so a test can
// cancel a full_revoke request right after the user row commits, as a
// client that disconnects there does.
type commitHookUsers struct {
	userstore.Store
	w *World
}

// Update implements userstore.Store.
func (u commitHookUsers) Update(ctx context.Context, tenantID, subject string, mutate func(*userstore.User) error) (userstore.User, error) {
	row, err := u.Store.Update(ctx, tenantID, subject, mutate)
	if err == nil {
		if hook := u.w.takeUserCommitHook(subject); hook != nil {
			hook()
		}
	}
	return row, err
}

// takeUserCommitHook returns and disarms the commit hook of subject.
func (w *World) takeUserCommitHook(subject string) func() {
	w.mu.Lock()
	defer w.mu.Unlock()
	fn := w.userCommitHooks[subject]
	delete(w.userCommitHooks, subject)
	return fn
}

// armUserCommitHook runs fn once, right after the next committed update of
// subject's user row.
func (w *World) armUserCommitHook(subject string, fn func()) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.userCommitHooks == nil {
		w.userCommitHooks = map[string]func(){}
	}
	w.userCommitHooks[subject] = fn
}

// FullRevokeResult is a full_revoke request's answer.
type FullRevokeResult struct {
	Status         int
	Body           string
	FailedSessions []string
	LeasesRevoked  int
}

// FullRevoke issues POST /v1/admin/users/{user}/invalidate with mode
// full_revoke through the production handler as a platform admin. When
// disconnect is set, the request's context is cancelled right after the
// user row commits. On a 200 it asserts the §11.4 outcome: every session of
// the user committed before the call is terminal or gone, no session row of
// the user holds a lease unless a silent lease-store fault swallowed the
// lease step, and no cancel write failed. A 404 is accepted only once the tenant's deletion has
// erased the user row.
//
// spec: §11.4 (User Invalidation: Full revoke propagation mechanism)
func (w *World) FullRevoke(ctx context.Context, user string, disconnect bool) FullRevokeResult {
	w.markTouched(user)
	start := w.next()
	silentBefore := w.Leases.SilentFired()
	reqCtx := ctx
	if disconnect {
		var cancel context.CancelFunc
		reqCtx, cancel = context.WithCancel(ctx)
		defer cancel()
		w.armUserCommitHook(user, cancel)
	}
	body := `{"mode":"full_revoke","tenantId":"` + w.Tenant + `","reason":"lifecyclefence"}`
	rr := w.serveAdminBody(reqCtx, "/v1/admin/users/"+user+"/invalidate", strings.NewReader(body))
	res := FullRevokeResult{Status: rr.Code, Body: rr.Body.String()}
	switch {
	case rr.Code == http.StatusNotFound && w.Phase4Done():
		return res
	case rr.Code != http.StatusOK:
		w.Violatef("full_revoke of %s answered %d: %s", user, rr.Code, res.Body)
		return res
	}
	var out struct {
		LeasesRevoked  int `json:"leasesRevoked"`
		FailedSessions []struct {
			SessionID string `json:"sessionId"`
		} `json:"failedSessions"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		w.Violatef("full_revoke of %s: decode %q: %v", user, res.Body, err)
		return res
	}
	res.LeasesRevoked = out.LeasesRevoked
	for _, f := range out.FailedSessions {
		res.FailedSessions = append(res.FailedSessions, f.SessionID)
	}
	if len(res.FailedSessions) > 0 {
		w.Violatef("full_revoke of %s reported failed cancel writes %v with no fault armed", user, res.FailedSessions)
	}
	w.assertRevoked(context.WithoutCancel(ctx), user, start, silentBefore)
	return res
}

// assertRevoked records a violation for each session of user committed
// before the sequence number start that is still live, and for any lease a
// session row of the user still holds when no silent lease fault fired
// during the call.
func (w *World) assertRevoked(ctx context.Context, user string, start int64, silentBefore int) {
	for _, id := range w.sessionsOfBefore(user, start) {
		row, err := w.S.Sessions.Get(ctx, w.Tenant, id)
		switch {
		case errors.Is(err, sessionstore.ErrNotFound):
		case err != nil:
			w.Violatef("full_revoke of %s: read session %s: %v", user, id, err)
		case !session.IsTerminal(row.State):
			w.Violatef("full_revoke of %s answered 200 and left session %s %s", user, id, row.State)
		}
	}
	if held := w.reachableLeases(ctx, user); len(held) > 0 && w.Leases.SilentFired() == silentBefore {
		w.Violatef("full_revoke of %s answered 200 and the user still holds leases %v", user, held)
	}
}

// sessionsOfBefore returns the seeded live sessions of user and the
// admitted creates of user whose insert committed before start.
func (w *World) sessionsOfBefore(user string, start int64) []string {
	ids := w.SeededLive(user)
	for _, o := range w.Outcomes() {
		if o.Owner == user && o.Err == nil && o.CommittedSeq > 0 && o.CommittedSeq < start {
			ids = append(ids, o.ID)
		}
	}
	return ids
}
