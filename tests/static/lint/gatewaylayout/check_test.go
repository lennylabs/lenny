// SPDX-License-Identifier: MIT

package gatewaylayout

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const mod = "example.com/fix"

// pkg builds a package of the fixture module from repository-relative
// paths.
func pkg(path string, imports ...string) Package {
	p := Package{ImportPath: mod + "/" + path}
	for _, imp := range imports {
		if strings.Contains(imp, ".") && !strings.HasPrefix(imp, "pkg/") && !strings.HasPrefix(imp, "cmd/") && !strings.HasPrefix(imp, "tests/") {
			p.Imports = append(p.Imports, imp)
			continue
		}
		p.Imports = append(p.Imports, mod+"/"+imp)
	}
	return p
}

// base is a module in which every part sits in its directory and imports
// only what the rules allow.
func base() []Package {
	return []Package{
		pkg("pkg/gateway/seam"),
		pkg("pkg/gateway/seam/subsystem", "pkg/gateway/seam"),
		pkg("pkg/gateway/streamproxy/bus", "pkg/gateway/seam", "pkg/gateway/streamproxy/events"),
		pkg("pkg/gateway/streamproxy/events", "pkg/gateway/seam/subsystem"),
		pkg("pkg/gateway/platformtools/mcp", "pkg/gateway/seam", "pkg/common"),
		pkg("pkg/gateway/core/session", "pkg/gateway/seam", "pkg/gateway/core/podclaim", "pkg/quota"),
		pkg("pkg/gateway/core/podclaim"),
		pkg("pkg/gateway/api/pipeline", "pkg/gateway/seam", "pkg/auth"),
		pkg("pkg/gateway/drain", "pkg/gateway/core/session", "pkg/gateway/seam"),
		pkg("pkg/common"),
		pkg("pkg/quota"),
		pkg("pkg/auth"),
		pkg("cmd/lenny-gateway", "pkg/gateway/core/session", "pkg/gateway/streamproxy/bus", "pkg/gateway/api/pipeline"),
		pkg("tests/integration", "pkg/gateway/core/session"),
		pkg("cmd/other", "pkg/quota", "github.com/x/y"),
	}
}

func check(t *testing.T, pkgs []Package, reg Register) Result {
	t.Helper()
	if reg.Version == 0 {
		reg.Version = 1
	}
	return Check(mod, Layout, pkgs, reg)
}

func assertClean(t *testing.T, res Result) {
	t.Helper()
	if res.Failed() || len(res.Violations) > 0 {
		t.Fatalf("expected a clean check, got violations %v, unregistered %v, stale %v, malformed %v",
			res.Violations, res.Unregistered, res.Stale, res.Malformed)
	}
}

func assertOnly(t *testing.T, res Result, want Violation) {
	t.Helper()
	if len(res.Unregistered) != 1 || res.Unregistered[0] != want {
		t.Fatalf("unregistered = %v, want exactly %v", res.Unregistered, want)
	}
	if !res.Failed() {
		t.Fatal("a check with an unregistered violation did not fail")
	}
}

// TestAModuleThatKeepsEveryPartInItsDirectoryAndBoundaryPasses is the
// passing case of every rule: seams reach the core and each other only
// through the seam interface, the core and the API front import no seam,
// the replica-level parts and the composition root import what they need,
// and tests under tests/ are exempt.
// spec: GW-R1
func TestAModuleThatKeepsEveryPartInItsDirectoryAndBoundaryPasses(t *testing.T) {
	assertClean(t, check(t, base(), Register{}))
}

// TestEachRuleRefusesTheImportThatBreaksIt adds one breaking import or
// package to the passing module per rule and checks that the check names
// exactly that violation.
// spec: GW-R1
func TestEachRuleRefusesTheImportThatBreaksIt(t *testing.T) {
	cases := []struct {
		name string
		add  Package
		want Violation
	}{
		{
			"seam imports the core", pkg("pkg/gateway/uploadhandler/up", "pkg/gateway/core/session"),
			Violation{RuleSeamImports, "pkg/gateway/uploadhandler/up", "pkg/gateway/core/session"},
		},
		{
			"seam imports another seam", pkg("pkg/gateway/uploadhandler/up", "pkg/gateway/streamproxy/bus"),
			Violation{RuleSeamImports, "pkg/gateway/uploadhandler/up", "pkg/gateway/streamproxy/bus"},
		},
		{
			"seam imports the API front", pkg("pkg/gateway/meteringproxy/llm", "pkg/gateway/api/pipeline"),
			Violation{RuleSeamImports, "pkg/gateway/meteringproxy/llm", "pkg/gateway/api/pipeline"},
		},
		{
			"seam imports a replica-level part", pkg("pkg/gateway/meteringproxy/llm", "pkg/gateway/drain"),
			Violation{RuleSeamImports, "pkg/gateway/meteringproxy/llm", "pkg/gateway/drain"},
		},
		{
			"core imports a seam", pkg("pkg/gateway/core/checkpoint", "pkg/gateway/platformtools/mcp"),
			Violation{RuleCoreImports, "pkg/gateway/core/checkpoint", "pkg/gateway/platformtools/mcp"},
		},
		{
			"core imports the API front", pkg("pkg/gateway/core/checkpoint", "pkg/gateway/api/pipeline"),
			Violation{RuleCoreImports, "pkg/gateway/core/checkpoint", "pkg/gateway/api/pipeline"},
		},
		{
			"API front imports a seam", pkg("pkg/gateway/api/admin", "pkg/gateway/streamproxy/bus"),
			Violation{RuleAPIFrontImports, "pkg/gateway/api/admin", "pkg/gateway/streamproxy/bus"},
		},
		{
			"API front imports the core", pkg("pkg/gateway/api/admin", "pkg/gateway/core/podclaim"),
			Violation{RuleAPIFrontImports, "pkg/gateway/api/admin", "pkg/gateway/core/podclaim"},
		},
		{
			"seam interface imports the core", pkg("pkg/gateway/seam/values", "pkg/gateway/core/podclaim"),
			Violation{RuleSeamInterfaceImports, "pkg/gateway/seam/values", "pkg/gateway/core/podclaim"},
		},
		{
			"seam interface imports a replica-level part", pkg("pkg/gateway/seam/values", "pkg/gateway/drain"),
			Violation{RuleSeamInterfaceImports, "pkg/gateway/seam/values", "pkg/gateway/drain"},
		},
		{
			"a replica-level part imports a seam", pkg("pkg/gateway/drain/prestop", "pkg/gateway/streamproxy/bus"),
			Violation{RuleReplicaImports, "pkg/gateway/drain/prestop", "pkg/gateway/streamproxy/bus"},
		},
		{
			"the playground imports a seam", pkg("pkg/gateway/playground", "pkg/gateway/platformtools/mcp"),
			Violation{RuleReplicaImports, "pkg/gateway/playground", "pkg/gateway/platformtools/mcp"},
		},
		{
			"another component imports the core", pkg("pkg/podregistry", "pkg/gateway/core/podclaim"),
			Violation{RuleOutsideImports, "pkg/podregistry", "pkg/gateway/core/podclaim"},
		},
		{
			"another binary imports the seam interface", pkg("cmd/lenny-adapter", "pkg/gateway/seam"),
			Violation{RuleOutsideImports, "cmd/lenny-adapter", "pkg/gateway/seam"},
		},
		{
			"a package outside every directory", pkg("pkg/gateway/session/sessionstore"),
			Violation{Rule: RulePlacement, Package: "pkg/gateway/session/sessionstore"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertOnly(t, check(t, append(base(), tc.add), Register{}), tc.want)
		})
	}
}

// TestRegisteredViolationsPassAndStaleRowsFail checks the register: a
// registered violation passes, and a row whose violation no longer occurs
// fails, so the register only shrinks.
// spec: GW-R1
func TestRegisteredViolationsPassAndStaleRowsFail(t *testing.T) {
	pkgs := append(
		base(),
		pkg("pkg/gateway/sessionserver", "pkg/gateway/platformtools/mcp"),
		pkg("pkg/gateway/mcpfabric/mcptools", "pkg/gateway/sessionserver"),
	)
	reg := Register{Packages: []PackageRow{
		{Package: "pkg/gateway/sessionserver", Part: "core", Step: "0117.S11"},
		{Package: "pkg/gateway/mcpfabric/mcptools", Part: "platform-tools", Step: "0117.S7"},
	}, Imports: []ImportRow{
		{From: "pkg/gateway/sessionserver", To: "pkg/gateway/platformtools/mcp", Rule: RuleCoreImports, Step: "0117.S7"},
		{From: "pkg/gateway/mcpfabric/mcptools", To: "pkg/gateway/sessionserver", Rule: RuleSeamImports, Step: "0117.S7"},
	}}
	res := check(t, pkgs, reg)
	if res.Failed() || len(res.Violations) != 4 {
		t.Fatalf("registered violations: violations %v, unregistered %v, stale %v, malformed %v",
			res.Violations, res.Unregistered, res.Stale, res.Malformed)
	}

	// The import is cut and the package has moved: both rows are stale.
	moved := append(base(), pkg("pkg/gateway/core/sessionserver"))
	reg.Packages = append(reg.Packages, PackageRow{Package: "pkg/gateway/core/sessionserver", Part: "core", Step: "0117.S11"})
	res = check(t, moved, reg)
	for _, want := range []string{
		"package row pkg/gateway/sessionserver: the package no longer exists",
		"package row pkg/gateway/core/sessionserver: the package now sits in the directory of its part",
		"import row pkg/gateway/sessionserver -> pkg/gateway/platformtools/mcp: the import no longer breaks a rule",
	} {
		if !contains(res.Stale, want) {
			t.Errorf("stale rows %v lack %q", res.Stale, want)
		}
	}

	// A row registered under the wrong rule is stale.
	wrong := Register{Packages: reg.Packages[:2], Imports: []ImportRow{
		{From: "pkg/gateway/sessionserver", To: "pkg/gateway/platformtools/mcp", Rule: RuleSeamImports, Step: "0117.S7"},
		reg.Imports[1],
	}}
	res = check(t, pkgs, wrong)
	if !contains(res.Stale, "import row pkg/gateway/sessionserver -> pkg/gateway/platformtools/mcp: registered as seam-imports, now breaks core-imports") {
		t.Errorf("stale rows %v lack the rule mismatch", res.Stale)
	}
}

// TestPackagesInTransitAreImportableAndDomainPackagesStayOutside checks
// the transit parts: any part may import a package that leaves the gateway
// or that a change removes, a leaving package is held to the outside rule,
// and the imports of a removed package are not checked.
// spec: GW-R1
func TestPackagesInTransitAreImportableAndDomainPackagesStayOutside(t *testing.T) {
	pkgs := append(
		base(),
		pkg("pkg/gateway/billing/billingstore", "pkg/gateway/connectors/conn"),
		pkg("pkg/gateway/connectors/conn", "pkg/gateway/core/podclaim", "pkg/gateway/streamproxy/bus"),
		pkg("pkg/gateway/platformtools/tools", "pkg/gateway/billing/billingstore", "pkg/gateway/connectors/conn"),
		pkg("pkg/ops", "pkg/gateway/billing/billingstore", "pkg/gateway/connectors/conn"),
		pkg("pkg/gateway/quota/quotacheckpoint", "pkg/gateway/api/pipeline"),
	)
	reg := Register{Packages: []PackageRow{
		{Package: "pkg/gateway/billing/billingstore", Part: PartDomain, Step: "0117.S9"},
		{Package: "pkg/gateway/quota/quotacheckpoint", Part: PartDomain, Step: "0117.S9"},
		{Package: "pkg/gateway/connectors/conn", Part: PartRemoved, RemovedBy: "0124"},
	}}
	res := check(t, pkgs, reg)
	assertOnly(t, res, Violation{RuleOutsideImports, "pkg/gateway/quota/quotacheckpoint", "pkg/gateway/api/pipeline"})
}

// TestARegisterRowOverridesTheDirectoryOfAMisplacedPackage checks that a
// package sitting today in a directory the layout gives to another part is
// classified by its register row and fails the placement rule until it
// moves.
// spec: GW-R1
func TestARegisterRowOverridesTheDirectoryOfAMisplacedPackage(t *testing.T) {
	pkgs := append(
		base(),
		pkg("pkg/gateway/core/subsystem"),
		pkg("pkg/gateway/meteringproxy/llm", "pkg/gateway/core/subsystem"),
	)
	reg := Register{Packages: []PackageRow{{Package: "pkg/gateway/core/subsystem", Part: "seam-interface", Step: "0117.S4"}}}
	res := check(t, pkgs, reg)
	if res.Failed() {
		t.Fatalf("a registered misplaced package failed: unregistered %v, stale %v, malformed %v", res.Unregistered, res.Stale, res.Malformed)
	}
	if len(res.Violations) != 1 || res.Violations[0].Rule != RulePlacement {
		t.Fatalf("violations = %v, want the one placement violation", res.Violations)
	}
	assertOnly(t, check(t, pkgs, Register{}),
		Violation{RuleSeamImports, "pkg/gateway/meteringproxy/llm", "pkg/gateway/core/subsystem"})
}

// TestMalformedRegisterRowsFail checks the validation of the register.
func TestMalformedRegisterRowsFail(t *testing.T) {
	reg := Register{Version: 2, Packages: []PackageRow{
		{Package: "pkg/gateway/a", Part: "nowhere", Step: "0117.S3"},
		{Package: "pkg/gateway/b", Part: "core"},
		{Package: "pkg/gateway/c", Part: PartRemoved},
		{Package: "pkg/gateway/d", Part: PartRemoved, RemovedBy: "0124", Step: "0117.S3"},
		{Package: "pkg/gateway/e", Part: "core", Step: "S3"},
		{Package: "pkg/gateway/e", Part: "core", Step: "0117.S3", RemovedBy: "0124"},
		{Package: "pkg/gateway/f", Part: PartRemoved, RemovedBy: "124"},
	}, Imports: []ImportRow{
		{From: "x", To: "y", Rule: "made-up", Step: "0117.S7"},
		{From: "x", To: "y", Rule: RuleCoreImports},
		{From: "u", To: "v", Rule: RuleCoreImports, Step: "0117.S7", RemovedBy: "0124"},
	}}
	got := strings.Join(reg.validate(Layout), "\n")
	for _, want := range []string{
		"register version 2, want 1",
		`package row pkg/gateway/a: unknown part "nowhere"`,
		"package row pkg/gateway/b: names no step that moves it",
		"package row pkg/gateway/c: a removed package names the change that removes it",
		"package row pkg/gateway/d: a removed package names no step",
		`package row pkg/gateway/e: step "S3" is not a step identifier`,
		"package row pkg/gateway/e: duplicated",
		"package row pkg/gateway/e: a package that moves names no removing change",
		`package row pkg/gateway/f: removing change "124" is not a change number`,
		`import row x -> y: unknown rule "made-up"`,
		"import row x -> y: duplicated",
		"import row x -> y: names neither a step nor a removing change",
		"import row u -> v: names both a step and a removing change",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("validation lacks %q in:\n%s", want, got)
		}
	}
	if res := check(t, base(), reg); !res.Failed() {
		t.Error("a malformed register did not fail the check")
	}
}

// TestLoadRegisterRefusesUnknownKeys checks that a misspelled key cannot
// drop a row's end condition silently.
func TestLoadRegisterRefusesUnknownKeys(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "good.yaml")
	writeFile(t, good, "version: 1\npackages:\n  - package: pkg/gateway/a\n    part: core\n    step: 0117.S11\nimports: []\n")
	reg, err := LoadRegister(good)
	if err != nil || len(reg.Packages) != 1 || reg.Packages[0].Step != "0117.S11" {
		t.Fatalf("LoadRegister(good) = %+v, %v", reg, err)
	}
	bad := filepath.Join(dir, "bad.yaml")
	writeFile(t, bad, "version: 1\npackages:\n  - package: pkg/gateway/a\n    part: core\n    stpe: 0117.S11\n")
	if _, err := LoadRegister(bad); err == nil {
		t.Error("LoadRegister accepted an unknown key")
	}
	if _, err := LoadRegister(filepath.Join(dir, "absent.yaml")); err == nil {
		t.Error("LoadRegister accepted a missing file")
	}
}

// TestListPackagesReadsAFixtureModuleThatBreaksAndOneThatPasses runs the
// whole gate, go list included, over two fixture modules: one whose seam
// imports the core and one whose seam reaches the core through the seam
// interface.
// spec: GW-R1
func TestListPackagesReadsAFixtureModuleThatBreaksAndOneThatPasses(t *testing.T) {
	files := map[string]string{
		"go.mod":                                "module " + mod + "\n\ngo 1.21\n",
		"pkg/gateway/seam/seam.go":              "package seam\n\ntype Sessions interface{ Lookup(id string) error }\n",
		"pkg/gateway/core/session/s.go":         "package session\n\nimport _ \"" + mod + "/pkg/gateway/seam\"\n",
		"pkg/gateway/streamproxy/bus/b.go":      "package bus\n\nimport _ \"" + mod + "/pkg/gateway/seam\"\n",
		"pkg/gateway/streamproxy/bus/b_test.go": "package bus\n\nimport _ \"" + mod + "/pkg/gateway/core/session\"\n",
	}
	passing := writeModule(t, files)
	module, pkgs, err := ListPackages(passing)
	if err != nil {
		t.Fatal(err)
	}
	if module != mod {
		t.Fatalf("module = %q, want %q", module, mod)
	}
	assertClean(t, Check(module, Layout, pkgs, Register{Version: 1}))

	files["pkg/gateway/streamproxy/bus/b.go"] = "package bus\n\nimport _ \"" + mod + "/pkg/gateway/core/session\"\n"
	breaking := writeModule(t, files)
	module, pkgs, err = ListPackages(breaking)
	if err != nil {
		t.Fatal(err)
	}
	assertOnly(t, Check(module, Layout, pkgs, Register{Version: 1}),
		Violation{RuleSeamImports, "pkg/gateway/streamproxy/bus", "pkg/gateway/core/session"})

	if _, _, err := ListPackages(t.TempDir()); err == nil {
		t.Error("ListPackages succeeded in a directory with no module")
	}
}

// TestViolationStringsNameTheRuleAndThePackages pins the messages the tree
// gate prints.
func TestViolationStringsNameTheRuleAndThePackages(t *testing.T) {
	v := Violation{RuleCoreImports, "pkg/gateway/core/a", "pkg/gateway/streamproxy/b"}
	if got := v.String(); got != "core-imports: pkg/gateway/core/a imports pkg/gateway/streamproxy/b" {
		t.Errorf("String() = %q", got)
	}
	p := Violation{Rule: RulePlacement, Package: "pkg/gateway/x"}
	if got := p.String(); !strings.Contains(got, "pkg/gateway/x sits outside the directory of its part") {
		t.Errorf("String() = %q", got)
	}
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func writeModule(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		writeFile(t, filepath.Join(dir, filepath.FromSlash(name)), content)
	}
	return dir
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestRatchetAcceptsDeletionsAndMovesAndRefusesNewRows checks the
// register's ratchet against its merge-base copy.
// spec: GW-R1
func TestRatchetAcceptsDeletionsAndMovesAndRefusesNewRows(t *testing.T) {
	base := Register{Version: 1, Packages: []PackageRow{
		{Package: "pkg/gateway/llmproxy/llmproxy", Part: "metering-proxy", Step: "0117.S3"},
		{Package: "pkg/gateway/session/sessionevents", Part: "stream-proxy", Step: "0117.S6"},
		{Package: "pkg/gateway/core/semver", Part: PartDomain, Step: "0117.S4"},
	}, Imports: []ImportRow{
		{From: "pkg/gateway/mcpfabric/mcp", To: "pkg/gateway/session/sessionevents", Rule: RuleSeamImports, Step: "0117.S6"},
		{From: "pkg/gateway/sessionserver", To: "pkg/gateway/mcpfabric/mcp", Rule: RuleCoreImports, Step: "0117.S7"},
	}}
	moves := []Move{
		{Old: "pkg/gateway/session/sessionevents", New: "pkg/gateway/streamproxy/sessionevents", Step: "0117.S6"},
		{Old: "pkg/gateway/mcpfabric/mcptools", New: "pkg/gateway/platformtools/mcptools", Step: "0117.S7"},
	}

	// Unchanged, deleted, and move-rewritten rows pass, and so does a row
	// a pre-existing transit package keeps inside a table directory.
	current := Register{Version: 1, Packages: base.Packages[2:], Imports: []ImportRow{
		{From: "pkg/gateway/sessionserver", To: "pkg/gateway/mcpfabric/mcp", Rule: RuleCoreImports, Step: "0117.S7"},
	}}
	if got := Ratchet(base, current, moves, Layout); len(got) != 0 {
		t.Fatalf("deletions were refused: %v", got)
	}
	carried := current
	carried.Imports = append([]ImportRow{}, base.Imports...)
	carried.Imports[0].To = "pkg/gateway/streamproxy/sessionevents"
	if got := Ratchet(base, carried, moves, Layout); len(got) != 0 {
		t.Fatalf("a row whose path a move rewrote was refused: %v", got)
	}

	// A row the step that moves its package adds is refused: a move step
	// adds no row.
	atStep := current
	atStep.Imports = append(atStep.Imports, ImportRow{From: "pkg/gateway/platformtools/mcptools", To: "pkg/gateway/core/session", Rule: RuleSeamImports, Step: "0117.S7"})
	if got := Ratchet(base, atStep, moves, Layout); len(got) != 1 {
		t.Fatalf("a row added by the step that moves its package passed: %v", got)
	}

	// New rows outside a move are refused, and so is a new transit row
	// inside a table directory.
	added := current
	added.Packages = append(
		append([]PackageRow{}, current.Packages...),
		PackageRow{Package: "pkg/gateway/quota/newstore", Part: PartDomain, Step: "0117.S9"},
		PackageRow{Package: "pkg/gateway/platformtools/mcptools/internal/gen", Part: PartRemoved, RemovedBy: "0138"},
	)
	added.Imports = append(added.Imports, ImportRow{From: "pkg/gateway/x", To: "pkg/gateway/y", Rule: RuleCoreImports, Step: "0117.S11"})
	got := strings.Join(Ratchet(base, added, moves, Layout), "\n")
	for _, want := range []string{
		"package row pkg/gateway/quota/newstore: the row is new since the merge base",
		"package row pkg/gateway/platformtools/mcptools/internal/gen: a package of part removed sits in pkg/gateway/platformtools",
		"import row pkg/gateway/x -> pkg/gateway/y: the row is new since the merge base",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("ratchet lacks %q in:\n%s", want, got)
		}
	}

	// A row whose step changed is new.
	restepped := Register{Version: 1, Packages: []PackageRow{{Package: "pkg/gateway/llmproxy/llmproxy", Part: "metering-proxy", Step: "0117.S4"}}}
	if got := Ratchet(base, restepped, moves, Layout); len(got) != 1 {
		t.Errorf("a re-stepped row was not refused: %v", got)
	}
}

// TestParseRegisterRefusesMalformedYAML checks the decoder on bytes.
func TestParseRegisterRefusesMalformedYAML(t *testing.T) {
	if _, err := ParseRegister([]byte("version: [")); err == nil {
		t.Error("malformed YAML parsed")
	}
	r, err := ParseRegister([]byte("version: 1\n"))
	if err != nil || r.Version != 1 {
		t.Errorf("ParseRegister = %+v, %v", r, err)
	}
}

// TestConfinedPackagesStayWithTheCoreAndTheMeteringProxy checks GW-R2 over
// fixed modules: with no confined import the check is clean; the session
// core, the metering proxy, the composition root, packages outside the
// gateway, and packages in transit import every confined package; the API
// front imports the authentication middleware and the stores and packages
// its admin handlers read, and nothing else confined; and every other part
// imports none, before and after the confined package moves to its concern
// directory.
// spec: GW-R2
func TestConfinedPackagesStayWithTheCoreAndTheMeteringProxy(t *testing.T) {
	assertClean(t, check(t, base(), Register{}))

	allowed := append(
		base(),
		pkg("pkg/gateway/core/admission", "pkg/gateway/quota/quotastore", "pkg/policy/policy", "pkg/gateway/credentials/credentialstore"),
		pkg("pkg/gateway/meteringproxy/llm", "pkg/gateway/credentials/credleasestore", "pkg/quota", "pkg/tenancy/tenantstore"),
		pkg("pkg/gateway/api/pipeline2", "pkg/gateway/middleware/auth", "pkg/auth/auth"),
		pkg("pkg/gateway/api/admin2", "pkg/gateway/environment/tenantstore", "pkg/gateway/quota/quotastore", "pkg/gateway/policy/ratelimit", "pkg/gateway/policy/interceptor/interceptorstore", "pkg/kms/rekey", "pkg/auth/jwt", "pkg/credential/credentialpoolstore", "pkg/credential", "pkg/gateway/externalapi/admintoken"),
		pkg("cmd/lenny-gateway/wire", "pkg/gateway/policy/interceptor"),
		pkg("pkg/ops", "pkg/credential/credentialstore"),
		pkg("pkg/gateway/billing/usage", "pkg/gateway/quota/quotastore"),
	)
	transit := Register{Packages: []PackageRow{{Package: "pkg/gateway/billing/usage", Part: PartDomain, Step: "0117.S9"}}}
	res := check(t, allowed, transit)
	if res.Failed() {
		t.Fatalf("allowed importers were refused: unregistered %v, stale %v, malformed %v", res.Unregistered, res.Stale, res.Malformed)
	}

	cases := []struct {
		name string
		add  Package
		want Violation
	}{
		{
			"a seam imports quota", pkg("pkg/gateway/platformtools/tools", "pkg/gateway/quota/quotastore"),
			Violation{RuleConfinedImports, "pkg/gateway/platformtools/tools", "pkg/gateway/quota/quotastore"},
		},
		{
			"a seam imports the moved credential store", pkg("pkg/gateway/streamproxy/x", "pkg/credential/credentialstore/pgstore"),
			Violation{RuleConfinedImports, "pkg/gateway/streamproxy/x", "pkg/credential/credentialstore/pgstore"},
		},
		{
			"a seam imports the authentication middleware", pkg("pkg/gateway/uploadhandler/up", "pkg/gateway/middleware/auth"),
			Violation{RuleConfinedImports, "pkg/gateway/uploadhandler/up", "pkg/gateway/middleware/auth"},
		},
		{
			"the API front imports policy", pkg("pkg/gateway/api/admin", "pkg/gateway/policy/policy"),
			Violation{RuleConfinedImports, "pkg/gateway/api/admin", "pkg/gateway/policy/policy"},
		},
		{
			"a replica-level part imports the tenant store", pkg("pkg/gateway/replica/x", "pkg/gateway/environment/tenantstore"),
			Violation{RuleConfinedImports, "pkg/gateway/replica/x", "pkg/gateway/environment/tenantstore"},
		},
		{
			"the seam interface imports quota", pkg("pkg/gateway/seam/q", "pkg/quota"),
			Violation{RuleConfinedImports, "pkg/gateway/seam/q", "pkg/quota"},
		},
		{
			"a seam imports the JWT package", pkg("pkg/gateway/platformtools/tools", "pkg/auth/jwt"),
			Violation{RuleConfinedImports, "pkg/gateway/platformtools/tools", "pkg/auth/jwt"},
		},
		{
			"a replica-level part imports KMS", pkg("pkg/gateway/replica/k", "pkg/kms"),
			Violation{RuleConfinedImports, "pkg/gateway/replica/k", "pkg/kms"},
		},
		{
			"the API front imports KMS outside rekey", pkg("pkg/gateway/api/k", "pkg/kms/aws"),
			Violation{RuleConfinedImports, "pkg/gateway/api/k", "pkg/kms/aws"},
		},
		{
			"the API front imports the credential store", pkg("pkg/gateway/api/c", "pkg/credential/credentialstore"),
			Violation{RuleConfinedImports, "pkg/gateway/api/c", "pkg/credential/credentialstore"},
		},
		{
			"a seam imports a package of the credentials subtree", pkg("pkg/gateway/platformtools/c", "pkg/gateway/credentials/credrenewal"),
			Violation{RuleConfinedImports, "pkg/gateway/platformtools/c", "pkg/gateway/credentials/credrenewal"},
		},
		{
			"a seam imports the Token Service proxy", pkg("pkg/gateway/streamproxy/t", "pkg/tokensvcproxy"),
			Violation{RuleConfinedImports, "pkg/gateway/streamproxy/t", "pkg/tokensvcproxy"},
		},
		{
			"the API front imports a package under the shared credential package", pkg("pkg/gateway/api/c2", "pkg/credential/credfallback"),
			Violation{RuleConfinedImports, "pkg/gateway/api/c2", "pkg/credential/credfallback"},
		},
		{
			"the API front imports token introspection", pkg("pkg/gateway/api/i", "pkg/auth/introspection"),
			Violation{RuleConfinedImports, "pkg/gateway/api/i", "pkg/auth/introspection"},
		},
		{
			"a seam imports an authorization predicate", pkg("pkg/gateway/streamproxy/a", "pkg/gateway/environment/envaccess"),
			Violation{RuleConfinedImports, "pkg/gateway/streamproxy/a", "pkg/gateway/environment/envaccess"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertOnly(t, check(t, append(append([]Package{}, allowed...), tc.add), transit), tc.want)
		})
	}

	// A registered importer passes, and its row goes stale when the import
	// goes.
	withRow := transit
	withRow.Imports = []ImportRow{{From: "pkg/gateway/platformtools/tools", To: "pkg/gateway/quota/quotastore", Rule: RuleConfinedImports, Step: "0117.S7"}}
	res = check(t, append(append([]Package{}, allowed...), pkg("pkg/gateway/platformtools/tools", "pkg/gateway/quota/quotastore")), withRow)
	if res.Failed() {
		t.Fatalf("a registered confined import failed: unregistered %v, stale %v", res.Unregistered, res.Stale)
	}
	res = check(t, append(append([]Package{}, allowed...), pkg("pkg/gateway/platformtools/tools")), withRow)
	if len(res.Stale) != 1 || !strings.Contains(res.Stale[0], "no longer breaks a rule") {
		t.Fatalf("a registered confined import that went is not stale: %v", res.Stale)
	}
}

// TestRatchetRefusesANewRowPlacingAPackageInAnotherPartsDirectory checks
// the placement refusal of the ratchet.
// spec: GW-R1
func TestRatchetRefusesANewRowPlacingAPackageInAnotherPartsDirectory(t *testing.T) {
	misplaced := Register{Version: 1, Packages: []PackageRow{{Package: "pkg/gateway/core/x", Part: "platform-tools", Step: "0117.S7"}}}
	got := Ratchet(Register{Version: 1}, misplaced, nil, Layout)
	if len(got) != 1 || !strings.Contains(got[0], "a package of part platform-tools sits in pkg/gateway/core") {
		t.Errorf("a new row placing a package in another part's directory: %v", got)
	}
	if got := Ratchet(misplaced, misplaced, nil, Layout); len(got) != 0 {
		t.Errorf("a row present at the merge base was refused: %v", got)
	}
}
