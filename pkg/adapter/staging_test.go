// SPDX-License-Identifier: MIT

package adapter

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

func runSetupReq(sessionID string, policy *adapterv1.SetupPolicy, cmds ...string) *adapterv1.RunSetupRequest {
	req := &adapterv1.RunSetupRequest{
		SessionId:   &adapterv1.SessionId{Value: sessionID},
		SetupPolicy: policy,
	}
	for _, c := range cmds {
		req.SetupCommands = append(req.SetupCommands, &adapterv1.SetupCommand{Cmd: c})
	}
	return req
}

func TestRunSetupExecutesCommands(t *testing.T) {
	root := t.TempDir()
	srv := &Server{WorkspaceRoot: root}
	if _, err := srv.RunSetup(context.Background(),
		runSetupReq("sess-1", nil, "echo ok > result.txt", "mkdir sub")); err != nil {
		t.Fatalf("RunSetup: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(root, "result.txt"))
	if err != nil {
		t.Fatalf("read result.txt: %v", err)
	}
	if strings.TrimSpace(string(b)) != "ok" {
		t.Errorf("result.txt = %q, want ok", b)
	}
	if fi, err := os.Stat(filepath.Join(root, "sub")); err != nil || !fi.IsDir() {
		t.Errorf("setup command did not create the sub directory: %v", err)
	}
}

func TestRunSetupNoCommands(t *testing.T) {
	// The §4.7 sequence calls RunSetup even for a plan with no setup
	// commands; an empty list completes the phase as a no-op.
	srv := &Server{WorkspaceRoot: t.TempDir()}
	if _, err := srv.RunSetup(context.Background(), runSetupReq("sess-1", nil)); err != nil {
		t.Errorf("RunSetup with no commands = %v, want nil", err)
	}
}

func TestRunSetupRequiresSessionID(t *testing.T) {
	srv := &Server{WorkspaceRoot: t.TempDir()}
	_, err := srv.RunSetup(context.Background(), runSetupReq("", nil, "true"))
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("RunSetup without a session id = %v, want InvalidArgument", err)
	}
}

func TestRunSetupRequiresWorkspaceRoot(t *testing.T) {
	srv := &Server{}
	_, err := srv.RunSetup(context.Background(), runSetupReq("sess-1", nil, "true"))
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("RunSetup without a workspace root = %v, want FailedPrecondition", err)
	}
}

func TestRunSetupFailingCommandIsRejected(t *testing.T) {
	srv := &Server{WorkspaceRoot: t.TempDir()}
	_, err := srv.RunSetup(context.Background(), runSetupReq("sess-1", nil, "exit 3"))
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("RunSetup with a failing command = %v, want FailedPrecondition", err)
	}
}

func TestRunSetupAggregateTimeoutFails(t *testing.T) {
	// §5.1: a setupPolicy with on_timeout "fail" aborts the phase when
	// the aggregate cap is exceeded. Threading the policy through the
	// RunSetup RPC is what this exercises.
	srv := &Server{WorkspaceRoot: t.TempDir()}
	policy := &adapterv1.SetupPolicy{TimeoutSeconds: 1, OnTimeout: "fail"}
	_, err := srv.RunSetup(context.Background(), runSetupReq("sess-1", policy, "sleep 30"))
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("RunSetup over the aggregate cap = %v, want FailedPrecondition", err)
	}
}

func TestRunSetupAggregateTimeoutWarnProceeds(t *testing.T) {
	// §5.1: a setupPolicy with on_timeout "warn" proceeds past the cap
	// rather than failing pod startup.
	srv := &Server{WorkspaceRoot: t.TempDir()}
	policy := &adapterv1.SetupPolicy{TimeoutSeconds: 1, OnTimeout: "warn"}
	if _, err := srv.RunSetup(context.Background(), runSetupReq("sess-1", policy, "sleep 30")); err != nil {
		t.Errorf("RunSetup over the cap with the warn disposition = %v, want nil", err)
	}
}
