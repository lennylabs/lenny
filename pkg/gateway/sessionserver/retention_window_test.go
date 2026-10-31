// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/environment/environmentstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
)

// failingTenants is a tenant store whose Get fails with a non-not-found
// error.
type failingTenants struct{ tenantstore.Store }

func (failingTenants) Get(context.Context, string) (tenantstore.Tenant, error) {
	return tenantstore.Tenant{}, errors.New("tenant store unreachable")
}

// failingEnvironments is an environment store whose Get fails with a
// non-not-found error.
type failingEnvironments struct{ environmentstore.Store }

func (failingEnvironments) Get(context.Context, string, string) (environmentstore.Environment, error) {
	return environmentstore.Environment{}, errors.New("environment store unreachable")
}

// TestRetentionWindowReturnsTheTierWindowAndLookupErrors_spec_7_1 pins
// (*Server).RetentionWindow, the window the retention GC counts from
// TerminatedAt: it returns the tier window, a deployer window shorter than
// the terminal pipeline budget included, and the default window with no
// error for a tenant that does not exist. A tenant or environment Get that
// fails with any other error returns that error, so the GC leaves the row
// rather than fall back to a shorter default; on the pre-fix lookup the
// error was swallowed.
//
// spec: §7.1 (Artifact retention), §12.5 (Artifact Store)
func TestRetentionWindowReturnsTheTierWindowAndLookupErrors_spec_7_1(t *testing.T) {
	ctx := context.Background()
	tenants := tenantstore.NewMemory()
	for _, tn := range []tenantstore.Tenant{
		{ID: "acme", WorkspaceTier: tenantstore.WorkspaceTierT4},
		{ID: "globex", WorkspaceTier: tenantstore.WorkspaceTierT3},
	} {
		if err := tenants.Create(ctx, tn); err != nil {
			t.Fatalf("seed tenant %s: %v", tn.ID, err)
		}
	}
	short := time.Minute
	srv := sessionserver.New(memstore.New(), sessionserver.Options{
		DevTenantHeader: true, Tenants: tenants, Environments: environmentstore.NewMemory(), DefaultRetention: short,
	})
	for _, tc := range []struct {
		name    string
		sess    sessionstore.Session
		want    time.Duration
		wantErr bool
	}{
		{"T4 tier window", sessionstore.Session{UserID: testOwner, TenantID: "acme"}, 24 * time.Hour, false},
		{"deployer window shorter than the budget", sessionstore.Session{UserID: testOwner, TenantID: "globex"}, short, false},
		{"not-found tenant takes the default", sessionstore.Session{UserID: testOwner, TenantID: "ghost"}, short, false},
		{"not-found environment keeps the tenant tier", sessionstore.Session{UserID: testOwner, TenantID: "acme", Environment: "missing"}, 24 * time.Hour, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := srv.RetentionWindow(ctx, tc.sess)
			if got != tc.want || (err != nil) != tc.wantErr {
				t.Errorf("RetentionWindow = (%v, %v), want (%v, error %v)", got, err, tc.want, tc.wantErr)
			}
		})
	}

	failTenant := sessionserver.New(memstore.New(), sessionserver.Options{
		DevTenantHeader: true, Tenants: failingTenants{tenants}, Environments: environmentstore.NewMemory(),
	})
	if _, err := failTenant.RetentionWindow(ctx, sessionstore.Session{UserID: testOwner, TenantID: "acme"}); err == nil {
		t.Error("RetentionWindow with a failing tenant Get = nil error, want the lookup error")
	}
	failEnv := sessionserver.New(memstore.New(), sessionserver.Options{
		DevTenantHeader: true, Tenants: tenants, Environments: failingEnvironments{environmentstore.NewMemory()},
	})
	if _, err := failEnv.RetentionWindow(ctx, sessionstore.Session{UserID: testOwner, TenantID: "acme", Environment: "restricted"}); err == nil {
		t.Error("RetentionWindow with a failing environment Get = nil error, want the lookup error")
	}
}
