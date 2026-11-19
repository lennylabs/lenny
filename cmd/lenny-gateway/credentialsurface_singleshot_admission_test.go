// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"errors"
	"net/http"
	"testing"

	pkgauth "github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/authmw"
	"github.com/lennylabs/lenny/pkg/gateway/core/session/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/environment/translator"
	"github.com/lennylabs/lenny/pkg/runtime/runtimecapoverride"
	"github.com/lennylabs/lenny/pkg/runtime/runtimestore"
	"github.com/lennylabs/lenny/pkg/session/sessionstore/memstore"
)

// downStores is a DualStoreGate that reports both coordination stores
// unreachable while down is set.
type downStores struct{ down bool }

func (d downStores) Unavailable() bool { return d.down }

// singleShotBind runs BindSingleShot for the echo runtime in tenant acme as
// an acme principal with roles, over a session server built with opts, and
// returns the binding and the mapped single-shot error.
func singleShotBind(t *testing.T, opts sessionserver.Options, roles ...pkgauth.Role) (translator.SingleShotBinding, *translator.SingleShotError, error) {
	t.Helper()
	opts.DevTenantHeader = true
	srv := sessionserver.New(memstore.New(), opts)
	t.Cleanup(srv.Close)
	ctx := authmw.WithPrincipal(context.Background(),
		authmw.Principal{Subject: "carol@acme.com", TenantID: "acme", Roles: roles})
	binding, err := sessionSingleShotBinder{srv: srv, runtimes: runtimestore.NewMemory(), capOverrides: runtimecapoverride.NewMemory()}.
		BindSingleShot(ctx, translator.SingleShotSpec{TenantID: "acme", RuntimeRef: "echo"})
	var ssErr *translator.SingleShotError
	if err != nil && !errors.As(err, &ssErr) {
		t.Fatalf("err = %v, want *translator.SingleShotError", err)
	}
	return binding, ssErr, err
}

// TestSingleShotBinderFollowsTheCreatePermissionRow pins that the
// single-shot path refuses a tenant-viewer and a billing-viewer with a 403
// FORBIDDEN single-shot error and binds no session, and binds a session for
// a user.
//
// spec: §10.2 (Authorization and RBAC) Session creation surfaces, §15 (Built-in adapter single-shot compute model)
func TestSingleShotBinderFollowsTheCreatePermissionRow(t *testing.T) {
	for _, role := range []pkgauth.Role{pkgauth.RoleTenantViewer, pkgauth.RoleBillingViewer} {
		t.Run(string(role), func(t *testing.T) {
			binding, ssErr, err := singleShotBind(t, sessionserver.Options{}, role)
			if err == nil {
				t.Fatalf("the single-shot path bound session %q for %s; want 403 FORBIDDEN", binding.SessionID, role)
			}
			if ssErr.HTTPStatus != http.StatusForbidden || ssErr.Code != "FORBIDDEN" || ssErr.Retryable {
				t.Errorf("mapped error = %+v, want a non-retryable 403 FORBIDDEN", ssErr)
			}
			if binding.SessionID != "" {
				t.Errorf("binding = %+v, want empty on refusal", binding)
			}
		})
	}
	t.Run(string(pkgauth.RoleUser), func(t *testing.T) {
		binding, _, err := singleShotBind(t, sessionserver.Options{}, pkgauth.RoleUser)
		if err != nil {
			t.Fatalf("the single-shot path refused a user: %v", err)
		}
		if binding.SessionID == "" {
			t.Error("the single-shot path bound no session for a user")
		}
	})
}

// TestSingleShotBinderRefusesWhileBothStoresAreUnavailable pins that the
// single-shot path refuses a create while both coordination stores are
// unreachable with a retryable 503 PLATFORM_DEGRADED single-shot error that
// carries the ten-second Retry-After the adapter re-emits.
//
// spec: §10.1 (Dual-Store Unavailability) item 2, §15 (Built-in adapter single-shot compute model)
func TestSingleShotBinderRefusesWhileBothStoresAreUnavailable(t *testing.T) {
	binding, ssErr, err := singleShotBind(t, sessionserver.Options{DualStore: downStores{down: true}}, pkgauth.RoleUser)
	if err == nil {
		t.Fatalf("the single-shot path bound session %q while both stores are unavailable", binding.SessionID)
	}
	if ssErr.HTTPStatus != http.StatusServiceUnavailable || ssErr.Code != "PLATFORM_DEGRADED" {
		t.Fatalf("mapped error = %+v, want 503 PLATFORM_DEGRADED", ssErr)
	}
	if !ssErr.Retryable || ssErr.RetryAfterSeconds != 10 {
		t.Errorf("mapped error = %+v, want retryable with a ten-second Retry-After", ssErr)
	}
}
