// SPDX-License-Identifier: MIT

package mcptools_test

import (
	"context"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	pkgauth "github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/authmw"
	"github.com/lennylabs/lenny/pkg/gateway/core/session/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/core/session/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/platformtools/mcp"
	"github.com/lennylabs/lenny/pkg/gateway/platformtools/mcptools"
	"github.com/lennylabs/lenny/pkg/runtime/poolstore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
	"github.com/lennylabs/lenny/pkg/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/tests/testinfra/apifront"
)

// createToolNames are the MCP tools that create a session.
var createToolNames = []string{"lenny/create_session", "lenny/create_and_start_session"}

// createdSessionID is the ID the session server assigns every create in
// these tests.
const createdSessionID = "sess_mcp_create"

// stores is a DualStoreGate that reports both coordination stores
// unreachable while down is set.
type stores struct{ down bool }

func (s stores) Unavailable() bool { return s.down }

// createToolsFixture registers the MCP tools over a session server built
// with opts, wired through the API front so lenny/create_and_start_session
// reaches the REST route through the request pipeline, as the composition
// root wires it, and lenny/create_session reaches the in-process creation
// entry point through GWSEAM-CreateSession.
func createToolsFixture(t *testing.T, opts sessionserver.Options) ownershipFixture {
	t.Helper()
	store := memstore.New()
	opts.DevTenantHeader = true
	opts.IDFunc = func() string { return createdSessionID }
	opts.Clock = func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }
	srv := sessionserver.New(store, opts)
	t.Cleanup(srv.Close)
	apifront.Pipeline(srv)
	tools := srv.ToolsSeam()
	mcpSrv := mcp.NewServer()
	mcptools.Register(mcpSrv, mcptools.Deps{
		SessionCreator:    tools,
		SessionService:    tools,
		SessionAccess:     tools,
		Store:             store,
		DevTenantFallback: true,
		TenantID:          "acme",
	})
	return ownershipFixture{srv: mcpSrv, store: store}
}

// createdSession reports whether the create under test wrote its row.
func (f ownershipFixture) createdSession() (sessionstore.Session, bool) {
	row, err := f.store.Get(context.Background(), "acme", createdSessionID)
	return row, err == nil
}

// caller returns an acme principal for subject with roles.
func caller(subject string, roles ...pkgauth.Role) authmw.Principal {
	return authmw.Principal{Subject: subject, TenantID: "acme", Roles: roles}
}

// drainingPoolOptions returns session-server options under which the echo
// runtime resolves to the service-mode warm pool echo-pool, which the pool
// registry records as draining.
func drainingPoolOptions(t *testing.T) sessionserver.Options {
	t.Helper()
	const ns = "lenny-agents"
	scheme := runtime.NewScheme()
	if err := lennyv1.AddToScheme(scheme); err != nil {
		t.Fatalf("add scheme: %v", err)
	}
	cluster := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&lennyv1.PodSet{
		ObjectMeta: metav1.ObjectMeta{Name: "echo-pool", Namespace: ns},
		Spec: lennyv1.PodSetSpec{
			RuntimeRef: "echo", IsolationProfile: string(isolation.ProfileSandboxed),
			ExecutionMode: "service", MinWarm: 1, MaxWarm: 5,
		},
	}).Build()
	pools := poolstore.NewMemory()
	ctx := context.Background()
	if err := pools.Create(ctx, poolstore.Pool{Name: "echo-pool", RuntimeRef: "echo", MaxSessionAgeSeconds: 3600}); err != nil {
		t.Fatalf("create pool: %v", err)
	}
	if _, err := pools.Update(ctx, "echo-pool", func(p *poolstore.Pool) error {
		p.DrainingSince = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Add(-time.Minute)
		return nil
	}); err != nil {
		t.Fatalf("drain pool: %v", err)
	}
	return sessionserver.Options{
		Pools:                   pools,
		PodBinder:               &podsession.Binder{Client: cluster, Namespace: ns},
		AgentNamespace:          ns,
		DefaultIsolationProfile: isolation.ProfileSandboxed,
	}
}

// TestMCPCreateToolsFollowTheCreatePermissionRow pins that both MCP create
// tools refuse a tenant-viewer and a billing-viewer with FORBIDDEN and create
// no session, and admit a user and a tenant-admin, whose session they create
// owned by the caller.
//
// spec: §10.2 (Authorization and RBAC) Session creation surfaces, §15.2.1 (REST/MCP Consistency Contract)
func TestMCPCreateToolsFollowTheCreatePermissionRow(t *testing.T) {
	cases := []struct {
		role    pkgauth.Role
		allowed bool
	}{
		{pkgauth.RoleTenantViewer, false},
		{pkgauth.RoleBillingViewer, false},
		{pkgauth.RoleUser, true},
		{pkgauth.RoleTenantAdmin, true},
	}
	for _, tool := range createToolNames {
		for _, tc := range cases {
			t.Run(tool+"/"+string(tc.role), func(t *testing.T) {
				f := createToolsFixture(t, sessionserver.Options{})
				te := f.dispatchPrincipal(t, caller("carol@acme.com", tc.role), tool, `{"runtimeRef":"echo"}`)
				row, created := f.createdSession()
				if !tc.allowed {
					if te == nil || te.Code != "FORBIDDEN" {
						t.Fatalf("error = %+v, want FORBIDDEN", te)
					}
					if created {
						t.Error("a refused create left a session row")
					}
					return
				}
				if te != nil {
					t.Fatalf("the create was refused: %s %s", te.Code, te.Msg)
				}
				if !created {
					t.Fatal("the admitted create wrote no row")
				}
				if row.UserID != "carol@acme.com" {
					t.Errorf("owner = %q, want carol@acme.com", row.UserID)
				}
			})
		}
	}
}

// TestMCPCreateToolsRefuseWhileThePoolDrains pins that both MCP create
// tools refuse a create that would select a draining pool with
// POOL_DRAINING and create no session.
//
// spec: §15.1 (REST API) POOL_DRAINING, §15.2.1 (REST/MCP Consistency Contract)
func TestMCPCreateToolsRefuseWhileThePoolDrains(t *testing.T) {
	for _, tool := range createToolNames {
		t.Run(tool, func(t *testing.T) {
			f := createToolsFixture(t, drainingPoolOptions(t))
			te := f.dispatchPrincipal(t, caller("carol@acme.com", pkgauth.RoleUser), tool, `{"runtimeRef":"echo"}`)
			if te == nil || te.Code != "POOL_DRAINING" {
				t.Fatalf("error = %+v, want POOL_DRAINING", te)
			}
			if _, created := f.createdSession(); created {
				t.Error("a refused create left a session row")
			}
		})
	}
}

// TestMCPCreateToolsRefuseWhileBothStoresAreUnavailable pins that both MCP
// create tools refuse a create with PLATFORM_DEGRADED while both
// coordination stores are unreachable, and create no session.
//
// spec: §10.1 (Dual-Store Unavailability) item 2, §15.2.1 (REST/MCP Consistency Contract)
func TestMCPCreateToolsRefuseWhileBothStoresAreUnavailable(t *testing.T) {
	for _, tool := range createToolNames {
		t.Run(tool, func(t *testing.T) {
			f := createToolsFixture(t, sessionserver.Options{DualStore: stores{down: true}})
			te := f.dispatchPrincipal(t, caller("carol@acme.com", pkgauth.RoleUser), tool, `{"runtimeRef":"echo"}`)
			if te == nil || te.Code != "PLATFORM_DEGRADED" {
				t.Fatalf("error = %+v, want PLATFORM_DEGRADED", te)
			}
			if _, created := f.createdSession(); created {
				t.Error("a refused create left a session row")
			}
		})
	}
}
