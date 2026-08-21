// SPDX-License-Identifier: MIT

package podsession_test

import (
	"context"
	"testing"

	"github.com/lennylabs/lenny/pkg/adapter"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"

	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
)

// spec: 6.4 (per-slot workspace layout), 7.3 (workspace-root assertion),
// 15.5 (version handshake)
// diagnosis: a bind path put a derived value on the workspace member the
// handshake reports. The member carries the adapter's workspace base
// verbatim and the gateway derives `<base>/slots/{sessionId}/current` once,
// where the value is persisted. A member already holding a derived root
// makes that persist write `<base>/slots/{id}/current/slots/{id}/current`,
// which the adapter's §7.3 step (d) guard rejects on every resume.
func TestBindPathsCarryReportedWorkspaceBaseVerbatim_spec_6_4(t *testing.T) {
	rt := &fakeRuntime{}
	srv := adapter.New("adapter-test")
	base := t.TempDir()
	srv.WorkspaceBase = base
	srv.Runtime = rt

	c := k8sClient(t, idleSandbox("sbx-1", "10.244.1.7"))
	binder := newBinder(c, adapterDialer(t, srv))

	req := podsession.BindRequest{
		Pool: testPool, SessionID: "sess-1", TenantID: "acme", Runtime: "claude-code",
	}
	claim, err := binder.Claim(context.Background(), req)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if claim.WorkspaceBase != base {
		t.Errorf("ClaimResult.WorkspaceBase = %q, want the reported base %q", claim.WorkspaceBase, base)
	}
	req.SandboxName = claim.SandboxName
	prep, err := binder.Prepare(context.Background(), req)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if prep.WorkspaceBase != base {
		t.Errorf("PrepareResult.WorkspaceBase = %q, want the reported base %q", prep.WorkspaceBase, base)
	}
	res, err := binder.Launch(context.Background(), req)
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	defer res.Adapter.Close()
	if res.WorkspaceBase != base {
		t.Errorf("BindResult.WorkspaceBase = %q, want the reported base %q", res.WorkspaceBase, base)
	}
}

// spec: 5.2 (concurrent-workspace slot), 6.4 (per-slot workspace layout),
// 7.3 (workspace-root assertion)
// diagnosis: the concurrent bind reported no workspace value, so a session
// on a `maxConcurrentSessions > 1` pool persisted no workspace root at
// start and the §7.3 step (d) assertion was skipped for the whole pool on
// resume. The slot bind carries the same reported base the base-mode bind
// carries, on the same member, so the one persist site derives the slot
// root for both paths.
func TestBindSlotCarriesReportedWorkspaceBase_spec_5_2(t *testing.T) {
	a := newConcurrentAdapter()
	a.workspaceBase = "/workspace"
	c := k8sClient(t, concurrentIdleSandbox("sbx-1", "10.244.1.7"))
	binder := newSlotBinder(t, c, concurrentAdapterDialer(t, a))

	res, err := binder.BindSlot(context.Background(), podsession.SlotBindRequest{
		Pool: testPool, SessionID: "sess-1", TenantID: "acme", Runtime: "claude-code",
		MaxConcurrentSessions: 8,
		Plan:                  &adapterv1.WorkspacePlan{},
	})
	if err != nil {
		t.Fatalf("BindSlot: %v", err)
	}
	defer res.Adapter.Close()
	if res.WorkspaceBase != "/workspace" {
		t.Errorf("BindResult.WorkspaceBase = %q, want the reported base /workspace", res.WorkspaceBase)
	}
}
