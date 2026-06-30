// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

func TestExperimentContextToProtoNil(t *testing.T) {
	// An unenrolled session has no experimentContext to deliver.
	if got := experimentContextToProto(nil); got != nil {
		t.Errorf("experimentContextToProto(nil) = %v, want nil", got)
	}
}

func TestExperimentContextToProtoMapsFields(t *testing.T) {
	got := experimentContextToProto(&sessionstore.ExperimentContext{
		ExperimentID: "exp_1",
		VariantID:    "treatment",
		Inherited:    true,
	})
	if got == nil {
		t.Fatal("experimentContextToProto returned nil for an enrolled session")
	}
	if got.GetExperimentId() != "exp_1" || got.GetVariantId() != "treatment" || !got.GetInherited() {
		t.Errorf("proto experimentContext = %+v, want exp_1/treatment inherited", got)
	}
}

// spec: §6.4 line 260 / §26.2 reference catalog — F-7.5.12.
// runtimeSetupPolicy applies the 300s aggregate-cap default when the
// runtime declares no setupPolicy block.
func TestRuntimeSetupPolicyDefaults300sWhenUnset(t *testing.T) {
	runtimes := runtimestore.NewMemory()
	_ = runtimes.Create(context.Background(), runtimestore.Runtime{
		Name: "no-policy",
		Type: runtimestore.TypeAgent,
	})
	s := &Server{runtimes: runtimes}
	got := s.runtimeSetupPolicy(context.Background(), "no-policy")
	if got == nil {
		t.Fatal("runtimeSetupPolicy must return a §6.4 default when the runtime declares none")
	}
	if got.GetTimeoutSeconds() != DefaultSetupPolicyTimeoutSeconds {
		t.Errorf("default timeout = %d, want %d", got.GetTimeoutSeconds(), DefaultSetupPolicyTimeoutSeconds)
	}
	if got.GetOnTimeout() != string(runtimestore.SetupTimeoutFail) {
		t.Errorf("default onTimeout = %q, want fail", got.GetOnTimeout())
	}
}

// runtimeSetupPolicy honors an explicit setupPolicy block over the
// platform default. spec: §5.1 setupPolicy.
func TestRuntimeSetupPolicyHonorsExplicitTimeout(t *testing.T) {
	runtimes := runtimestore.NewMemory()
	_ = runtimes.Create(context.Background(), runtimestore.Runtime{
		Name:        "explicit",
		Type:        runtimestore.TypeAgent,
		SetupPolicy: &runtimestore.SetupPolicy{TimeoutSeconds: 600, OnTimeout: runtimestore.SetupTimeoutWarn},
	})
	s := &Server{runtimes: runtimes}
	got := s.runtimeSetupPolicy(context.Background(), "explicit")
	if got.GetTimeoutSeconds() != 600 || got.GetOnTimeout() != "warn" {
		t.Errorf("explicit setupPolicy = %+v, want 600s / warn", got)
	}
}

// runtimeSetupPolicy plumbs setupCommandPolicy.shell onto the adapter
// SetupPolicy.shell field so argv-mode can be selected per-runtime. The
// default (no policy declared) keeps shell=true (legacy behaviour). spec:
// §7.5 line 490 — F-7.5.2.
func TestRuntimeSetupPolicyShellFromSetupCommandPolicy(t *testing.T) {
	runtimes := runtimestore.NewMemory()
	_ = runtimes.Create(context.Background(), runtimestore.Runtime{
		Name:               "argv",
		Type:               runtimestore.TypeAgent,
		SetupCommandPolicy: &runtimestore.SetupCommandPolicy{Mode: runtimestore.SetupCommandModeAllowlist, Shell: false},
	})
	_ = runtimes.Create(context.Background(), runtimestore.Runtime{
		Name:               "shell",
		Type:               runtimestore.TypeAgent,
		SetupCommandPolicy: &runtimestore.SetupCommandPolicy{Mode: runtimestore.SetupCommandModeAllowlist, Shell: true},
	})
	_ = runtimes.Create(context.Background(), runtimestore.Runtime{
		Name: "no-policy",
		Type: runtimestore.TypeAgent,
	})
	s := &Server{runtimes: runtimes}
	if got := s.runtimeSetupPolicy(context.Background(), "argv"); got.GetShell() {
		t.Error("setupCommandPolicy.shell=false must yield SetupPolicy.shell=false")
	}
	if got := s.runtimeSetupPolicy(context.Background(), "shell"); !got.GetShell() {
		t.Error("setupCommandPolicy.shell=true must yield SetupPolicy.shell=true")
	}
	if got := s.runtimeSetupPolicy(context.Background(), "no-policy"); !got.GetShell() {
		t.Error("no setupCommandPolicy declared must yield SetupPolicy.shell=true (legacy)")
	}
}
