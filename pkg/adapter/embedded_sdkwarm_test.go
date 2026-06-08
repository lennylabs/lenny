// SPDX-License-Identifier: MIT

package adapter_test

import (
	"context"
	"io"
	"testing"

	"github.com/lennylabs/lenny/pkg/adapter"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// echoLoop is the §15.4.1 in-process loop the SDK-warm reference runtime
// drives: it echoes each inbound line back verbatim until EOF.
func sdkWarmEchoLoop(_ context.Context, in io.Reader, out io.Writer) error {
	buf := make([]byte, 4096)
	for {
		n, err := in.Read(buf)
		if n > 0 {
			if _, werr := out.Write(buf[:n]); werr != nil {
				return werr
			}
		}
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
	}
}

// spec: §6.1 lines 30-40 — the SDK-warm reference runtime pre-connects at
// warm time, binds to a session via ConfigureWorkspace, and tears down via
// DemoteSDK so a subsequent StartSession runs the pod-warm path.
func TestSDKWarmInProcessRuntime_spec_6_1(t *testing.T) {
	rt := adapter.NewSDKWarmInProcessRuntime(sdkWarmEchoLoop)
	s := adapter.New("test")
	s.WorkspaceRoot = t.TempDir()
	s.ManifestDir = t.TempDir()
	s.Runtime = rt

	// Warm time: not ready until PreConnect.
	if s.SDKWarmReady() {
		t.Fatalf("not ready before PreConnect")
	}
	if err := s.PreConnect(context.Background()); err != nil {
		t.Fatalf("PreConnect: %v", err)
	}
	if !s.SDKWarmReady() {
		t.Fatalf("expected SDKWarmReady after PreConnect")
	}

	// Claim: ConfigureWorkspace binds the pre-connected loop to the session.
	if _, err := s.ConfigureWorkspace(context.Background(), &adapterv1.ConfigureWorkspaceRequest{
		SessionId: &adapterv1.SessionId{Value: "sess-1"},
		Cwd:       s.WorkspaceRoot,
	}); err != nil {
		t.Fatalf("ConfigureWorkspace: %v", err)
	}

	// The bound loop echoes a message round-trip.
	ch, err := rt.Output(context.Background(), "sess-1")
	if err != nil {
		t.Fatalf("Output: %v", err)
	}
	if err := rt.WriteEnvelope("sess-1", []byte(`{"type":"ping"}`)); err != nil {
		t.Fatalf("WriteEnvelope: %v", err)
	}
	got := <-ch
	if string(got) != `{"type":"ping"}` {
		t.Fatalf("echo = %q, want the ping envelope", got)
	}

	// DemoteSDK tears the loop down and clears warm readiness.
	if _, err := s.DemoteSDK(context.Background(), &adapterv1.DemoteSDKRequest{}); err != nil {
		t.Fatalf("DemoteSDK: %v", err)
	}
	if s.SDKWarmReady() {
		t.Fatalf("DemoteSDK must clear SDKWarmReady")
	}
	// After demotion the pod is pod-warm: a fresh StartSession claims it.
	if _, err := s.StartSession(context.Background(), &adapterv1.StartSessionRequest{
		SessionId: &adapterv1.SessionId{Value: "sess-2"},
	}); err != nil {
		t.Fatalf("StartSession after DemoteSDK: %v", err)
	}
}
