// SPDX-License-Identifier: MIT

package values_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// This file is the unit check that the chart's ClusterRoles
// contain every verb the gateway and controller binaries need.
//
// Regression source: commit f54b7bb. The gateway issued
// `client.Delete(... podclaim ...)` while the gateway ClusterRole
// did not grant `delete` on `podclaims`. Every load_cloud
// terminate returned HTTP 200 to the caller while leaking the claim,
// and the pool saturated within seconds. The chart RBAC missing one
// verb is one of the highest-leverage bug classes: it surfaces only
// under load and only when the affected RBAC path actually fires.
//
// The check in this file asserts that a known-required set of
// {verb, resource} pairs is present in each ClusterRole template
// the gateway and controller bind to. The list is the source of
// truth derived from the codebase; whenever a new client.Verb call
// site is added, the test fails until the verb is added to both the
// chart and the assertion table here.
//
// A future enhancement walks the Go AST to derive the {verb, resource}
// set automatically. The Wave 4 cut is the hand-curated table.
//
// TESTING.md §6.1 (Wave 4 unit-test uplift).

// requiredVerbs is the hand-curated set of {role, resource, verb}
// triples the chart's ClusterRole templates must grant. The set tracks
// the §4.6.3 CRD-field-ownership decomposition: the gateway creates and
// deletes per-pod PodClaims and writes the claim binding state on
// podclaims/status, while the WarmPoolController is the sole writer
// of AgentPod.status. When a verb is added or removed in §4.6.3, the
// reviewer updates the entry here in the same change.
//
// spec: 4.6.3 (gateway and controller ServiceAccount RBAC grants)
var requiredVerbs = []struct {
	Role     string
	Resource string
	Verbs    []string
}{
	// §4.6.3: gateway creates/gets/deletes per-pod PodClaims. It gets
	// a claim to read the binding state and the resourceVersion guarding
	// the hold-expiry DELETE precondition. Intra-pod capacity is gated by
	// the §5.2 Redis slot counter with a §12.4 Postgres
	// GetActiveSlotsByPod fallback, so the gateway does not list or watch
	// claims and the §4.6.3 grant is create/get/delete.
	{"lenny-gateway", "podclaims", []string{"get", "create", "delete"}},
	// §4.6.3: gateway is the sole writer of the claim binding state on
	// the podclaims/status subresource (binding phase, transition
	// time, rewarmStartedAt, holdExpiresAt).
	{"lenny-gateway", "podclaims/status", []string{"get", "patch"}},
	// §4.6.3: gateway reads AgentPod for pod selection during claim. It
	// holds no patch/watch on the main resource and no agentpods/status
	// grant — AgentPod.status is written solely by the WarmPoolController.
	{"lenny-gateway", "agentpods", []string{"get", "list"}},
	// §4.6.3: controller creates sandboxes from templates and holds the
	// full main-resource grant. `patch` backs the §4.7 nonce-only
	// AgentPod.spec.requireSoPeercred carrier SSA write (Server-Side Apply
	// issues an HTTP PATCH, for which `update` is insufficient): the
	// WarmPoolController records the render decision on the carrier before
	// it creates the pod, and that write is forbidden without `patch` on
	// the main resource. Pinning the full granted set here makes this the
	// durable guard that a future RBAC narrowing cannot silently drop
	// `patch` (a positive-only [get, list, watch, create] entry would not
	// catch a dropped patch).
	{"lenny-controller", "agentpods", []string{"get", "list", "watch", "create", "update", "patch", "delete"}},
	// §4.6.3: WarmPoolController is the sole writer of AgentPod.status.
	{"lenny-controller", "agentpods/status", []string{"get", "update", "patch"}},
	// §4.6.1 / §4.6.3: WarmPoolController watches PodClaims (the
	// occupancy projection) and lists/deletes them (orphan-claim GC).
	{"lenny-controller", "podclaims", []string{"get", "list", "watch", "delete"}},
	// §4.6.3: controller reads pool/template/runtime definitions.
	{"lenny-controller", "podsets", []string{"get", "list", "watch"}},
	{"lenny-controller", "podsets", []string{"get", "list", "watch"}},
	{"lenny-controller", "runtimes", []string{"get", "list", "watch"}},
}

func TestClusterRolesGrantRequiredVerbs(t *testing.T) {
	root := repoRoot(t)
	templates := map[string]string{
		"lenny-controller": filepath.Join(root, "charts/lenny/templates/controller-rbac.yaml"),
		"lenny-gateway":    filepath.Join(root, "charts/lenny/templates/gateway-deployment.yaml"),
	}
	for _, req := range requiredVerbs {
		req := req
		t.Run(req.Role+"/"+req.Resource, func(t *testing.T) {
			tmplPath, ok := templates[req.Role]
			if !ok {
				t.Fatalf("no template registered for role %q; update pkg/chart/values/cluster_role_verbs_test.go", req.Role)
			}
			body, err := os.ReadFile(tmplPath)
			if err != nil {
				t.Fatalf("read %s: %v", tmplPath, err)
			}
			missing := missingVerbs(string(body), req.Role, req.Resource, req.Verbs)
			if len(missing) > 0 {
				t.Errorf("§6.1 violated: role=%s resource=%s missing verbs: %v\n  template: %s",
					req.Role, req.Resource, missing, tmplPath)
			}
		})
	}
}

// forbiddenVerbs is the set of {role, resource, verb} triples the
// chart's ClusterRole templates must NOT grant. It pins the fail-closed
// half of the §4.6.3 pod-status ownership decomposition: the gateway is
// projection-blind to AgentPod.status (the WarmPoolController is its sole
// writer) and reads the AgentPod main resource read-only, so the gateway
// holds no write verb on `agentpods` and no grant whatsoever on the
// `agentpods/status` subresource. It also pins the converged PodClaim
// grant to create/get/delete: the gateway holds no list or watch verb,
// because §5.2 intra-pod capacity is gated by the Redis slot counter with
// a §12.4 Postgres GetActiveSlotsByPod fallback. A positive-only verb
// matrix would still pass if a later edit
// re-added an AgentPod.status write or the SSA claim-list capacity gate to
// the gateway; this table catches that regression. An empty Verbs slice
// asserts the resource carries no rule for the role at all.
//
// spec: 4.6.3 (gateway loses every AgentPod.status write surface), 5.2
// (intra-pod capacity gate)
var forbiddenVerbs = []struct {
	Role     string
	Resource string
	Verbs    []string
}{
	// §4.6.3: the gateway is the sole reader (not writer) of AgentPod; it
	// must hold no write or watch verb on the main resource. A re-added
	// patch/update/watch here would let the gateway mutate pod state or
	// re-establish the AgentPod.status write path the decomposition removed.
	{"lenny-gateway", "agentpods", []string{"create", "update", "patch", "delete", "watch"}},
	// §4.6.3 / §5.2: the gateway's PodClaim grant is create/get/delete.
	// It must hold no list or watch verb. Intra-pod capacity is gated by
	// the Redis slot counter with a Postgres GetActiveSlotsByPod fallback,
	// so a re-added list/watch would re-establish the SSA claim-list
	// capacity gate the converged design does not use.
	{"lenny-gateway", "podclaims", []string{"list", "watch"}},
	// §4.6.3: the gateway holds no grant on the AgentPod.status
	// subresource. An empty want asserts the absence of any rule, so even
	// a read (`get`) grant on agentpods/status fails this test.
	{"lenny-gateway", "agentpods/status", nil},
}

// TestGatewayHasNoAgentPodStatusWrite asserts the fail-closed half of the
// §4.6.3 decomposition: the gateway holds no AgentPod.status write surface
// and no write verb on the AgentPod main resource. diagnosis: a failure
// here means the gateway ClusterRole regained an AgentPod or AgentPod.status
// write grant the WarmPoolController must own exclusively, breaking the
// single-writer ownership boundary that keeps occupancy a claim projection.
//
// spec: 4.6.3 (gateway and controller ServiceAccount RBAC grants)
func TestGatewayHasNoAgentPodStatusWrite(t *testing.T) {
	root := repoRoot(t)
	tmplPath := filepath.Join(root, "charts/lenny/templates/gateway-deployment.yaml")
	body, err := os.ReadFile(tmplPath)
	if err != nil {
		t.Fatalf("read %s: %v", tmplPath, err)
	}
	for _, req := range forbiddenVerbs {
		req := req
		t.Run(req.Role+"/"+req.Resource, func(t *testing.T) {
			granted := grantedVerbs(string(body), req.Role, req.Resource)
			if len(req.Verbs) == 0 {
				if len(granted) > 0 {
					t.Errorf("§4.6.3 violated: role=%s must hold no grant on %s but has verbs: %v\n  template: %s",
						req.Role, req.Resource, granted, tmplPath)
				}
				return
			}
			grantedSet := map[string]bool{}
			for _, v := range granted {
				grantedSet[v] = true
			}
			for _, v := range req.Verbs {
				if grantedSet[v] {
					t.Errorf("§4.6.3 violated: role=%s resource=%s must not grant verb %q\n  template: %s",
						req.Role, req.Resource, v, tmplPath)
				}
			}
		})
	}
}

// grantedVerbs returns the verbs granted on `resource` within the given
// ClusterRole in the Helm template, or nil when the role grants no rule
// for the resource. It shares the lenient rule-walking that missingVerbs
// uses so positive and negative assertions read the same YAML structure.
func grantedVerbs(template, roleName, resource string) []string {
	roleStart := indexOfRole(template, roleName)
	if roleStart < 0 {
		return nil
	}
	end := strings.Index(template[roleStart:], "\n---")
	var block string
	if end < 0 {
		block = template[roleStart:]
	} else {
		block = template[roleStart : roleStart+end]
	}
	granted := map[string]bool{}
	for _, rule := range splitRules(block) {
		if !ruleContainsResource(rule, resource) {
			continue
		}
		for _, v := range extractVerbs(rule) {
			granted[v] = true
		}
	}
	out := make([]string, 0, len(granted))
	for v := range granted {
		out = append(out, v)
	}
	return out
}

// missingVerbs returns the verbs from `want` that are not granted on
// `resource` within the given ClusterRole in the Helm template.
//
// The implementation is tolerant of the chart's exact YAML shape
// (named roles, multiple rules, multi-resource rules). It does not
// fully parse the YAML; it locates the role section by name and the
// rule entries by `resources: [...]` membership.
func missingVerbs(template, roleName, resource string, want []string) []string {
	// Locate the ClusterRole block for roleName. The chart uses
	// `name: lenny-controller` or `name: lenny-gateway` directly.
	roleStart := indexOfRole(template, roleName)
	if roleStart < 0 {
		return want
	}
	// Take from roleStart to the next `---` document separator (the
	// next YAML doc), or end of file.
	end := strings.Index(template[roleStart:], "\n---")
	var block string
	if end < 0 {
		block = template[roleStart:]
	} else {
		block = template[roleStart : roleStart+end]
	}
	// Walk every `- apiGroups:`/`resources:`/`verbs:` rule and
	// collect the verbs whose `resources:` array contains `resource`.
	rules := splitRules(block)
	granted := map[string]bool{}
	for _, rule := range rules {
		if !ruleContainsResource(rule, resource) {
			continue
		}
		for _, v := range extractVerbs(rule) {
			granted[v] = true
		}
	}
	var missing []string
	for _, v := range want {
		if !granted[v] {
			missing = append(missing, v)
		}
	}
	return missing
}

// indexOfRole locates `name: <roleName>` within a kind: ClusterRole
// metadata block. Returns -1 when not found.
func indexOfRole(template, roleName string) int {
	pattern := "name: " + roleName
	idx := 0
	for {
		k := strings.Index(template[idx:], pattern)
		if k < 0 {
			return -1
		}
		abs := idx + k
		// Confirm it's under metadata: of a ClusterRole. Look back
		// for `kind: ClusterRole` within the preceding 200 bytes.
		look := abs - 400
		if look < 0 {
			look = 0
		}
		if strings.Contains(template[look:abs], "kind: ClusterRole") {
			return abs
		}
		idx = abs + len(pattern)
	}
}

func splitRules(block string) []string {
	var rules []string
	cur := ""
	for _, line := range strings.Split(block, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "- apiGroups:") || strings.HasPrefix(strings.TrimSpace(line), "- nonResourceURLs:") {
			if cur != "" {
				rules = append(rules, cur)
			}
			cur = line + "\n"
		} else if cur != "" {
			cur += line + "\n"
		}
	}
	if cur != "" {
		rules = append(rules, cur)
	}
	return rules
}

func ruleContainsResource(rule, resource string) bool {
	// resources: ["agentpods", "..."]  or  resources:\n  - agentpods
	if strings.Contains(rule, `"`+resource+`"`) {
		return true
	}
	// Use a word-boundary regex so "agentpods" does not match in
	// "agentpods/status".
	re := regexp.MustCompile(`\b` + regexp.QuoteMeta(resource) + `\b`)
	for _, line := range strings.Split(rule, "\n") {
		ls := strings.TrimSpace(line)
		if strings.HasPrefix(ls, "resources:") || strings.HasPrefix(ls, "- ") {
			if re.MatchString(ls) {
				// Don't match `<resource>/status` (different resource).
				if !strings.Contains(ls, resource+"/") {
					return true
				}
			}
		}
	}
	return false
}

func extractVerbs(rule string) []string {
	idx := strings.Index(rule, "verbs:")
	if idx < 0 {
		return nil
	}
	tail := rule[idx:]
	end := strings.Index(tail, "\n")
	var line string
	if end < 0 {
		line = tail
	} else {
		line = tail[:end]
	}
	// Capture every quoted string on the line.
	re := regexp.MustCompile(`"([a-z]+)"`)
	matches := re.FindAllStringSubmatch(line, -1)
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		out = append(out, m[1])
	}
	return out
}
