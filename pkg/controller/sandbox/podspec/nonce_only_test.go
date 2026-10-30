// SPDX-License-Identifier: MIT

package podspec_test

import (
	"testing"

	"k8s.io/utils/ptr"

	"github.com/lennylabs/lenny/pkg/controller/sandbox/podspec"
)

const requireSoPeercredFalseFlag = "--require-so-peercred=false"

// TestBuildSidecarRendersNonceOnlyFlagOnlyForExplicitFalse asserts the §4.7
// activation render: the sidecar adapter container carries
// --require-so-peercred=false only when the resolved
// Inputs.RequireSoPeercred is an explicit false, and the flag is absent for a
// nil or true value so the adapter default of true (require SO_PEERCRED)
// governs.
//
// spec: §4.7 (nonce-only mode activation), §4.3 (render path).
func TestBuildSidecarRendersNonceOnlyFlagOnlyForExplicitFalse(t *testing.T) {
	cases := map[string]struct {
		value      *bool
		wantRender bool
	}{
		"explicit false renders the flag": {value: ptr.To(false), wantRender: true},
		"nil renders no flag":             {value: nil, wantRender: false},
		"explicit true renders no flag":   {value: ptr.To(true), wantRender: false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			in := inputs()
			in.DeploymentModel = "sidecar"
			in.RequireSoPeercred = tc.value
			pod, err := podspec.Build(in)
			if err != nil {
				t.Fatalf("Build: %v", err)
			}
			args := container(t, pod, "adapter").Args
			got := hasArg(args, requireSoPeercredFalseFlag)
			if got != tc.wantRender {
				t.Errorf("adapter args %v: rendered %q = %v, want %v",
					args, requireSoPeercredFalseFlag, got, tc.wantRender)
			}
		})
	}
}

// TestBuildEmbeddedNeverRendersNonceOnlyFlag asserts the §4.1 constraint that
// the embedded model has no adapter-agent socket boundary, so buildEmbedded
// renders no --require-so-peercred flag regardless of the resolved value. An
// embedded runtime carrying requireSoPeercred: false is rejected upstream by
// the RuntimeReconciler; the builder is structurally inert for it here.
//
// spec: §4.7 (nonce-only mode activation), §4.1 (sidecar-only constraint).
func TestBuildEmbeddedNeverRendersNonceOnlyFlag(t *testing.T) {
	for _, value := range []*bool{nil, ptr.To(true), ptr.To(false)} {
		in := inputs()
		in.DeploymentModel = "embedded"
		in.RequireSoPeercred = value
		pod, err := podspec.Build(in)
		if err != nil {
			t.Fatalf("Build(%v): %v", value, err)
		}
		args := container(t, pod, "runtime").Args
		if hasArgPrefix(args, "--require-so-peercred") {
			t.Errorf("embedded runtime args %v must not carry --require-so-peercred (value=%v)", args, value)
		}
	}
}
