// SPDX-License-Identifier: MIT

package main

import (
	"flag"
	"os"
	"testing"

	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

// TestRuntimeClassOverrideFlagsReachTheGatewayMap_spec_17_5 pins that the
// gateway binds the controller's --standard-runtime-class,
// --sandboxed-runtime-class, and --microvm-runtime-class flags, which the
// chart renders for both binaries from runtimeClasses.profiles.<profile>.name, and that
// runtimeClassOverrides turns them into the map the binder, the session
// server, and the recycle seams label runtime_class through. With no flag set
// every profile keeps its default name.
//
// spec: 17.5 (the per-profile RuntimeClass name override), 16.1 (Metrics) the
// runtime_class label, 5.3 (Isolation Profiles).
func TestRuntimeClassOverrideFlagsReachTheGatewayMap_spec_17_5(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want map[isolation.Profile]string
	}{
		{name: "no flag", want: map[isolation.Profile]string{
			isolation.ProfileStandard: "runc", isolation.ProfileSandboxed: "gvisor", isolation.ProfileMicrovm: "kata",
		}},
		{
			name: "sandboxed and microvm set",
			args: []string{"--sandboxed-runtime-class=runsc", "--microvm-runtime-class=kata-qemu"},
			want: map[isolation.Profile]string{
				isolation.ProfileStandard: "runc", isolation.ProfileSandboxed: "runsc", isolation.ProfileMicrovm: "kata-qemu",
			},
		},
		{
			name: "all set",
			args: []string{"--standard-runtime-class=crun", "--sandboxed-runtime-class=runsc", "--microvm-runtime-class=kata-fc"},
			want: map[isolation.Profile]string{
				isolation.ProfileStandard: "crun", isolation.ProfileSandboxed: "runsc", isolation.ProfileMicrovm: "kata-fc",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			savedCmdLine := flag.CommandLine
			savedArgs := os.Args
			t.Cleanup(func() {
				flag.CommandLine = savedCmdLine
				os.Args = savedArgs
			})
			flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ContinueOnError)
			os.Args = []string{savedArgs[0]}
			for _, env := range []string{"LENNY_STANDARD_RUNTIME_CLASS", "LENNY_SANDBOXED_RUNTIME_CLASS", "LENNY_MICROVM_RUNTIME_CLASS"} {
				t.Setenv(env, "")
			}

			f := &gatewayFlags{}
			f.registerPolicyFlags()
			if err := flag.CommandLine.Parse(tc.args); err != nil {
				t.Fatalf("parse: %v", err)
			}
			w := &gatewayWiring{f: f}
			overrides := w.runtimeClassOverrides()
			for p, want := range tc.want {
				if got, ok := isolation.ResolveRuntimeClassName(p, overrides); !ok || got != want {
					t.Errorf("profile %q maps to %q, want %q", p, got, want)
				}
			}
		})
	}
}
