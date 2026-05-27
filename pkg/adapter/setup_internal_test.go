// SPDX-License-Identifier: MIT

package adapter

import (
	"strings"
	"testing"
	"time"

	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

func TestSetupOptionsFromProtoNil(t *testing.T) {
	// No policy means no aggregate cap on the setup phase.
	got := setupOptionsFromProto(nil, "/workspace/current")
	if got.AggregateTimeout != 0 || got.FailOnAggregateTimeout {
		t.Errorf("setupOptionsFromProto(nil) = %+v, want a zero SetupOptions", got)
	}
	// F-7.5.8: even with a nil policy, the env whitelist is wired so a
	// setup command never inherits the adapter's process env.
	assertEnvWhitelist(t, got.Env)
}

func TestSetupOptionsFromProtoFail(t *testing.T) {
	got := setupOptionsFromProto(&adapterv1.SetupPolicy{TimeoutSeconds: 300, OnTimeout: "fail"}, "/workspace/current")
	if got.AggregateTimeout != 300*time.Second || !got.FailOnAggregateTimeout {
		t.Errorf("setupOptionsFromProto(fail) = %+v, want 300s / fail", got)
	}
	assertEnvWhitelist(t, got.Env)
}

func TestSetupOptionsFromProtoWarn(t *testing.T) {
	got := setupOptionsFromProto(&adapterv1.SetupPolicy{TimeoutSeconds: 120, OnTimeout: "warn"}, "/workspace/current")
	if got.AggregateTimeout != 120*time.Second || got.FailOnAggregateTimeout {
		t.Errorf("setupOptionsFromProto(warn) = %+v, want 120s / proceed", got)
	}
	assertEnvWhitelist(t, got.Env)
}

func TestSetupOptionsFromProtoEmptyDispositionIsFail(t *testing.T) {
	// §5.1: an empty onTimeout is the conservative fail default.
	got := setupOptionsFromProto(&adapterv1.SetupPolicy{TimeoutSeconds: 60}, "/workspace/current")
	if !got.FailOnAggregateTimeout {
		t.Error("an empty onTimeout must be treated as fail")
	}
}

// spec: §7.5 line 490 — F-7.5.2. A nil policy preserves the legacy
// shell=true (`/bin/sh -c`) execution mode so a runtime that does not
// declare a setupCommandPolicy at all still runs setup the legacy way.
func TestSetupOptionsFromProtoNilDefaultsShellTrue(t *testing.T) {
	got := setupOptionsFromProto(nil, "/workspace/current")
	if !got.Shell {
		t.Error("setupOptionsFromProto(nil) must default Shell=true (legacy /bin/sh -c)")
	}
}

// spec: §7.5 line 490 — F-7.5.2. An explicit Shell flag on the wire
// (proto3 bool) is mirrored onto SetupOptions so the runtime's
// setupCommandPolicy.shell choice survives the gateway → adapter hop.
func TestSetupOptionsFromProtoHonorsShellFlag(t *testing.T) {
	argv := setupOptionsFromProto(&adapterv1.SetupPolicy{Shell: false}, "/workspace/current")
	if argv.Shell {
		t.Error("setupOptionsFromProto with proto Shell=false must set SetupOptions.Shell=false")
	}
	shell := setupOptionsFromProto(&adapterv1.SetupPolicy{Shell: true}, "/workspace/current")
	if !shell.Shell {
		t.Error("setupOptionsFromProto with proto Shell=true must set SetupOptions.Shell=true")
	}
}

func assertEnvWhitelist(t *testing.T, env []string) {
	t.Helper()
	if env == nil {
		t.Fatal("setupOptionsFromProto must seed Env (F-7.5.8)")
	}
	for _, prefix := range []string{"PATH=", "HOME=", "USER="} {
		var found bool
		for _, e := range env {
			if strings.HasPrefix(e, prefix) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("env whitelist missing %q in %v", prefix, env)
		}
	}
}
