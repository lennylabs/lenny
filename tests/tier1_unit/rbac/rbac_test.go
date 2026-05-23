// SPDX-License-Identifier: MIT

package rbac

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// requiredVerbs is the hand-curated set of {role, resource, verb}
// triples the Wave 4 cut asserts the chart's ClusterRole templates
// grant. Every entry corresponds to an actual client.<Verb> call site
// in pkg/gateway or pkg/controller; when a call site is added, the
// reviewer adds the verb here in the same PR.
var requiredVerbs = []struct {
	Role     string
	Resource string
	Verbs    []string
}{
	// §5.2: gateway dispatches against sandboxclaims, including the
	// f54b7bb-mandated delete on slot release.
	{"lenny-gateway", "sandboxclaims", []string{"get", "list", "watch", "create", "delete"}},
	// §5.2: gateway patches sandbox metadata (tenant pinning).
	{"lenny-gateway", "sandboxes", []string{"get", "list", "watch", "patch"}},
	// §4.6.3: controller creates sandboxes from templates.
	{"lenny-controller", "sandboxes", []string{"get", "list", "watch", "create"}},
	// §4.6.3: controller reads pool/template/runtime definitions.
	{"lenny-controller", "sandboxtemplates", []string{"get", "list", "watch"}},
	{"lenny-controller", "sandboxwarmpools", []string{"get", "list", "watch"}},
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
				t.Fatalf("no template registered for role %q; update tests/tier1_unit/rbac/rbac_test.go", req.Role)
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
	// resources: ["sandboxes", "..."]  or  resources:\n  - sandboxes
	if strings.Contains(rule, `"`+resource+`"`) {
		return true
	}
	// Use a word-boundary regex so "sandboxes" does not match in
	// "sandboxes/status".
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

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	// tests/tier1_unit/rbac/rbac_test.go → repo root is three dirs up.
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", ".."))
}
