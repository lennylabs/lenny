// SPDX-License-Identifier: MIT

package adapter_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/lennylabs/lenny/pkg/adapter"
	"github.com/lennylabs/lenny/pkg/adapter/gatewaycontrol"
	"github.com/lennylabs/lenny/pkg/adapter/slotlayout"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// readManifest decodes the adapter manifest written into dir.
func readManifest(t *testing.T, dir string) adapter.Manifest {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, adapter.ManifestFilename))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var m adapter.Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	return m
}

// fakeSDKWarmRuntime is a fakeRuntime that also implements the §4.7
// SDK-warm fast path, so the adapter's ConfigureWorkspace / DemoteSDK RPCs
// engage instead of returning Unimplemented.
type fakeSDKWarmRuntime struct {
	fakeRuntime
	preConnected int      // number of PreConnect calls
	preConnErr   error    // error PreConnect returns
	configured   []string // cwd of each ConfigureWorkspace call
	configErr    error
	demoted      int
	demoteErr    error
	// onConfigure runs inside ConfigureWorkspace on the calling goroutine,
	// between the handler's claim and its start confirmation.
	onConfigure func()
}

func (f *fakeSDKWarmRuntime) PreConnect(_ context.Context) error {
	if f.preConnErr != nil {
		return f.preConnErr
	}
	f.preConnected++
	return nil
}

func (f *fakeSDKWarmRuntime) ConfigureWorkspace(_ context.Context, _ string, cwd string) error {
	if f.onConfigure != nil {
		f.onConfigure()
	}
	if f.configErr != nil {
		return f.configErr
	}
	f.configured = append(f.configured, cwd)
	return nil
}

func (f *fakeSDKWarmRuntime) DemoteSDK(_ context.Context) error {
	if f.demoteErr != nil {
		return f.demoteErr
	}
	f.demoted++
	return nil
}

func configureReq(sessionID, cwd string) *adapterv1.ConfigureWorkspaceRequest {
	return &adapterv1.ConfigureWorkspaceRequest{
		SessionId: &adapterv1.SessionId{Value: sessionID},
		Cwd:       cwd,
	}
}

func sdkWarmServer(t *testing.T) (*adapter.Server, *fakeSDKWarmRuntime) {
	t.Helper()
	rt := &fakeSDKWarmRuntime{}
	s := adapter.New("test")
	s.WorkspaceBase = t.TempDir()
	s.Runtime = rt
	return s, rt
}

// spec: §4.7 — ConfigureWorkspace applies only to SDK-warm pods; a
// pod-warm adapter returns Unimplemented.
func TestConfigureWorkspacePodWarmUnimplemented_spec_4_7(t *testing.T) {
	s, _, _ := sessionServer(t) // plain fakeRuntime: pod-warm
	_, err := s.ConfigureWorkspace(context.Background(), configureReq("sess-1", "/workspace/slots/sess-1/current"))
	if status.Code(err) != codes.Unimplemented {
		t.Errorf("ConfigureWorkspace on pod-warm pod: code = %v, want Unimplemented", status.Code(err))
	}
}

// spec: §4.7 — DemoteSDK applies only to SDK-warm pods; a pod-warm adapter
// returns Unimplemented.
func TestDemoteSDKPodWarmUnimplemented_spec_4_7(t *testing.T) {
	s, _, _ := sessionServer(t)
	_, err := s.DemoteSDK(context.Background(), &adapterv1.DemoteSDKRequest{})
	if status.Code(err) != codes.Unimplemented {
		t.Errorf("DemoteSDK on pod-warm pod: code = %v, want Unimplemented", status.Code(err))
	}
}

// spec: §4.7 — ConfigureWorkspace points the pre-connected SDK at the
// finalized cwd and claims the session.
func TestConfigureWorkspaceConfiguresPreConnected_spec_4_7(t *testing.T) {
	s, rt := sdkWarmServer(t)
	if _, err := s.ConfigureWorkspace(context.Background(), configureReq("sess-1", "/workspace/slots/sess-1/current")); err != nil {
		t.Fatalf("ConfigureWorkspace: %v", err)
	}
	if len(rt.configured) != 1 || rt.configured[0] != "/workspace/slots/sess-1/current" {
		t.Errorf("runtime configured = %v, want [/workspace/slots/sess-1/current]", rt.configured)
	}
	// The pod now holds the session: a StartSession for another session is
	// Unavailable.
	if _, err := s.StartSession(context.Background(), startReq("sess-2")); status.Code(err) != codes.Unavailable {
		t.Errorf("StartSession after ConfigureWorkspace: code = %v, want Unavailable", status.Code(err))
	}
}

// spec: §4.7 — ConfigureWorkspace is idempotent: a repeat for the same
// session re-points the runtime without rewriting the manifest, so the
// §15.4.3 nonce the runtime authenticated with stays stable.
func TestConfigureWorkspaceIdempotent_spec_4_7(t *testing.T) {
	s, rt := sdkWarmServer(t)
	s.ManifestDir = t.TempDir()

	if _, err := s.ConfigureWorkspace(context.Background(), configureReq("sess-1", "/workspace/slots/sess-1/current")); err != nil {
		t.Fatalf("first ConfigureWorkspace: %v", err)
	}
	first := readManifest(t, s.ManifestDir).MCPNonce

	if _, err := s.ConfigureWorkspace(context.Background(), configureReq("sess-1", "/workspace/slots/sess-1/current")); err != nil {
		t.Fatalf("repeat ConfigureWorkspace: %v", err)
	}
	second := readManifest(t, s.ManifestDir).MCPNonce

	if first == "" || first != second {
		t.Errorf("manifest nonce changed across idempotent calls: %q -> %q", first, second)
	}
	if len(rt.configured) != 2 {
		t.Errorf("runtime configured %d times, want 2 (idempotent re-point)", len(rt.configured))
	}
}

// spec: §4.7 — a different session on an already-claimed pod is Unavailable.
func TestConfigureWorkspaceDifferentSessionUnavailable_spec_4_7(t *testing.T) {
	s, _ := sdkWarmServer(t)
	if _, err := s.ConfigureWorkspace(context.Background(), configureReq("sess-1", "/workspace/slots/sess-1/current")); err != nil {
		t.Fatalf("ConfigureWorkspace sess-1: %v", err)
	}
	_, err := s.ConfigureWorkspace(context.Background(), configureReq("sess-2", "/workspace/slots/sess-1/current"))
	if status.Code(err) != codes.Unavailable {
		t.Errorf("ConfigureWorkspace for a second session: code = %v, want Unavailable", status.Code(err))
	}
}

// spec: §4.7 — when the runtime cannot accept the workspace, the adapter
// reports an error and releases the tentatively claimed session so the
// gateway's DemoteSDK fallback can land a fresh start.
func TestConfigureWorkspaceRuntimeErrorReleases_spec_4_7(t *testing.T) {
	s, rt := sdkWarmServer(t)
	rt.configErr = errors.New("sdk rejected workspace")
	if _, err := s.ConfigureWorkspace(context.Background(), configureReq("sess-1", "/workspace/slots/sess-1/current")); status.Code(err) != codes.Internal {
		t.Fatalf("ConfigureWorkspace error: code = %v, want Internal", status.Code(err))
	}
	// The session was released: a fresh ConfigureWorkspace can claim the pod.
	rt.configErr = nil
	if _, err := s.ConfigureWorkspace(context.Background(), configureReq("sess-1", "/workspace/slots/sess-1/current")); err != nil {
		t.Errorf("ConfigureWorkspace after a released failure: %v", err)
	}
}

// spec: §4.7 — DemoteSDK tears down the pre-connected SDK and returns the
// pod to pod-warm (idle), so a subsequent StartSession can claim it.
func TestDemoteSDKTearsDown_spec_4_7(t *testing.T) {
	s, rt := sdkWarmServer(t)
	if _, err := s.ConfigureWorkspace(context.Background(), configureReq("sess-1", "/workspace/slots/sess-1/current")); err != nil {
		t.Fatalf("ConfigureWorkspace: %v", err)
	}
	resp, err := s.DemoteSDK(context.Background(), &adapterv1.DemoteSDKRequest{Reason: "blocking-path"})
	if err != nil {
		t.Fatalf("DemoteSDK: %v", err)
	}
	if !resp.GetDemoted() {
		t.Error("DemoteSDK response demoted = false, want true")
	}
	if rt.demoted != 1 {
		t.Errorf("runtime demoted %d times, want 1", rt.demoted)
	}
	// Pod returned to idle: StartSession claims it.
	if _, err := s.StartSession(context.Background(), startReq("sess-2")); err != nil {
		t.Errorf("StartSession after DemoteSDK: %v", err)
	}
}

func TestConfigureWorkspaceMissingArgs_spec_4_7(t *testing.T) {
	s, _ := sdkWarmServer(t)
	if _, err := s.ConfigureWorkspace(context.Background(), configureReq("", "/workspace/slots/sess-1/current")); status.Code(err) != codes.InvalidArgument {
		t.Errorf("ConfigureWorkspace with no session id: code = %v, want InvalidArgument", status.Code(err))
	}
	if _, err := s.ConfigureWorkspace(context.Background(), configureReq("sess-1", "")); status.Code(err) != codes.InvalidArgument {
		t.Errorf("ConfigureWorkspace with no cwd: code = %v, want InvalidArgument", status.Code(err))
	}
}

// spec: §4.7 — the adapter advertises the preConnect capability during
// negotiation when the wired runtime supports the SDK-warm fast path, and
// withholds it for a pod-warm runtime.
func TestNegotiateVersionAdvertisesPreConnect_spec_4_7(t *testing.T) {
	sdkWarm, _ := sdkWarmServer(t)
	resp, err := sdkWarm.NegotiateVersion(context.Background(), &adapterv1.NegotiateVersionRequest{
		AcceptedProtocolVersions: []string{"1.0.0"},
	})
	if err != nil {
		t.Fatalf("NegotiateVersion: %v", err)
	}
	if !contains(resp.GetCapabilities(), adapter.CapabilityPreConnect) {
		t.Errorf("SDK-warm capabilities = %v, want to include %q", resp.GetCapabilities(), adapter.CapabilityPreConnect)
	}

	podWarm, _, _ := sessionServer(t)
	resp, err = podWarm.NegotiateVersion(context.Background(), &adapterv1.NegotiateVersionRequest{
		AcceptedProtocolVersions: []string{"1.0.0"},
	})
	if err != nil {
		t.Fatalf("NegotiateVersion (pod-warm): %v", err)
	}
	if contains(resp.GetCapabilities(), adapter.CapabilityPreConnect) {
		t.Errorf("pod-warm capabilities = %v, want to omit %q", resp.GetCapabilities(), adapter.CapabilityPreConnect)
	}
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// spec: §6.1 — PreConnect starts the SDK at warm time for a
// preConnect runtime and is idempotent; SDKWarmReady gates claimability.
func TestPreConnect_spec_6_1(t *testing.T) {
	t.Run("preConnect starts the SDK once and reports ready", func(t *testing.T) {
		s, rt := sdkWarmServer(t)
		if s.SDKWarmReady() {
			t.Fatalf("not ready before PreConnect")
		}
		if err := s.PreConnect(context.Background()); err != nil {
			t.Fatalf("PreConnect: %v", err)
		}
		if rt.preConnected != 1 {
			t.Fatalf("PreConnect called %d times, want 1", rt.preConnected)
		}
		if !s.SDKWarmReady() {
			t.Fatalf("expected SDKWarmReady after PreConnect")
		}
		// Idempotent: a second call does not restart the SDK.
		if err := s.PreConnect(context.Background()); err != nil {
			t.Fatalf("PreConnect repeat: %v", err)
		}
		if rt.preConnected != 1 {
			t.Fatalf("PreConnect re-invoked runtime %d times, want 1", rt.preConnected)
		}
	})

	t.Run("pod-warm runtime PreConnect is a no-op and never ready", func(t *testing.T) {
		s, _, _ := sessionServer(t) // plain fakeRuntime: pod-warm
		if err := s.PreConnect(context.Background()); err != nil {
			t.Fatalf("pod-warm PreConnect should be a no-op success: %v", err)
		}
		if s.SDKWarmReady() {
			t.Fatalf("pod-warm pod must never report SDKWarmReady")
		}
	})

	t.Run("PreConnect failure leaves the pod not ready", func(t *testing.T) {
		s, rt := sdkWarmServer(t)
		rt.preConnErr = errors.New("sdk boot failed")
		if err := s.PreConnect(context.Background()); err == nil {
			t.Fatalf("expected PreConnect error")
		}
		if s.SDKWarmReady() {
			t.Fatalf("a failed PreConnect must not report ready")
		}
	})

	t.Run("DemoteSDK clears warm readiness", func(t *testing.T) {
		s, _ := sdkWarmServer(t)
		if err := s.PreConnect(context.Background()); err != nil {
			t.Fatalf("PreConnect: %v", err)
		}
		if !s.SDKWarmReady() {
			t.Fatalf("expected ready after PreConnect")
		}
		if _, err := s.DemoteSDK(context.Background(), &adapterv1.DemoteSDKRequest{}); err != nil {
			t.Fatalf("DemoteSDK: %v", err)
		}
		if s.SDKWarmReady() {
			t.Fatalf("DemoteSDK must clear SDKWarmReady")
		}
	})
}

// countingScrubReporter records the sessions a §5.2 cleanup-outcome report
// was filed for.
type countingScrubReporter struct {
	mu       sync.Mutex
	sessions []string
}

func (r *countingScrubReporter) ReportSessionScrub(_ context.Context, _, sessionID string, _ gatewaycontrol.SessionScrubOutcome) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sessions = append(r.sessions, sessionID)
	return nil
}

func (r *countingScrubReporter) reportsFor(sessionID string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, id := range r.sessions {
		if id == sessionID {
			n++
		}
	}
	return n
}

// spec: §4.7.1 (role and gateway RPC contract); §6.1 (SDK-warm pre-connect); §7.1 (normal flow)
//
// Rule 8 on the SDK-warm start. A ConfigureWorkspace whose claimed entry
// leaves the registry while the pre-connected SDK is being pointed at the
// workspace takes the runtime half of the §6.1 demotion and refuses on
// Aborted. It files no cleanup-outcome report for the session, and it
// removes nothing: a successor that re-created the entry under the same
// slot identifier keeps its entry, its cwd and its credential file.
func TestTheSDKWarmConfirmationRefusesWithoutReleasing_spec_4_7_1(t *testing.T) {
	for _, withSuccessor := range []bool{false, true} {
		name := "removed"
		if withSuccessor {
			name = "successor"
		}
		t.Run(name, func(t *testing.T) {
			s, rt := sdkWarmServer(t)
			s.CredentialsDir = t.TempDir()
			reporter := &countingScrubReporter{}
			s.SessionScrubReporter = reporter
			if err := s.PreConnect(t.Context()); err != nil {
				t.Fatalf("PreConnect: %v", err)
			}
			rt.onConfigure = func() {
				rt.onConfigure = nil
				if _, err := s.Shutdown(t.Context(), &adapterv1.ShutdownRequest{
					SessionId:             &adapterv1.SessionId{Value: "alice"},
					UnconditionalTeardown: true,
				}); err != nil {
					t.Errorf("unconditional Shutdown from the hook: %v", err)
				}
				if withSuccessor {
					if _, err := s.AssignCredentials(t.Context(), &adapterv1.AssignCredentialsRequest{
						BindAttempt: "attempt-b",
						SessionId:   &adapterv1.SessionId{Value: "alice"},
						Leases: map[string]*adapterv1.CredentialLease{
							"anthropic": credLease("l-b", "anthropic", `{}`),
						},
					}); err != nil {
						t.Errorf("successor AssignCredentials: %v", err)
					}
				}
			}

			_, err := s.ConfigureWorkspace(t.Context(), configureReq("alice", "/workspace/slots/alice/current"))
			if status.Code(err) != codes.Aborted {
				t.Fatalf("ConfigureWorkspace = %v, want Aborted for a refused start confirmation", err)
			}
			if rt.demoted != 1 {
				t.Errorf("SDKWarmRuntime.DemoteSDK calls = %d, want 1", rt.demoted)
			}
			if s.SDKWarmReady() {
				t.Error("SDKWarmReady is still true after the refusal demoted the SDK")
			}
			if n := reporter.reportsFor("alice"); n != 0 {
				t.Errorf("cleanup-outcome reports for alice = %d, want 0", n)
			}
			if !withSuccessor {
				return
			}
			paths, err := slotlayout.Resolve(slotlayout.Roots{
				Workspace:   s.WorkspaceBase,
				Credentials: s.CredentialsDir,
			}, "alice")
			if err != nil {
				t.Fatalf("resolve alice's slot paths: %v", err)
			}
			if _, err := os.Stat(paths.Current); err != nil {
				t.Errorf("the successor's cwd is gone: %v", err)
			}
			if _, err := os.Stat(paths.CredentialsFile); err != nil {
				t.Errorf("the successor's credential file is gone: %v", err)
			}
			resp, err := s.Shutdown(t.Context(), &adapterv1.ShutdownRequest{
				SessionId:   &adapterv1.SessionId{Value: "alice"},
				BindAttempt: "attempt-b",
			})
			if err != nil {
				t.Fatalf("Shutdown naming the successor: %v", err)
			}
			if got := resp.GetSlotReclaim(); got != adapterv1.SlotReclaimOutcome_SLOT_RECLAIM_OUTCOME_RECLAIMED {
				t.Errorf("Shutdown naming the successor = %v, want RECLAIMED; the refusal released the successor's entry", got)
			}
		})
	}
}
