// SPDX-License-Identifier: MIT

package pipeline

import (
	"context"
	"testing"

	"github.com/lennylabs/lenny/pkg/auth/authmw"
	"github.com/lennylabs/lenny/pkg/gateway/seam"
)

// TestRequestCallerReadsThePrincipal checks that the caller identity is the
// context's principal, copied field by field, and that a context with no
// principal names no caller.
// spec: GWSEAM-RequestCaller
func TestRequestCallerReadsThePrincipal(t *testing.T) {
	if _, ok := (Callers{}).RequestCaller(context.Background()); ok {
		t.Fatal("a context with no principal resolved a caller")
	}
	groups := []string{"eng"}
	ctx := authmw.WithPrincipal(context.Background(), authmw.Principal{
		Subject: "alice", TenantID: "acme", SessionID: "sess_1", CallerType: "agent", Groups: groups,
		JTI: "jti-1", Origin: "playground",
	})
	got, ok := (Callers{}).RequestCaller(ctx)
	want := seam.Caller{Subject: "alice", TenantID: "acme", SessionID: "sess_1", CallerType: "agent", Groups: []string{"eng"}}
	if !ok || got.Subject != want.Subject || got.TenantID != want.TenantID || got.SessionID != want.SessionID ||
		got.CallerType != want.CallerType || len(got.Groups) != 1 || got.Groups[0] != "eng" ||
		got.JTI != "jti-1" || got.Origin != "playground" {
		t.Fatalf("RequestCaller = %+v, %v; want %+v, true", got, ok, want)
	}
	got.Groups[0] = "changed"
	if groups[0] != "eng" {
		t.Error("the caller shares its group slice with the principal")
	}
}
