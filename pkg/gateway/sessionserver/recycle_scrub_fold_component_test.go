// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	"github.com/lennylabs/lenny/pkg/adapter"
	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/adapterclient"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/poolstore"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

// recordingRecycleAdapter is a raw gRPC adapter fake that captures every
// ShutdownRequest so the fold test can observe the exact §4.7 recycle
// disposition (RecycleScrub sub-message) the gateway delivers on release.
type recordingRecycleAdapter struct {
	adapterv1.UnimplementedAdapterServer
	mu   sync.Mutex
	reqs []*adapterv1.ShutdownRequest
}

func (a *recordingRecycleAdapter) Shutdown(_ context.Context, req *adapterv1.ShutdownRequest) (*adapterv1.ShutdownResponse, error) {
	a.mu.Lock()
	a.reqs = append(a.reqs, req)
	a.mu.Unlock()
	return &adapterv1.ShutdownResponse{ExitedCleanly: true}, nil
}

func (a *recordingRecycleAdapter) lastShutdown() *adapterv1.ShutdownRequest {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.reqs) == 0 {
		return nil
	}
	return a.reqs[len(a.reqs)-1]
}

func dialRecycleRecorderClient(t *testing.T, a *recordingRecycleAdapter) *adapterclient.Client {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	gs := grpc.NewServer()
	adapterv1.RegisterAdapterServer(gs, a)
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)
	cl, err := adapterclient.Dial("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial recycle recorder: %v", err)
	}
	return cl
}

// noopRecycleBoundary satisfies podsession.RecycleBoundaryArmer without a
// live coordinator, so the recycle-release path can arm and return.
type noopRecycleBoundary struct{ armed []string }

func (n *noopRecycleBoundary) OnRecycling(podID string) { n.armed = append(n.armed, podID) }

// spec: 5.2 (whole-pod scrub trigger, poolstore sessionPolicy scrub config
// delivered on the recycle Shutdown), 4.6.3 (poolstore ownership of the
// cleanup config), 3.4 (recycle disposition)
// diagnosis: the §5.2 whole-pod scrub cleanup configuration did not fold
// end-to-end from the poolstore sessionPolicy through the gateway-enforced
// mirror, ResolvePool's PoolMatch, and the session-mode BindResult to the
// §4.7 recycle-trigger Shutdown. A failure means a recycling pod reaching
// occupancy zero was signaled without its deployer cleanup commands (or with
// the wrong pod_id), so the adapter's whole-pod scrub cannot run the pool's
// cleanup and ReportPodScrub cannot cancel the missing-report timeout keyed
// on the pod name.
func TestRecycleScrubConfigFoldsEndToEnd_spec_5_2(t *testing.T) {
	rt := &podBindRuntime{}
	adapterSrv := adapter.New("adapter-test")
	adapterSrv.WorkspaceRoot = t.TempDir()
	adapterSrv.Runtime = rt

	// A recycling microvm pool carrying the §5.2 deployer cleanup commands and
	// their aggregate cap on the poolstore sessionPolicy (the gateway-enforced
	// source of truth), plus the vm-restart scrub profile on the recycle block.
	pools := poolstore.NewMemory()
	if err := pools.Create(context.Background(), poolstore.Pool{
		Name:             "recycle-pool",
		RuntimeRef:       "echo",
		ExecutionMode:    runtimestore.ExecutionModeSession,
		IsolationProfile: isolation.ProfileMicrovm,
		SessionPolicy: &runtimestore.SessionPolicy{
			MaxConcurrentSessions: 1,
			CleanupCommands:       []string{"rm -rf /workspace/*", "history -c"},
			CleanupTimeoutSeconds: 45,
			Recycle: &runtimestore.RecyclePolicy{
				Enabled:                    true,
				AcknowledgeBestEffortScrub: true,
				MaxSessionsPerPod:          25,
				ScrubProfile:               runtimestore.MicrovmScrubVMRestart,
			},
		},
	}); err != nil {
		t.Fatalf("create recycle pool: %v", err)
	}

	// The CRD template carries the recycle block so ResolvePool sets
	// PoolMatch.Recycle and MicrovmScrubMode (both CRD-sourced), while the
	// poolstore mirror above supplies the gateway-enforced cleanup config.
	tmpl := podBindTemplate("recycle-tmpl", "echo", string(isolation.ProfileMicrovm))
	tmpl.Spec.SessionPolicy = &lennyv1.SessionPolicy{
		Recycle: &lennyv1.RecyclePolicy{ScrubProfile: string(runtimestore.MicrovmScrubVMRestart)},
	}
	cluster := podBindClient(
		t,
		podBindWarmPool("recycle-pool", "recycle-tmpl"),
		tmpl,
		podBindIdleSandbox("sbx-r", "recycle-pool", "10.244.3.9"),
	)
	binder := podBindBinder(cluster, podBindAdapterDialer(t, adapterSrv))
	binder.RecycleBoundary = &noopRecycleBoundary{}
	registry := podsession.NewRegistry()

	srv := sessionserver.New(memstore.New(), sessionserver.Options{
		IDFunc:                  func() string { return "sess-recycle" },
		DefaultIsolationProfile: isolation.ProfileMicrovm,
		PodBinder:               binder,
		PodRegistry:             registry,
		AgentNamespace:          podTestNS,
		Pools:                   pools,
	})

	body, _ := json.Marshal(sessionserver.CreateAndStartRequest{RuntimeRef: "echo", UserID: "alice@acme.com"})
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/start", bytes.NewReader(body))
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("start status = %d, want 201; body=%s", rr.Code, rr.Body.String())
	}

	// The full fold: poolstore sessionPolicy → poolPolicyMirror → ResolvePool
	// PoolMatch → exclusiveBindRequest → BindResult. The registry holds the
	// bind result the release path reads.
	bind, ok := registry.Get("sess-recycle")
	if !ok {
		t.Fatal("registry holds no binding for the recycling session")
	}
	if !bind.Recycle {
		t.Error("BindResult.Recycle = false, want true (recycling pool)")
	}
	if bind.CleanupTimeoutSeconds != 45 {
		t.Errorf("BindResult.CleanupTimeoutSeconds = %d, want 45 (from the poolstore sessionPolicy)", bind.CleanupTimeoutSeconds)
	}
	if len(bind.CleanupCommands) != 2 || bind.CleanupCommands[0] != "rm -rf /workspace/*" || bind.CleanupCommands[1] != "history -c" {
		t.Errorf("BindResult.CleanupCommands = %v, want [rm -rf /workspace/* history -c] (folded from the mirror)", bind.CleanupCommands)
	}

	// Swap the live adapter connection for a recording fake, then release
	// cleanly through the real binder so the exact §4.7 recycle Shutdown is
	// observable. The recording adapter does not run the async scrub; the wire
	// message carrying the folded config is the assertion here.
	rec := &recordingRecycleAdapter{}
	bind.Adapter.Close()
	bind.Adapter = dialRecycleRecorderClient(t, rec)

	if err := binder.Release(context.Background(), bind, "completed"); err != nil {
		t.Fatalf("Release: %v", err)
	}

	sd := rec.lastShutdown()
	if sd == nil {
		t.Fatal("no Shutdown reached the adapter on a clean recycle release")
	}
	rc := sd.GetRecycle()
	if rc == nil {
		t.Fatal("recycle release sent a plain Shutdown, want a RecycleScrub sub-message")
	}
	if rc.GetPodId() != "sbx-r" {
		t.Errorf("RecycleScrub.pod_id = %q, want sbx-r (the folded SandboxName the missing-report timeout keys on)", rc.GetPodId())
	}
	if rc.GetCleanupTimeoutSeconds() != 45 {
		t.Errorf("RecycleScrub.cleanup_timeout_seconds = %d, want 45", rc.GetCleanupTimeoutSeconds())
	}
	if got := rc.GetCleanupCommands(); len(got) != 2 || got[0] != "rm -rf /workspace/*" || got[1] != "history -c" {
		t.Errorf("RecycleScrub.cleanup_commands = %v, want the deployer cleanup commands", got)
	}
}
