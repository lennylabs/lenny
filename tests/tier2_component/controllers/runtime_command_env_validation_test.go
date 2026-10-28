// SPDX-License-Identifier: MIT

//go:build component

package controllers_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
)

// cmdEnvImage is a digest-pinned image the §5.3 Runtime CRD pattern admits,
// so every rejection below is attributable to the command or env rule
// under test.
const cmdEnvImage = "registry.example.com/agent@sha256:" + digest64

// runtimeSpecObject builds an unstructured Runtime whose spec is the given
// map, so a case can send a value the typed client cannot express (an
// empty command list, which the typed omitempty field drops).
func runtimeSpecObject(name string, spec map[string]any) *unstructured.Unstructured {
	u := &unstructured.Unstructured{}
	u.SetAPIVersion(lennyv1.GroupVersion.String())
	u.SetKind("Runtime")
	u.SetName(name)
	base := map[string]any{"image": cmdEnvImage, "integrationLevel": "basic"}
	for k, v := range spec {
		base[k] = v
	}
	u.Object["spec"] = base
	return u
}

// TestRuntimeCRDCommandEnvValidation_spec_5_1 applies Runtime objects to a
// real kube-apiserver carrying the committed Runtime CRD and asserts that
// the API server enforces each §5.1 command and env rule: one rejected
// Runtime per branch of each rule, and the admitted placements, including a
// sidecar type: agent Runtime carrying both fields.
//
// spec: §5.1 (command), §5.1 (env).
// diagnosis: a failure means the committed Runtime CRD no longer carries a
// §5.1 command or env validation rule (or carries one that rejects a valid
// Runtime). Check the RuntimeSpec kubebuilder markers in
// pkg/apis/lenny/v1alpha1/runtime_types.go and regenerate the chart CRD.
func TestRuntimeCRDCommandEnvValidation_spec_5_1(t *testing.T) {
	env := sharedEnv.Start(t)
	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(lennyv1.AddToScheme(scheme))
	c, err := client.New(env.RESTConfig(), client.Options{Scheme: scheme})
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}
	ctx := context.Background()

	cmd := []any{"/usr/local/bin/agent"}
	rejected := []struct {
		name    string
		spec    map[string]any
		wantMsg string
	}{
		{"sidecar agent without command", map[string]any{"type": "agent", "deploymentModel": "sidecar"}, "command is required"},
		{"agent with default deploymentModel without command", map[string]any{"type": "agent"}, "command is required"},
		{"embedded agent with command", map[string]any{"type": "agent", "deploymentModel": "embedded", "command": cmd}, "forbidden on every other runtime"},
		{"mcp runtime with command", map[string]any{"type": "mcp", "command": cmd}, "forbidden on every other runtime"},
		{"empty command list", map[string]any{"type": "agent", "command": []any{}}, "spec.command"},
		{"empty command element", map[string]any{"type": "agent", "command": []any{""}}, "spec.command[0]"},
		{"relative first element", map[string]any{"type": "agent", "command": []any{"bin/agent"}}, "absolute path"},
		{"embedded agent with env", map[string]any{"type": "agent", "deploymentModel": "embedded", "env": []any{map[string]any{"name": "A", "value": "1"}}}, "env is forbidden"},
		{"mcp runtime with env", map[string]any{"type": "mcp", "env": []any{map[string]any{"name": "A", "value": "1"}}}, "env is forbidden"},
		{"env name with the reserved LENNY_ prefix", map[string]any{"type": "agent", "command": cmd, "env": []any{map[string]any{"name": "LENNY_SOCKET", "value": "x"}}}, "reserved for the platform"},
		{"env name starting with a digit", map[string]any{"type": "agent", "command": cmd, "env": []any{map[string]any{"name": "1VAR", "value": "x"}}}, "spec.env[0].name"},
		{"env name with a hyphen", map[string]any{"type": "agent", "command": cmd, "env": []any{map[string]any{"name": "MY-VAR", "value": "x"}}}, "spec.env[0].name"},
		{"empty env name", map[string]any{"type": "agent", "command": cmd, "env": []any{map[string]any{"name": "", "value": "x"}}}, "spec.env[0].name"},
		{"env entry without name", map[string]any{"type": "agent", "command": cmd, "env": []any{map[string]any{"value": "x"}}}, "name"},
		{"duplicate env name", map[string]any{"type": "agent", "command": cmd, "env": []any{
			map[string]any{"name": "DUP", "value": "1"}, map[string]any{"name": "DUP", "value": "2"},
		}}, "Duplicate value"},
	}
	for i, tc := range rejected {
		t.Run("rejects "+tc.name, func(t *testing.T) {
			obj := runtimeSpecObject(clusterName(t, "Runtime", fmt.Sprintf("cmdenv-reject-%d", i)), tc.spec)
			err := c.Create(ctx, obj)
			if err == nil {
				_ = c.Delete(ctx, obj)
				t.Fatalf("API server admitted %s, want an Invalid rejection", tc.name)
			}
			if !apierrors.IsInvalid(err) {
				t.Fatalf("error = %v, want an Invalid rejection", err)
			}
			if !strings.Contains(err.Error(), tc.wantMsg) {
				t.Errorf("error = %v, want it to mention %q", err, tc.wantMsg)
			}
		})
	}

	admitted := []struct {
		name string
		spec lennyv1.RuntimeSpec
	}{
		{"sidecar agent with command and env", lennyv1.RuntimeSpec{
			Type: "agent", DeploymentModel: "sidecar",
			Command: []string{"/usr/local/bin/agent", "--serve"},
			Env: []lennyv1.RuntimeEnvVar{
				{Name: "PATH", Value: "/opt/venv/bin:/usr/bin:/bin"},
				{Name: "_private_9", Value: ""},
			},
		}},
		{"agent with default deploymentModel and command", lennyv1.RuntimeSpec{Type: "agent", Command: []string{"/usr/local/bin/agent"}}},
		{"embedded agent without command", lennyv1.RuntimeSpec{Type: "agent", DeploymentModel: "embedded"}},
		{"mcp runtime without command", lennyv1.RuntimeSpec{Type: "mcp"}},
	}
	for i, tc := range admitted {
		t.Run("admits "+tc.name, func(t *testing.T) {
			spec := tc.spec
			spec.Image = cmdEnvImage
			spec.IntegrationLevel = "basic"
			rt := &lennyv1.Runtime{ObjectMeta: metav1.ObjectMeta{Name: clusterName(t, "Runtime", fmt.Sprintf("cmdenv-admit-%d", i))}, Spec: spec}
			if err := c.Create(ctx, rt); err != nil {
				t.Fatalf("API server rejected %s: %v", tc.name, err)
			}
			var got lennyv1.Runtime
			if err := c.Get(ctx, client.ObjectKey{Name: rt.Name}, &got); err != nil {
				t.Fatalf("get: %v", err)
			}
			if len(got.Spec.Command) != len(spec.Command) || len(got.Spec.Env) != len(spec.Env) {
				t.Errorf("stored command/env = %q/%+v, want %q/%+v", got.Spec.Command, got.Spec.Env, spec.Command, spec.Env)
			}
		})
	}
}
