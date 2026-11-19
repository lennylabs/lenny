// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/auth/authmw"
	"github.com/lennylabs/lenny/pkg/gateway/core/session/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/environment/userstore"
	"github.com/lennylabs/lenny/pkg/gateway/seam"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

// TestAdmitActiveCallerAnswersTheUserLifecycleCheck checks the session
// core's side of the user lifecycle check the platform tools seam's routes
// call: a caller with no principal or no registered user is admitted, an
// invalidated user and one with a pending erasure are refused with their
// codes and the refused action, and a failed user read is an error naming
// the failed check.
//
// spec: GWSEAM-AdmitActiveCaller
func TestAdmitActiveCallerAnswersTheUserLifecycleCheck(t *testing.T) {
	ctx := context.Background()
	users := userstore.NewMemory()
	for _, u := range []userstore.User{
		{Subject: "disabled", TenantID: "acme", Disabled: true},
		{Subject: "erasing", TenantID: "acme", ProcessingRestricted: true, ErasureJobID: "job-1"},
		{Subject: "active", TenantID: "acme"},
	} {
		if err := users.Create(ctx, u); err != nil {
			t.Fatalf("seed %s: %v", u.Subject, err)
		}
	}
	core := sessionserver.New(memstore.New(), sessionserver.Options{Users: users}).ToolsSeam()
	as := func(subject string) context.Context {
		return authmw.WithPrincipal(ctx, authmw.Principal{Subject: subject, TenantID: "acme"})
	}
	if err := core.AdmitActiveCaller(ctx, "cannot resolve interactions"); err != nil {
		t.Errorf("no principal: %v, want admitted", err)
	}
	for _, subject := range []string{"active", "unregistered"} {
		if err := core.AdmitActiveCaller(as(subject), "cannot resolve interactions"); err != nil {
			t.Errorf("%s: %v, want admitted", subject, err)
		}
	}
	for subject, code := range map[string]string{"disabled": "USER_INVALIDATED", "erasing": "ERASURE_IN_PROGRESS"} {
		err := core.AdmitActiveCaller(as(subject), "cannot resolve interactions")
		ref, ok := seam.IsRefusal(err)
		if !ok || ref.Code != code || !strings.Contains(ref.Message, "cannot resolve interactions") {
			t.Errorf("%s: %v, want a %s refusal naming the action", subject, err, code)
		}
		if subject == "erasing" && ref != nil && ref.Details["jobId"] != "job-1" {
			t.Errorf("erasure refusal details %v, want the job id", ref.Details)
		}
	}

	failing := sessionserver.New(memstore.New(), sessionserver.Options{Users: failingUsers{userstore.NewMemory()}}).ToolsSeam()
	err := failing.AdmitActiveCaller(as("active"), "cannot resolve interactions")
	if _, refused := seam.IsRefusal(err); err == nil || refused || !strings.HasPrefix(err.Error(), "user-invalidation check failed: ") {
		t.Errorf("failed read: %v, want the failed check's error", err)
	}
}

// failingUsers is a user store whose reads fail.
type failingUsers struct{ userstore.Store }

func (failingUsers) Get(context.Context, string, string) (userstore.User, error) {
	return userstore.User{}, errors.New("user registry unavailable")
}
