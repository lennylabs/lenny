// SPDX-License-Identifier: MIT

package v1alpha1_test

import (
	"testing"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
)

// TestRuntimeSpecRestartable_spec_5_1 covers the §5.1 derivation of the
// restartable flag: true only for a runtime that carries command, has
// executionMode session (an empty mode defaults to session), and does not
// declare capabilities.preConnect: true. Each row breaks one conjunct.
//
// spec: §5.1 (command), §5.2 (recycle.runtimeProcess).
func TestRuntimeSpecRestartable_spec_5_1(t *testing.T) {
	cmd := []string{"/usr/local/bin/agent"}
	for _, c := range []struct {
		name string
		spec lennyv1.RuntimeSpec
		want bool
	}{
		{"command and empty mode", lennyv1.RuntimeSpec{Command: cmd}, true},
		{"command and session mode", lennyv1.RuntimeSpec{Command: cmd, ExecutionMode: "session"}, true},
		{"command and preConnect false", lennyv1.RuntimeSpec{Command: cmd, Capabilities: &lennyv1.RuntimeCapabilitiesCRD{PreConnect: false}}, true},
		{"no command", lennyv1.RuntimeSpec{ExecutionMode: "session"}, false},
		{"empty command list", lennyv1.RuntimeSpec{Command: []string{}}, false},
		{"service mode", lennyv1.RuntimeSpec{Command: cmd, ExecutionMode: "service"}, false},
		{"preConnect true", lennyv1.RuntimeSpec{Command: cmd, Capabilities: &lennyv1.RuntimeCapabilitiesCRD{PreConnect: true}}, false},
		{"embedded runtime with no command", lennyv1.RuntimeSpec{DeploymentModel: "embedded", ExecutionMode: "session"}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := c.spec.Restartable(); got != c.want {
				t.Errorf("Restartable() = %v, want %v", got, c.want)
			}
		})
	}
}

// TestRuntimeDeepCopyIsolatesCommandAndEnv_spec_5_1 catches a shallow copy
// of the §5.1 command and env slices in the generated DeepCopy: a copy that
// shared either backing array would let one Runtime's argv or environment
// leak into another.
//
// spec: §5.1 (command), §5.1 (env).
func TestRuntimeDeepCopyIsolatesCommandAndEnv_spec_5_1(t *testing.T) {
	orig := &lennyv1.Runtime{Spec: lennyv1.RuntimeSpec{
		Type:    "agent",
		Command: []string{"/usr/local/bin/agent", "--serve"},
		Env:     []lennyv1.RuntimeEnvVar{{Name: "PATH", Value: "/opt/venv/bin:/usr/bin"}},
	}}
	cp := orig.DeepCopy()
	cp.Spec.Command[0] = "/bin/other"
	cp.Spec.Env[0].Value = "changed"
	if orig.Spec.Command[0] != "/usr/local/bin/agent" {
		t.Errorf("DeepCopy shared the Command slice; original = %q", orig.Spec.Command)
	}
	if orig.Spec.Env[0].Value != "/opt/venv/bin:/usr/bin" {
		t.Errorf("DeepCopy shared the Env slice; original = %+v", orig.Spec.Env)
	}
}
