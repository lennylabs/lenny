// SPDX-License-Identifier: MIT

package journeys

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The journeys package is contributor tooling for the persona journey
// tier. No specification section governs it, so its tests carry no spec
// annotation.

const minimalScenario = `name: demo
persona: operator
setup: lenny
docs: [docs/operator-guide/installation.md]
goal: Install it.
tools: [kubectl]
postconditions:
  - name: ready
    kubectl: [get, pods]
    expect: {contains: Running}
`

func TestParseScenarioReadsEveryField(t *testing.T) {
	src := `name: demo
persona: tenant-admin
summary: One line.
target: kind
setup: lenny
credentials: [kubeconfig, gateway-url, admin-token]
prepare: ["sessions-allowed:default"]
docs: [docs/api/admin.md, docs/tutorials/first-session.md]
goal: |
  Create a tenant.
steps: [one, two]
environment: Facts.
tools: [kubectl, lenny-ctl]
timeLimit: 20m
postconditions:
  - name: tenant-exists
    description: It exists.
    within: 2m
    api: {method: GET, path: /v1/admin/tenants/x, as: platform-admin}
    expect: {path: id, equals: x}
  - name: listed
    api: {method: GET, path: /v1/sessions, as: default/u/user}
    forAny:
      items: items.*.id
      api: {method: GET, path: "/v1/sessions/{{item}}/transcript", as: default/u/user}
      expect: {matches: 'echo'}
`
	s, err := ParseScenario([]byte(src), "tests/journeys/demo.yaml")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if s.Persona != "tenant-admin" || s.TargetName() != TargetKind || s.Setup != SetupLenny {
		t.Errorf("persona, target, setup = %q, %q, %q", s.Persona, s.TargetName(), s.Setup)
	}
	if len(s.Credentials) != 3 || len(s.Prepare) != 1 || len(s.Docs) != 2 || len(s.Steps) != 2 || len(s.Tools) != 2 {
		t.Errorf("lists not read: %+v", s)
	}
	if s.Limit() != 20*time.Minute {
		t.Errorf("limit = %s, want 20m", s.Limit())
	}
	if p := s.Postconditions[0]; time.Duration(p.Within) != 2*time.Minute || p.API.As != "platform-admin" || *p.Expect.Equals != "x" {
		t.Errorf("first postcondition not read: %+v", p)
	}
	if f := s.Postconditions[1].ForAny; f == nil || f.Items != "items.*.id" || f.API == nil {
		t.Errorf("forAny not read: %+v", f)
	}
}

func TestScenarioDefaultsTargetAndTimeLimit(t *testing.T) {
	s, err := ParseScenario([]byte(minimalScenario), "demo.yaml")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if s.TargetName() != TargetKind {
		t.Errorf("target = %q, want kind", s.TargetName())
	}
	if s.Limit() != DefaultTimeLimit {
		t.Errorf("limit = %s, want the default", s.Limit())
	}
}

func TestParseScenarioRefusesInvalidScenarios(t *testing.T) {
	cases := []struct {
		name, edit, from, want string
	}{
		{"unknown field", "goal: Install it.", "goal: Install it.\ncolour: blue", "field colour not found"},
		{"name differs from file", "name: demo", "name: other", "differs from the file name"},
		{"bad name", "name: demo", "name: Demo_1", "not lowercase words"},
		{"bad setup", "setup: lenny", "setup: cloud", "setup \"cloud\""},
		{"docs outside docs/", "docs: [docs/operator-guide/installation.md]", "docs: [spec/README.md]", "not a clean path under docs/"},
		{"docs escaping", "docs: [docs/operator-guide/installation.md]", "docs: [docs/../spec/x.md]", "not a clean path under docs/"},
		{"no docs", "docs: [docs/operator-guide/installation.md]", "docs: []", "docs is empty"},
		{"tool not allowed", "tools: [kubectl]", "tools: [curl]", "tool \"curl\""},
		{"no tools", "tools: [kubectl]", "tools: []", "tools is empty"},
		{"unknown credential", "setup: lenny", "setup: lenny\ncredentials: [aws]", "credential \"aws\""},
		{"gateway credential without gateway", "setup: lenny", "setup: prerequisites\ncredentials: [admin-token]", "needs a running gateway"},
		{"prepare without gateway", "setup: lenny", "setup: prerequisites\nprepare: [\"sessions-allowed:default\"]", "needs setup"},
		{"unknown preparation", "setup: lenny", "setup: lenny\nprepare: [\"wipe:all\"]", "is not sessions-allowed"},
		{"no goal", "goal: Install it.", "goal: \"\"", "goal is empty"},
		{"negative limit", "goal: Install it.", "goal: Install it.\ntimeLimit: -5m", "negative"},
		{"bad limit", "goal: Install it.", "goal: Install it.\ntimeLimit: soon", "invalid duration"},
		{"no postconditions", "postconditions:\n  - name: ready\n    kubectl: [get, pods]\n    expect: {contains: Running}\n", "postconditions: []\n", "no postconditions"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := strings.Replace(minimalScenario, tc.edit, tc.from, 1)
			if src == minimalScenario {
				t.Fatalf("the edit %q did not apply", tc.edit)
			}
			_, err := ParseScenario([]byte(src), "demo.yaml")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want one containing %q", err, tc.want)
			}
		})
	}
}

func TestPostconditionValidationRefusesAmbiguousProbes(t *testing.T) {
	cases := []struct {
		name string
		p    Postcondition
		want string
	}{
		{"no probe", Postcondition{Name: "a", Expect: Expect{Contains: "x"}}, "names no probe"},
		{"two probes", Postcondition{Name: "a", Kubectl: []string{"get"}, API: &APIRequest{Method: "GET", Path: "/x", As: "platform-admin"}, Expect: Expect{Contains: "x"}}, "names two probes"},
		{"kubeconfig flag", Postcondition{Name: "a", Kubectl: []string{"--kubeconfig=/x", "get"}, Expect: Expect{Contains: "x"}}, "selects a cluster"},
		{"context flag", Postcondition{Name: "a", Kubectl: []string{"--context", "kind-lenny-e2e"}, Expect: Expect{Contains: "x"}}, "selects a cluster"},
		{"api without identity", Postcondition{Name: "a", API: &APIRequest{Method: "GET", Path: "/x"}}, "no identity"},
		{"api relative path", Postcondition{Name: "a", API: &APIRequest{Method: "GET", Path: "x", As: "platform-admin"}}, "starting with /"},
		{"checks nothing", Postcondition{Name: "a", Kubectl: []string{"get"}}, "checks nothing"},
		{"status on kubectl", Postcondition{Name: "a", Kubectl: []string{"get"}, Expect: Expect{Status: 200, Contains: "x"}}, "only to an api probe"},
		{"bad regexp", Postcondition{Name: "a", Kubectl: []string{"get"}, Expect: Expect{Matches: "("}}, "expect.matches"},
		{"bad path", Postcondition{Name: "a", Kubectl: []string{"get"}, Expect: Expect{Path: "a..b", Contains: "x"}}, "empty segment"},
		{"forAny without items", Postcondition{Name: "a", API: &APIRequest{Method: "GET", Path: "/x", As: "platform-admin"}, ForAny: &ForAny{Kubectl: []string{"get"}, Expect: Expect{Contains: "x"}}}, "forAny.items is empty"},
		{"forAny without probe", Postcondition{Name: "a", API: &APIRequest{Method: "GET", Path: "/x", As: "platform-admin"}, ForAny: &ForAny{Items: "items", Expect: Expect{Contains: "x"}}}, "forAny: names no probe"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.p.Validate()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want one containing %q", err, tc.want)
			}
		})
	}
	ok := Postcondition{Name: "status-only", API: &APIRequest{Method: "GET", Path: "/healthz", As: "platform-admin"}}
	if err := ok.Validate(); err != nil {
		t.Errorf("an api probe with no value check checks its status, got %v", err)
	}
}

func TestScenarioRefusesDuplicatePostconditionNames(t *testing.T) {
	src := minimalScenario + "  - name: ready\n    kubectl: [get, nodes]\n    expect: {contains: Ready}\n"
	if _, err := ParseScenario([]byte(src), "demo.yaml"); err == nil || !strings.Contains(err.Error(), "appears twice") {
		t.Fatalf("error = %v, want a duplicate-name error", err)
	}
}

func TestLoadDirAndSelect(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("demo.yaml", minimalScenario)
	write("alpha.yaml", strings.Replace(minimalScenario, "name: demo", "name: alpha", 1))
	write("README.md", "not a scenario")
	all, err := LoadDir(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(all) != 2 || all[0].Name != "alpha" || all[1].Name != "demo" {
		t.Fatalf("loaded %d scenarios, want alpha and demo in order", len(all))
	}
	got, err := Select(all, []string{"demo"})
	if err != nil || len(got) != 1 || got[0].Name != "demo" {
		t.Fatalf("select demo = %v, %v", got, err)
	}
	if got, _ := Select(all, nil); len(got) != 2 {
		t.Errorf("an empty selection selects every journey, got %d", len(got))
	}
	if _, err := Select(all, []string{"missing"}); err == nil || !strings.Contains(err.Error(), "alpha, demo") {
		t.Errorf("an unknown name lists the journeys, got %v", err)
	}
	write("broken.yaml", "name: broken\n")
	if _, err := LoadDir(dir); err == nil || !strings.Contains(err.Error(), "broken.yaml") {
		t.Errorf("a broken scenario fails the load naming its file, got %v", err)
	}
}

// TestCommittedJourneysParseAndNameExistingPages parses every scenario
// under tests/journeys/ and checks that the documentation pages each
// names exist, so a scenario that would fail at the start of a paid run
// fails the unit tier instead.
func TestCommittedJourneysParseAndNameExistingPages(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	all, err := LoadDir(filepath.Join(root, "tests", "journeys"))
	if err != nil {
		t.Fatalf("load the committed journeys: %v", err)
	}
	if len(all) < 3 {
		t.Fatalf("found %d journeys, want the three pilot journeys at least", len(all))
	}
	personas := map[string]bool{}
	for _, s := range all {
		personas[s.Persona] = true
		if err := s.CheckDocsExist(root); err != nil {
			t.Error(err)
		}
	}
	for _, p := range []string{"operator", "tenant-admin", "end-user"} {
		if !personas[p] {
			t.Errorf("no committed journey plays the %s persona", p)
		}
	}
}

func TestCheckDocsExistReportsMissingPages(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	s := &Scenario{Path: "x.yaml", Docs: []string{"docs/missing.md"}}
	if err := s.CheckDocsExist(root); err == nil || !strings.Contains(err.Error(), "docs/missing.md") {
		t.Fatalf("error = %v, want one naming the missing page", err)
	}
}
