// SPDX-License-Identifier: MIT

package v1alpha1_test

import (
	"testing"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
)

// TestRuntimeSpecCommandAndEnvCopyIndependently pins that a copy of a Runtime
// spec owns its command argv and env list, so the RuntimeReconciler and the
// pod builder can mutate a copy read from the informer cache without
// rewriting the cached object the next reconcile reads.
//
// spec: §5.1 (command; env)
func TestRuntimeSpecCommandAndEnvCopyIndependently_spec_5_1(t *testing.T) {
	orig := &lennyv1.RuntimeSpec{
		Command: []string{"/usr/local/bin/agent", "--serve"},
		Env:     []lennyv1.RuntimeEnvVar{{Name: "MODE", Value: "prod"}},
	}
	cp := orig.DeepCopy()
	cp.Command[0] = "/bin/other"
	cp.Env[0].Value = "dev"
	if orig.Command[0] != "/usr/local/bin/agent" || orig.Env[0].Value != "prod" {
		t.Fatalf("mutating the copy rewrote the original: %+v", orig)
	}

	ev := orig.Env[0].DeepCopy()
	ev.Name = "OTHER"
	if orig.Env[0].Name != "MODE" {
		t.Fatalf("mutating an env var copy rewrote the original: %+v", orig.Env[0])
	}
	var nilVar *lennyv1.RuntimeEnvVar
	if nilVar.DeepCopy() != nil {
		t.Fatal("DeepCopy of a nil env var is not nil")
	}
}
