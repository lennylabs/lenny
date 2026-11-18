// SPDX-License-Identifier: MIT

package gatewaylayout

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
)

// The rules, as the register and the violations name them.
const (
	RuleSeamImports          = "seam-imports"
	RuleCoreImports          = "core-imports"
	RuleAPIFrontImports      = "api-front-imports"
	RuleReplicaImports       = "replica-imports"
	RuleConfinedImports      = "confined-imports"
	RuleSeamInterfaceImports = "seam-interface-imports"
	RuleOutsideImports       = "outside-imports"
	RulePlacement            = "placement"
)

func knownRule(rule string) bool {
	switch rule {
	case RuleSeamImports, RuleCoreImports, RuleAPIFrontImports,
		RuleReplicaImports, RuleSeamInterfaceImports, RuleOutsideImports,
		RuleConfinedImports:
		return true
	}
	return false
}

// Package is one package of the module, with its non-test imports.
type Package struct {
	ImportPath string
	Imports    []string
}

// Violation is one break of a rule. Package is repository-relative; Import
// is the repository-relative imported package for an import rule and empty
// for a placement violation.
type Violation struct {
	Rule    string
	Package string
	Import  string
}

func (v Violation) String() string {
	if v.Rule == RulePlacement {
		return fmt.Sprintf("%s: %s sits outside the directory of its part in the gateway layout", v.Rule, v.Package)
	}
	return fmt.Sprintf("%s: %s imports %s", v.Rule, v.Package, v.Import)
}

// Result is the outcome of one check.
type Result struct {
	// Violations is every break of a rule, registered or not.
	Violations []Violation
	// Unregistered is the violations the register does not carry.
	Unregistered []Violation
	// Stale is the register rows that no longer occur.
	Stale []string
	// Malformed is the register rows that do not validate.
	Malformed []string
}

// Failed reports whether the check refuses the tree.
func (r Result) Failed() bool {
	return len(r.Unregistered) > 0 || len(r.Stale) > 0 || len(r.Malformed) > 0
}

// checker classifies packages against the table and the register.
type checker struct {
	module   string
	table    []Dir
	confined []Confinement
	rows     map[string]PackageRow
}

// Check applies the rules to the packages of a module. module is the
// module path, which every repository-relative path is taken against.
func Check(module string, table []Dir, pkgs []Package, reg Register) Result {
	c := &checker{module: module, table: table, confined: Confined, rows: map[string]PackageRow{}}
	for _, row := range reg.Packages {
		c.rows[row.Package] = row
	}
	res := Result{Malformed: reg.validate(table)}
	present := map[string]bool{}
	for _, p := range pkgs {
		rel, ok := c.rel(p.ImportPath)
		if !ok {
			continue
		}
		present[rel] = true
		res.Violations = append(res.Violations, c.violations(rel, p.Imports)...)
	}
	sortViolations(res.Violations)
	res.Unregistered, res.Stale = reconcile(res.Violations, reg, present)
	return res
}

// rel returns the repository-relative form of an import path in the
// module, or false for a path outside it.
func (c *checker) rel(importPath string) (string, bool) {
	if importPath == c.module {
		return "", true
	}
	rest, ok := strings.CutPrefix(importPath, c.module+"/")
	return rest, ok
}

// classify returns the kind of a repository-relative package path.
func (c *checker) classify(rel string) Kind {
	switch {
	case within(rel, GatewayRoot):
		if row, ok := c.rows[rel]; ok {
			if k, known := partKind(c.table, row.Part); known {
				return k
			}
		}
		if d, ok := layoutDir(c.table, rel); ok {
			return d.Kind
		}
		return KindUnplaced
	case within(rel, CompositionRoot):
		return KindCompositionRoot
	case within(rel, TestsRoot):
		return KindExempt
	default:
		return KindOutside
	}
}

// part returns the part name of a package: the part its register row
// names, or its directory's part.
func (c *checker) part(rel string) string {
	if row, ok := c.rows[rel]; ok {
		return row.Part
	}
	d, _ := layoutDir(c.table, rel)
	return d.Part
}

// misplaced reports whether a gateway package breaks the placement rule:
// it sits in no directory of the table, or its register row gives it a part
// other than its directory's. A register row overrides the directory,
// because a package can sit today in a directory the layout gives to
// another part, as the seam model does under pkg/gateway/core.
func (c *checker) misplaced(rel string) bool {
	d, inTable := layoutDir(c.table, rel)
	if !inTable {
		return true
	}
	row, ok := c.rows[rel]
	return ok && row.Part != d.Part
}

// violations returns the breaks of the rules by one package.
func (c *checker) violations(rel string, imports []string) []Violation {
	var out []Violation
	kind := c.classify(rel)
	if within(rel, GatewayRoot) && c.misplaced(rel) {
		out = append(out, Violation{Rule: RulePlacement, Package: rel})
	}
	part := c.part(rel)
	for _, imp := range imports {
		to, ok := c.rel(imp)
		if !ok {
			continue
		}
		rule := ""
		if within(to, GatewayRoot) {
			rule = c.importRule(rel, kind, to)
		}
		if rule == "" && confinedImport(kind, part, to, c.confined) {
			rule = RuleConfinedImports
		}
		if rule != "" {
			out = append(out, Violation{Rule: rule, Package: rel, Import: to})
		}
	}
	return out
}

// importRule returns the rule an import from a package of the given kind
// to a gateway package breaks, or "" when it breaks none. An import of a
// package in transit or of an unplaced package breaks no import rule: the
// first leaves the gateway or the tree, and the second already fails the
// placement rule.
func (c *checker) importRule(from string, kind Kind, to string) string {
	toKind := c.classify(to)
	switch toKind {
	case KindDomain, KindRemoved, KindUnplaced:
		return ""
	}
	switch kind {
	case KindSeam:
		if toKind == KindSeamInterface || (toKind == KindSeam && c.part(to) == c.part(from)) {
			return ""
		}
		return RuleSeamImports
	case KindCore:
		if toKind == KindSeam || toKind == KindAPIFront {
			return RuleCoreImports
		}
	case KindAPIFront:
		if toKind == KindSeam || toKind == KindCore {
			return RuleAPIFrontImports
		}
	case KindReplica:
		if toKind == KindSeam {
			return RuleReplicaImports
		}
	case KindSeamInterface:
		if toKind != KindSeamInterface {
			return RuleSeamInterfaceImports
		}
	case KindOutside, KindDomain:
		return RuleOutsideImports
	}
	return ""
}

// reconcile splits the violations into those the register does not carry,
// and lists the register rows that match no violation.
func reconcile(vs []Violation, reg Register, present map[string]bool) (unregistered []Violation, stale []string) {
	placed := map[string]bool{}
	edges := map[string]string{}
	for _, v := range vs {
		if v.Rule == RulePlacement {
			placed[v.Package] = true
			continue
		}
		edges[v.Package+" "+v.Import] = v.Rule
	}
	rowPkgs := map[string]bool{}
	for _, row := range reg.Packages {
		rowPkgs[row.Package] = true
		switch {
		case !present[row.Package]:
			stale = append(stale, fmt.Sprintf("package row %s: the package no longer exists", row.Package))
		case !placed[row.Package]:
			stale = append(stale, fmt.Sprintf("package row %s: the package now sits in the directory of its part", row.Package))
		}
	}
	rowEdges := map[string]bool{}
	for _, row := range reg.Imports {
		key := row.From + " " + row.To
		rowEdges[key] = true
		rule, ok := edges[key]
		switch {
		case !ok:
			stale = append(stale, fmt.Sprintf("import row %s -> %s: the import no longer breaks a rule", row.From, row.To))
		case rule != row.Rule:
			stale = append(stale, fmt.Sprintf("import row %s -> %s: registered as %s, now breaks %s", row.From, row.To, row.Rule, rule))
		}
	}
	for _, v := range vs {
		if v.Rule == RulePlacement && !rowPkgs[v.Package] {
			unregistered = append(unregistered, v)
		}
		if v.Rule != RulePlacement && !rowEdges[v.Package+" "+v.Import] {
			unregistered = append(unregistered, v)
		}
	}
	sort.Strings(stale)
	return unregistered, stale
}

func sortViolations(vs []Violation) {
	sort.Slice(vs, func(i, j int) bool {
		if vs[i].Package != vs[j].Package {
			return vs[i].Package < vs[j].Package
		}
		if vs[i].Import != vs[j].Import {
			return vs[i].Import < vs[j].Import
		}
		return vs[i].Rule < vs[j].Rule
	})
}

// ListPackages runs `go list` over every package of the module rooted at
// dir and returns the module path and each package with its non-test
// imports. A package that fails to load still lists the imports go list
// read from it, so one broken package does not hide the rest of the graph.
func ListPackages(dir string) (string, []Package, error) {
	gomod, err := goCommand(dir, "env", "GOMOD")
	if err != nil {
		return "", nil, err
	}
	if gomod = strings.TrimSpace(gomod); gomod == "" || gomod == os.DevNull {
		return "", nil, fmt.Errorf("%s is not inside a module", dir)
	}
	module, err := goCommand(dir, "list", "-m")
	if err != nil {
		return "", nil, err
	}
	out, err := goCommand(dir, "list", "-e", "-json=ImportPath,Imports", "./...")
	if err != nil {
		return "", nil, err
	}
	var pkgs []Package
	dec := json.NewDecoder(bytes.NewReader([]byte(out)))
	for {
		var p Package
		if err := dec.Decode(&p); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return "", nil, fmt.Errorf("decode go list output: %w", err)
		}
		pkgs = append(pkgs, p)
	}
	return strings.TrimSpace(module), pkgs, nil
}

// goCommand runs one go command in dir with CGO disabled and no workspace
// file, and returns its standard output.
func goCommand(dir string, args ...string) (string, error) {
	cmd := exec.Command("go", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOWORK=off", "GOFLAGS=")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("go %s in %s: %w\n%s", strings.Join(args, " "), dir, err, stderr.String())
	}
	return stdout.String(), nil
}
