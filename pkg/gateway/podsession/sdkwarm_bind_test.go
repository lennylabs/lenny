// SPDX-License-Identifier: MIT

package podsession_test

import (
	"context"
	"errors"
	"testing"

	"github.com/lennylabs/lenny/pkg/adapter"
	"github.com/lennylabs/lenny/pkg/gateway/podsession"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// fakeSDKWarmRuntime is a fakeRuntime that also implements the §6.1
// SDK-warm fast path, so the adapter's ConfigureWorkspace / DemoteSDK RPCs
// engage instead of returning Unimplemented.
type fakeSDKWarmRuntime struct {
	fakeRuntime
	configured []string // cwd of each ConfigureWorkspace call
	demoted    int
}

func (f *fakeSDKWarmRuntime) PreConnect(context.Context) error { return nil }

func (f *fakeSDKWarmRuntime) ConfigureWorkspace(_ context.Context, _ string, cwd string) error {
	f.configured = append(f.configured, cwd)
	return nil
}

func (f *fakeSDKWarmRuntime) DemoteSDK(context.Context) error {
	f.demoted++
	return nil
}

func inlinePlan(path string) *adapterv1.WorkspacePlan {
	return &adapterv1.WorkspacePlan{
		Sources: []*adapterv1.WorkspaceSource{
			{Type: "inlineFile", Path: path, Content: "x"},
		},
	}
}

// spec: §6.1 lines 30-34 — a preConnect pod whose plan matches no blocking
// path stays SDK-warm: the binder points the pre-connected SDK at the
// workspace (ConfigureWorkspace) and does not StartSession from cold.
func TestBindSDKWarmConfiguresWhenNoBlockingPath_spec_6_1(t *testing.T) {
	rt := &fakeSDKWarmRuntime{}
	srv := adapter.New("adapter-test")
	root := t.TempDir()
	srv.WorkspaceRoot = root
	srv.Runtime = rt

	c := k8sClient(t, idleSandbox("sbx-1", "10.244.1.7"))
	binder := newBinder(c, adapterDialer(t, srv))

	res, err := binder.Bind(context.Background(), podsession.BindRequest{
		Pool: testPool, SessionID: "sess-1", TenantID: "acme", Runtime: "claude-code",
		Plan:                 inlinePlan("src/main.go"),
		PreConnect:           true,
		SDKWarmBlockingPaths: []string{"CLAUDE.md", ".claude/*"},
	})
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	defer res.Adapter.Close()

	if len(rt.configured) != 1 || rt.configured[0] != root {
		t.Errorf("ConfigureWorkspace cwd = %v, want [%q]", rt.configured, root)
	}
	if rt.demoted != 0 {
		t.Errorf("unexpected demotion: %d", rt.demoted)
	}
	if rt.started != "" {
		t.Errorf("StartSession should not be called on the SDK-warm path, got %q", rt.started)
	}
}

// spec: §6.1 lines 34-40 — a preConnect pod whose plan matches a blocking
// path is demoted (DemoteSDK) before materialization and served via the
// pod-warm StartSession path; the demotion metric increments.
func TestBindSDKWarmDemotesOnBlockingPath_spec_6_1(t *testing.T) {
	rt := &fakeSDKWarmRuntime{}
	srv := adapter.New("adapter-test")
	srv.WorkspaceRoot = t.TempDir()
	srv.Runtime = rt

	c := k8sClient(t, idleSandbox("sbx-1", "10.244.1.7"))
	binder := newBinder(c, adapterDialer(t, srv))
	var demotedPools []string
	binder.SDKDemotion = func(pool string) { demotedPools = append(demotedPools, pool) }

	res, err := binder.Bind(context.Background(), podsession.BindRequest{
		Pool: testPool, SessionID: "sess-1", TenantID: "acme", Runtime: "claude-code",
		Plan:                 inlinePlan("CLAUDE.md"),
		PreConnect:           true,
		SDKWarmBlockingPaths: []string{"CLAUDE.md", ".claude/*"},
	})
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	defer res.Adapter.Close()

	if rt.demoted != 1 {
		t.Errorf("DemoteSDK called %d times, want 1", rt.demoted)
	}
	if len(rt.configured) != 0 {
		t.Errorf("ConfigureWorkspace should not run on the demoted path, got %v", rt.configured)
	}
	if rt.started != "sess-1" {
		t.Errorf("demoted pod must StartSession pod-warm, started = %q", rt.started)
	}
	if len(demotedPools) != 1 || demotedPools[0] != testPool {
		t.Errorf("SDKDemotion metric = %v, want [%q]", demotedPools, testPool)
	}
}

// spec: §6.1 line 40 — a preConnect pod whose adapter cannot DemoteSDK
// (returns UNIMPLEMENTED) fails the session with SDK_DEMOTION_NOT_SUPPORTED
// rather than serving it with stale SDK state.
func TestBindSDKWarmDemotionNotSupported_spec_6_1(t *testing.T) {
	// A plain fakeRuntime is pod-warm: its DemoteSDK returns Unimplemented.
	rt := &fakeRuntime{}
	srv := adapter.New("adapter-test")
	srv.WorkspaceRoot = t.TempDir()
	srv.Runtime = rt

	c := k8sClient(t, idleSandbox("sbx-1", "10.244.1.7"))
	binder := newBinder(c, adapterDialer(t, srv))

	_, err := binder.Bind(context.Background(), podsession.BindRequest{
		Pool: testPool, SessionID: "sess-1", TenantID: "acme", Runtime: "claude-code",
		Plan:                 inlinePlan("CLAUDE.md"),
		PreConnect:           true,
		SDKWarmBlockingPaths: []string{"CLAUDE.md"},
	})
	var notSupported *podsession.SDKDemotionNotSupported
	if !errors.As(err, &notSupported) {
		t.Fatalf("Bind error = %v, want *SDKDemotionNotSupported", err)
	}
	if rt.started != "" {
		t.Errorf("session must not start after a failed demotion, started = %q", rt.started)
	}
}

// spec: §6.1 line 38 — an empty sdkWarmBlockingPaths list keeps the pod
// SDK-warm for every request (no demotion path checking).
func TestBindSDKWarmEmptyBlockingPathsNeverDemotes_spec_6_1(t *testing.T) {
	rt := &fakeSDKWarmRuntime{}
	srv := adapter.New("adapter-test")
	srv.WorkspaceRoot = t.TempDir()
	srv.Runtime = rt

	c := k8sClient(t, idleSandbox("sbx-1", "10.244.1.7"))
	binder := newBinder(c, adapterDialer(t, srv))

	res, err := binder.Bind(context.Background(), podsession.BindRequest{
		Pool: testPool, SessionID: "sess-1", TenantID: "acme", Runtime: "claude-code",
		Plan:                 inlinePlan("CLAUDE.md"),
		PreConnect:           true,
		SDKWarmBlockingPaths: nil, // §6.1 line 38 opt-out
	})
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	defer res.Adapter.Close()

	if rt.demoted != 0 {
		t.Errorf("empty blocking paths must never demote, got %d", rt.demoted)
	}
	if len(rt.configured) != 1 {
		t.Errorf("pod must stay SDK-warm (ConfigureWorkspace), configured = %v", rt.configured)
	}
}
