// SPDX-License-Identifier: MIT

package tier0_static

import (
	"fmt"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/helm"
)

const (
	// credentialsGuardNamespace is the credentials namespace every guard
	// render sets. It differs from the chart default so a template that
	// hard-codes the default renders its grant outside the guarded namespace.
	credentialsGuardNamespace = "lenny-guard-credentials"
	credentialsGuardRelease   = "lenny-system"
)

// secretGrant is one binding that gives its subjects a verb on Secrets in the
// evaluated namespace.
type secretGrant struct {
	Binding  string
	Role     string
	Subjects any
}

// stringEntries returns the string items of a parsed YAML list.
func stringEntries(v any) []string {
	items, _ := v.([]any)
	out := make([]string, 0, len(items))
	for _, item := range items {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// ruleMatchesSecrets reports whether a parsed RBAC rule reaches Secrets,
// whatever its verbs and resourceNames.
func ruleMatchesSecrets(rule map[string]any) bool {
	groups := stringEntries(rule["apiGroups"])
	resources := stringEntries(rule["resources"])
	return (slices.Contains(groups, "") || slices.Contains(groups, "*")) &&
		(slices.Contains(resources, "secrets") || slices.Contains(resources, "*"))
}

// roleReachesSecrets reports whether any rule of a rendered role matches.
func roleReachesSecrets(role helm.Manifest) bool {
	rules, _ := role.Raw["rules"].([]any)
	for _, r := range rules {
		if rule, ok := r.(map[string]any); ok && ruleMatchesSecrets(rule) {
			return true
		}
	}
	return false
}

// manifestNamespace returns a manifest's namespace, defaulting an unset one to
// the release namespace as `helm install` does.
func manifestNamespace(m helm.Manifest, release string) string {
	if m.Namespace == "" {
		return release
	}
	return m.Namespace
}

// bindingApplies reports whether a binding has effect in namespace ns.
func bindingApplies(binding helm.Manifest, ns, release string) bool {
	return binding.Kind == "ClusterRoleBinding" ||
		(binding.Kind == "RoleBinding" && manifestNamespace(binding, release) == ns)
}

// resolveRole finds the rendered role a binding's roleRef names: a ClusterRole
// by name, or a Role of that name in the binding's own namespace.
func resolveRole(rendered helm.Manifests, binding helm.Manifest, release string) (helm.Manifest, error) {
	ref, _ := binding.Raw["roleRef"].(map[string]any)
	kind, _ := ref["kind"].(string)
	name, _ := ref["name"].(string)
	for _, candidate := range rendered {
		if candidate.Kind != kind || candidate.Name != name {
			continue
		}
		if kind == "ClusterRole" ||
			(kind == "Role" && manifestNamespace(candidate, release) == manifestNamespace(binding, release)) {
			return candidate, nil
		}
	}
	return helm.Manifest{}, fmt.Errorf("%s %q: roleRef %s %q names no rendered role", binding.Kind, binding.Name, kind, name)
}

// secretGrantsIn evaluates every binding that applies in namespace ns. It
// returns the grants of Secret access there and one error per applying binding
// whose roleRef resolves to no rendered role.
func secretGrantsIn(rendered helm.Manifests, ns, release string) ([]secretGrant, []error) {
	var grants []secretGrant
	var unresolved []error
	for _, binding := range rendered {
		if !bindingApplies(binding, ns, release) {
			continue
		}
		role, err := resolveRole(rendered, binding, release)
		if err != nil {
			unresolved = append(unresolved, err)
			continue
		}
		if roleReachesSecrets(role) {
			grants = append(grants, secretGrant{Binding: binding.Name, Role: role.Name, Subjects: binding.Raw["subjects"]})
		}
	}
	return grants, unresolved
}

// grantBindingNames returns the sorted binding names of a grant set.
func grantBindingNames(grants []secretGrant) []string {
	names := make([]string, 0, len(grants))
	for _, g := range grants {
		names = append(names, g.Binding)
	}
	sort.Strings(names)
	return names
}

// assertCredentialGrants fails unless the grants in the guarded namespace are
// exactly the two chart bindings when the render holds the Token Service
// Deployment, and none otherwise.
func assertCredentialGrants(t *testing.T, rendered helm.Manifests) {
	t.Helper()
	grants, unresolved := secretGrantsIn(rendered, credentialsGuardNamespace, credentialsGuardRelease)
	for _, err := range unresolved {
		t.Error(err)
	}
	want := []string{}
	if _, ok := rendered.Find("Deployment", "lenny-token-service"); ok {
		want = []string{"lenny-gateway-credential-writer", "lenny-token-service-credentials"}
	}
	if got := grantBindingNames(grants); !reflect.DeepEqual(got, want) {
		t.Errorf("Secret grants in %s = %v, want %v; grants: %+v", credentialsGuardNamespace, got, want, grants)
	}
}

// TestCredentialsNamespaceSecretGrants holds the chart defaults and each preset
// to the rule that only the Token Service and the gateway reach Secrets in the
// credentials namespace. helm-unittest asserts one document at a time and
// cannot join a binding to its role, so the join runs here.
//
// spec: §4.9 "Credentials namespace." rule 7, §17.6.
func TestCredentialsNamespaceSecretGrants(t *testing.T) {
	helm.SkipUnlessAvailable(t)
	const chart = "../../charts/lenny"
	presets, err := filepath.Glob(filepath.Join(chart, "presets", "*.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(presets) == 0 {
		t.Fatalf("no presets under %s", chart)
	}
	for _, values := range append([]string{""}, presets...) {
		name := filepath.Base(values)
		if values == "" {
			name = "defaults"
		}
		t.Run(name, func(t *testing.T) {
			rendered := helm.Render(t, helm.Options{
				Chart:     chart,
				Namespace: credentialsGuardRelease,
				Values:    values,
				Set:       renderArgs("credentials.namespace=" + credentialsGuardNamespace),
			})
			assertCredentialGrants(t, rendered)
		})
	}
}

// syntheticRBAC builds a rendered manifest from its kind, name, namespace, and
// parsed body.
func syntheticRBAC(kind, name, namespace string, raw map[string]any) helm.Manifest {
	return helm.Manifest{Kind: kind, Name: name, Namespace: namespace, Raw: raw}
}

// syntheticRole builds a role body with one rule.
func syntheticRole(kind, name, namespace string, groups, resources, verbs, resourceNames []any) helm.Manifest {
	rule := map[string]any{"apiGroups": groups, "resources": resources, "verbs": verbs}
	if resourceNames != nil {
		rule["resourceNames"] = resourceNames
	}
	return syntheticRBAC(kind, name, namespace, map[string]any{"rules": []any{rule}})
}

// syntheticBinding builds a binding body that names a role.
func syntheticBinding(kind, name, namespace, roleKind, roleName string) helm.Manifest {
	return syntheticRBAC(kind, name, namespace, map[string]any{
		"roleRef":  map[string]any{"kind": roleKind, "name": roleName},
		"subjects": []any{map[string]any{"kind": "ServiceAccount", "name": "alice"}},
	})
}

// TestSecretGrantEvaluator runs the evaluator over synthetic manifests, one
// case per matching and binding branch, so a passing chart render cannot hide
// an evaluator that matches nothing.
//
// spec: §4.9 "Credentials namespace." rule 7.
func TestSecretGrantEvaluator(t *testing.T) {
	const ns = "creds"
	secrets := func(kind, name, namespace string) helm.Manifest {
		return syntheticRole(kind, name, namespace, []any{""}, []any{"secrets"}, []any{"get"}, nil)
	}
	cases := []struct {
		name           string
		namespace      string
		manifests      helm.Manifests
		wantBindings   []string
		wantUnresolved int
	}{
		{
			name: "core group and secrets resource match",
			manifests: helm.Manifests{
				secrets("Role", "r", ns), syntheticBinding("RoleBinding", "b", ns, "Role", "r"),
			},
			wantBindings: []string{"b"},
		},
		{
			name: "wildcard group matches",
			manifests: helm.Manifests{
				syntheticRole("Role", "r", ns, []any{"*"}, []any{"secrets"}, []any{"get"}, nil),
				syntheticBinding("RoleBinding", "b", ns, "Role", "r"),
			},
			wantBindings: []string{"b"},
		},
		{
			name: "wildcard resource matches",
			manifests: helm.Manifests{
				syntheticRole("Role", "r", ns, []any{""}, []any{"*"}, []any{"get"}, nil),
				syntheticBinding("RoleBinding", "b", ns, "Role", "r"),
			},
			wantBindings: []string{"b"},
		},
		{
			name: "another api group does not match",
			manifests: helm.Manifests{
				syntheticRole("Role", "r", ns, []any{"lenny.dev"}, []any{"*"}, []any{"get"}, nil),
				syntheticBinding("RoleBinding", "b", ns, "Role", "r"),
			},
			wantBindings: []string{},
		},
		{
			name: "another core resource does not match",
			manifests: helm.Manifests{
				syntheticRole("Role", "r", ns, []any{""}, []any{"pods"}, []any{"*"}, nil),
				syntheticBinding("RoleBinding", "b", ns, "Role", "r"),
			},
			wantBindings: []string{},
		},
		{
			name: "any verb matches",
			manifests: helm.Manifests{
				syntheticRole("Role", "r", ns, []any{""}, []any{"secrets"}, []any{"watch"}, nil),
				syntheticBinding("RoleBinding", "b", ns, "Role", "r"),
			},
			wantBindings: []string{"b"},
		},
		{
			name: "resourceNames do not narrow a match",
			manifests: helm.Manifests{
				syntheticRole("Role", "r", ns, []any{""}, []any{"secrets"}, []any{"get"}, []any{"one"}),
				syntheticBinding("RoleBinding", "b", ns, "Role", "r"),
			},
			wantBindings: []string{"b"},
		},
		{
			name: "cluster role binding applies in every namespace",
			manifests: helm.Manifests{
				secrets("ClusterRole", "r", ""), syntheticBinding("ClusterRoleBinding", "b", "", "ClusterRole", "r"),
			},
			wantBindings: []string{"b"},
		},
		{
			name: "role binding to a cluster role applies in its namespace",
			manifests: helm.Manifests{
				secrets("ClusterRole", "r", ""), syntheticBinding("RoleBinding", "b", ns, "ClusterRole", "r"),
			},
			wantBindings: []string{"b"},
		},
		{
			name: "role binding in another namespace does not apply",
			manifests: helm.Manifests{
				secrets("ClusterRole", "r", ""), syntheticBinding("RoleBinding", "b", "elsewhere", "ClusterRole", "r"),
			},
			wantBindings: []string{},
		},
		{
			name: "role binding with no namespace is outside the credentials namespace",
			manifests: helm.Manifests{
				secrets("Role", "r", ""), syntheticBinding("RoleBinding", "b", "", "Role", "r"),
			},
			wantBindings: []string{},
		},
		{
			name:      "role binding with no namespace is in the release namespace",
			namespace: credentialsGuardRelease,
			manifests: helm.Manifests{
				secrets("Role", "r", credentialsGuardRelease), syntheticBinding("RoleBinding", "b", "", "Role", "r"),
			},
			wantBindings: []string{"b"},
		},
		{
			name: "a role of the same name in another namespace leaves the roleRef unresolved",
			manifests: helm.Manifests{
				secrets("Role", "r", "elsewhere"), syntheticBinding("RoleBinding", "b", ns, "Role", "r"),
			},
			wantBindings:   []string{},
			wantUnresolved: 1,
		},
		{
			name:           "a roleRef that names no rendered role is reported",
			manifests:      helm.Manifests{syntheticBinding("ClusterRoleBinding", "b", "", "ClusterRole", "absent")},
			wantBindings:   []string{},
			wantUnresolved: 1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			namespace := tc.namespace
			if namespace == "" {
				namespace = ns
			}
			grants, unresolved := secretGrantsIn(tc.manifests, namespace, credentialsGuardRelease)
			if got := grantBindingNames(grants); !reflect.DeepEqual(got, tc.wantBindings) {
				t.Errorf("grants = %v, want %v", got, tc.wantBindings)
			}
			if len(unresolved) != tc.wantUnresolved {
				t.Errorf("unresolved = %v, want %d", unresolved, tc.wantUnresolved)
			}
		})
	}
}
