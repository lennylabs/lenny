// SPDX-License-Identifier: MIT

package adapter_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/adapter"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// blockingSDKWarmRuntime is an SDK-warm runtime whose DemoteSDK blocks
// indefinitely until ForceTerminate releases it, so a test can drive the
// §6.1 line 67 bounded-teardown timeout path deterministically.
type blockingSDKWarmRuntime struct {
	fakeRuntime
	release chan struct{}
	forced  atomic.Bool
	demoted atomic.Int32
}

func newBlockingSDKWarmRuntime() *blockingSDKWarmRuntime {
	return &blockingSDKWarmRuntime{release: make(chan struct{})}
}

func (b *blockingSDKWarmRuntime) PreConnect(context.Context) error { return nil }

func (b *blockingSDKWarmRuntime) ConfigureWorkspace(context.Context, string, string) error {
	return nil
}

func (b *blockingSDKWarmRuntime) DemoteSDK(context.Context) error {
	<-b.release // only ForceTerminate unblocks: models a stuck SDK teardown.
	b.demoted.Add(1)
	return nil
}

func (b *blockingSDKWarmRuntime) ForceTerminate() {
	b.forced.Store(true)
	close(b.release)
}

var _ adapter.ForceTerminator = (*blockingSDKWarmRuntime)(nil)

// spec: §6.1 line 67 — on SIGTERM the adapter runs a bounded DemoteSDK to
// tear the pre-connected SDK down; within the timeout it completes cleanly
// and the pod returns to pod-warm.
func TestShutdownDemoteSDKDemotesPreConnected_spec_6_1_67(t *testing.T) {
	s, rt := sdkWarmServer(t)
	if err := s.PreConnect(context.Background()); err != nil {
		t.Fatalf("PreConnect: %v", err)
	}
	if !s.SDKWarmReady() {
		t.Fatalf("expected SDKWarmReady after PreConnect")
	}
	s.ShutdownDemoteSDK(5 * time.Second)
	if rt.demoted != 1 {
		t.Errorf("runtime demoted %d times, want 1", rt.demoted)
	}
	if s.SDKWarmReady() {
		t.Errorf("ShutdownDemoteSDK must clear SDKWarmReady")
	}
	// The pod returned to pod-warm: a fresh StartSession claims it.
	if _, err := s.StartSession(context.Background(), startReq("sess-after")); err != nil {
		t.Errorf("StartSession after shutdown demote: %v", err)
	}
}

// spec: §6.1 line 67 step 2 — when the bounded DemoteSDK overruns its
// timeout, the adapter force-terminates the SDK process so it is not
// abandoned mid-connection.
func TestShutdownDemoteSDKForceTerminatesOnTimeout_spec_6_1_67(t *testing.T) {
	rt := newBlockingSDKWarmRuntime()
	s := adapter.New("test")
	s.WorkspaceRoot = t.TempDir()
	s.Runtime = rt
	if err := s.PreConnect(context.Background()); err != nil {
		t.Fatalf("PreConnect: %v", err)
	}
	s.ShutdownDemoteSDK(20 * time.Millisecond)
	if !rt.forced.Load() {
		t.Errorf("ShutdownDemoteSDK on timeout must force-terminate the SDK")
	}
	if s.SDKWarmReady() {
		t.Errorf("ShutdownDemoteSDK timeout path must clear SDKWarmReady")
	}
}

// spec: §6.1 line 67 — ShutdownDemoteSDK is a no-op for a pod-warm pod
// (there is no pre-connected SDK to tear down), so a SIGTERM handler may
// call it unconditionally.
func TestShutdownDemoteSDKPodWarmNoop_spec_6_1_67(t *testing.T) {
	s, _, _ := sessionServer(t) // plain fakeRuntime: pod-warm
	s.ShutdownDemoteSDK(5 * time.Second)
	// No panic, and the pod is still claimable.
	if _, err := s.StartSession(context.Background(), startReq("sess-1")); err != nil {
		t.Errorf("StartSession after pod-warm shutdown demote: %v", err)
	}
}

// spec: §6.1 line 67 — an SDK-warm pod that never pre-connected has no SDK
// to tear down, so ShutdownDemoteSDK does not call the runtime's DemoteSDK.
func TestShutdownDemoteSDKNotConnectedNoop_spec_6_1_67(t *testing.T) {
	s, rt := sdkWarmServer(t)
	// No PreConnect: sdkConnected stays false.
	s.ShutdownDemoteSDK(5 * time.Second)
	if rt.demoted != 0 {
		t.Errorf("runtime demoted %d times on a non-pre-connected pod, want 0", rt.demoted)
	}
}

// spec: §6.1 line 67 — the bounded-teardown timeout is read from
// LENNY_DEMOTE_TIMEOUT_SECONDS, defaulting to 5s when unset or invalid.
func TestDemoteTimeoutFromEnv_spec_6_1_67(t *testing.T) {
	cases := []struct {
		name string
		set  bool
		val  string
		want time.Duration
	}{
		{name: "unset", set: false, want: adapter.DefaultDemoteTimeout},
		{name: "valid", set: true, val: "12", want: 12 * time.Second},
		{name: "zero", set: true, val: "0", want: adapter.DefaultDemoteTimeout},
		{name: "negative", set: true, val: "-3", want: adapter.DefaultDemoteTimeout},
		{name: "garbage", set: true, val: "soon", want: adapter.DefaultDemoteTimeout},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.set {
				t.Setenv("LENNY_DEMOTE_TIMEOUT_SECONDS", tc.val)
			} else {
				t.Setenv("LENNY_DEMOTE_TIMEOUT_SECONDS", "")
			}
			if got := adapter.DemoteTimeoutFromEnv(); got != tc.want {
				t.Errorf("DemoteTimeoutFromEnv() = %v, want %v", got, tc.want)
			}
		})
	}
}

// spec: §6.1 line 67 — the SDK-warm in-process runtime's force-terminate
// hook hard-stops a loop blocked on its input pipe without waiting for it to
// drain, the substitute for SIGKILLing a real SDK subprocess.
func TestSDKWarmInProcessForceTerminate_spec_6_1_67(t *testing.T) {
	// sdkWarmEchoLoop blocks on in.Read until its pipe closes; ForceClose
	// closes the pipes so ForceTerminate returns promptly.
	rt := adapter.NewSDKWarmInProcessRuntime(sdkWarmEchoLoop)
	s := adapter.New("test")
	s.WorkspaceRoot = t.TempDir()
	s.ManifestDir = t.TempDir()
	s.Runtime = rt
	if err := s.PreConnect(context.Background()); err != nil {
		t.Fatalf("PreConnect: %v", err)
	}
	if _, err := s.ConfigureWorkspace(context.Background(), &adapterv1.ConfigureWorkspaceRequest{
		SessionId: &adapterv1.SessionId{Value: "sess-1"},
		Cwd:       s.WorkspaceRoot,
	}); err != nil {
		t.Fatalf("ConfigureWorkspace: %v", err)
	}
	done := make(chan struct{})
	go func() {
		rt.ForceTerminate()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("ForceTerminate did not return: the stuck loop was not force-closed")
	}
	// The runtime's session binding was force-cleared, so a fresh session can
	// rebind the loop. (The Server-level SDKWarmReady flag is cleared by
	// ShutdownDemoteSDK's timeout path, covered separately above.)
	if err := rt.ConfigureWorkspace(context.Background(), "sess-2", s.WorkspaceRoot); err != nil {
		t.Errorf("ConfigureWorkspace after ForceTerminate: %v", err)
	}
}
