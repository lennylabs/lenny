// SPDX-License-Identifier: MIT

//go:build ignore

// goident renames the Go identifiers derived from the retired pod
// lifecycle kind names (0137.S5, "PLC-2" Identifiers that change.). It
// parses every Go file with go/parser and rewrites ast.Ident nodes only, so
// comments, string literals, and struct tags are left to strings.py. It
// also moves the import path and the package qualifier of
// pkg/controller/sandbox to pkg/controller/agentpod.
//
// Usage, from the repository root:
//
//	go run changes/0137-pod-lifecycle-controller/evidence/s5-rename/goident.go survey
//	go run changes/0137-pod-lifecycle-controller/evidence/s5-rename/goident.go apply
//
// survey prints every distinct identifier holding "sandbox" in any case,
// with its count and its new name (or "keep"), so the keep list can be
// reviewed before apply.
package main

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const (
	oldPkg = "github.com/lennylabs/lenny/pkg/controller/sandbox"
	newPkg = "github.com/lennylabs/lenny/pkg/controller/agentpod"
)

// skipDirs are never rewritten: the change records, the staged proposals,
// and fixtures that present text as it was written.
var skipDirs = map[string]bool{
	".git": true, "changes": true, "proposals": true, "testdata": true,
	"node_modules": true, "vendor": true, ".claude": true,
}

// keep lists identifiers that use "sandbox" in the isolation sense of the
// specification's sandbox isolation profile, name the pkg/sandbox or
// pkg/sandboxclaim packages, or name something other than a retired kind.
// Each was read in the survey.
var keep = map[string]bool{
	"assertScheduledOnSandboxGvisorNode": true, // a gVisor sandbox node
	"TestT4SandboxKMSFailClosedTestbed":  true, // the sandboxed isolation profile
}

// overrides name identifiers whose mechanical rename reads badly.
var overrides = map[string]string{
	"lookupOutageAgentSandbox": "lookupOutageAgentPod",
	"lookupOutageToolSandbox":  "lookupOutageToolAgentPod",
	"sandboxclaim_guard":       "podclaim_guard",
	"sandboxclaimstate":        "podclaimstate", // D14
	"sandboxclaim":             "podclaim",      // D14
	"TestAgentSandboxIsTheDefaultPoolManager_spec_17_1": "TestCRDPoolManagerIsTheDefault",
}

// pathMoves are the package directories that move, with their new package
// names.
var pathMoves = map[string]string{
	"github.com/lennylabs/lenny/pkg/admission/sandboxclaim_guard": "github.com/lennylabs/lenny/pkg/admission/podclaim_guard",
	"github.com/lennylabs/lenny/pkg/sandboxclaim/state":           "github.com/lennylabs/lenny/pkg/podclaim/state", // D14
}

// keepRe matches identifiers in the isolation sense: the sandboxed
// profile, GKE Sandbox, gVisor's runsc sandbox, and the pkg/sandbox
// package qualifier.
var keepRe = regexp.MustCompile(`(?i:sandboxed|gkesandbox|gvisorsandbox|sandboxisolation|sandboxprofile|sandboxruntimeclass)|^sandboxcond$|^sandboxstate$`)

// rules are applied in order to an identifier's name. Each rule matches a
// retired kind name at a camel-case word boundary.
var rules = []struct {
	re   *regexp.Regexp
	repl string
}{
	{regexp.MustCompile(`AgentSandbox`), "CRD"},
	{regexp.MustCompile(`SandboxWarmPools`), "PodSets"},
	{regexp.MustCompile(`SandboxWarmPool`), "PodSet"},
	{regexp.MustCompile(`SandboxTemplates`), "PodSets"},
	{regexp.MustCompile(`SandboxTemplate`), "PodSet"},
	{regexp.MustCompile(`SandboxClaims`), "PodClaims"},
	{regexp.MustCompile(`SandboxClaim`), "PodClaim"},
	{regexp.MustCompile(`Sandboxes`), "AgentPods"},
	{regexp.MustCompile(`SANDBOX_WARM_POOL`), "POD_SET"},
	{regexp.MustCompile(`SANDBOX_TEMPLATE`), "POD_SET"},
	{regexp.MustCompile(`SANDBOX_CLAIM`), "POD_CLAIM"},
	{regexp.MustCompile(`SANDBOXES`), "AGENT_PODS"},
	{regexp.MustCompile(`SANDBOX`), "AGENT_POD"},
	{regexp.MustCompile(`Sandbox`), "AgentPod"},
	{regexp.MustCompile(`^sandboxWarmPools`), "podSets"},
	{regexp.MustCompile(`^sandboxWarmPool`), "podSet"},
	{regexp.MustCompile(`^sandboxTemplates`), "podSets"},
	{regexp.MustCompile(`^sandboxTemplate`), "podSet"},
	{regexp.MustCompile(`^sandboxClaims`), "podClaims"},
	{regexp.MustCompile(`^sandboxClaim`), "podClaim"},
	{regexp.MustCompile(`^sandboxes`), "agentPods"},
	{regexp.MustCompile(`^sandbox([A-Z0-9_]|$)`), "agentPod$1"},
	{regexp.MustCompile(`^sbx([A-Z]|$)`), "apod$1"},
}

// rename returns the new name of an identifier, or "" to keep it.
func rename(name string) string {
	if to, ok := overrides[name]; ok {
		return to
	}
	if keep[name] || keepRe.MatchString(name) {
		return ""
	}
	out := name
	for _, r := range rules {
		out = r.re.ReplaceAllString(out, r.repl)
	}
	if out == name {
		return ""
	}
	// A renamed test function drops a section number from its name, which
	// the annotation lint refuses on a changed line; the section moves to
	// the doc comment (see stripped in processFile).
	if strings.HasPrefix(out, "Test") {
		out = sectionSuffix.ReplaceAllString(out, "")
	}
	return out
}

// sectionSuffix matches a section number at the end of a test name, as
// in _spec_4_6_3 or _Spec4_6_1.
var sectionSuffix = regexp.MustCompile(`_?[Ss]pec_?\d+(_\d+)*$`)

// sectionOf returns the section number a test name carries, such as
// "4.6.3" for _spec_4_6_3.
func sectionOf(name string) string {
	m := regexp.MustCompile(`[Ss]pec_?(\d+(?:_\d+)*)$`).FindStringSubmatch(name)
	if m == nil {
		return ""
	}
	return strings.ReplaceAll(m[1], "_", ".")
}

// headings maps a numbered specification section to its heading, read
// from the headings of spec/NN_*.md.
var headings = map[string]string{}

func loadHeadings() error {
	files, err := filepath.Glob("spec/[0-9][0-9]_*.md")
	if err != nil {
		return err
	}
	re := regexp.MustCompile(`(?m)^#{2,6} (\d+(?:\.\d+)*) (.+)$`)
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			return err
		}
		for _, m := range re.FindAllStringSubmatch(string(b), -1) {
			headings[m[1]] = strings.TrimSpace(m[2])
		}
	}
	return nil
}

// specLine returns the doc-comment annotation for a section, walking up
// to the nearest numbered parent that has a heading.
func specLine(sec string) string {
	for s := sec; s != ""; {
		if h, ok := headings[s]; ok {
			return fmt.Sprintf("// spec: §%s (%s)\n", s, h)
		}
		i := strings.LastIndex(s, ".")
		if i < 0 {
			break
		}
		s = s[:i]
	}
	return fmt.Sprintf("// spec: §%s\n", sec)
}

// applied records every identifier rename for strings.py, which applies
// the same names to comments, strings, and prose.
var applied = map[string]string{}

type edit struct {
	off, end int
	text     string
}

func main() {
	if len(os.Args) != 2 || (os.Args[1] != "survey" && os.Args[1] != "apply") {
		fmt.Fprintln(os.Stderr, "usage: goident survey|apply")
		os.Exit(2)
	}
	apply := os.Args[1] == "apply"
	if err := loadHeadings(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	counts := map[string]int{}
	files := 0
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skipDirs[d.Name()] && path != "." {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		changed, err := processFile(path, counts, apply)
		if err != nil {
			return err
		}
		if changed {
			files++
		}
		return nil
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if !apply {
		names := make([]string, 0, len(counts))
		for n := range counts {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			to := rename(n)
			if to == "" {
				to = "keep"
			}
			fmt.Printf("%6d  %s -> %s\n", counts[n], n, to)
		}
		return
	}
	names := make([]string, 0, len(applied))
	for n := range applied {
		names = append(names, n)
	}
	sort.Strings(names)
	var b strings.Builder
	for _, n := range names {
		fmt.Fprintf(&b, "%s\t%s\n", n, applied[n])
	}
	out := "changes/0137-pod-lifecycle-controller/evidence/s5-rename/renames.tsv"
	if err := os.WriteFile(out, []byte(b.String()), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("rewrote %d files; %d identifier renames in %s\n", files, len(names), out)
}

var sandboxAny = regexp.MustCompile(`(?i)sandbox|^sbx`)

func processFile(path string, counts map[string]int, apply bool) (bool, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, src, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		return false, fmt.Errorf("parse %s: %w", path, err)
	}
	inMovedPkg := strings.HasPrefix(filepath.ToSlash(path), "pkg/controller/sandbox/") &&
		!strings.Contains(strings.TrimPrefix(filepath.ToSlash(path), "pkg/controller/sandbox/"), "/")
	// qualifier is the local name of the moved package in this file, when
	// the file imports it without an alias.
	qualifier := ""
	var edits []edit
	for _, imp := range file.Imports {
		p, _ := strconv.Unquote(imp.Path.Value)
		if np, ok := pathMoves[p]; ok {
			edits = append(edits, edit{fset.Position(imp.Path.Pos()).Offset, fset.Position(imp.Path.End()).Offset, strconv.Quote(np)})
		}
		if p == oldPkg || strings.HasPrefix(p, oldPkg+"/") {
			np := newPkg + strings.TrimPrefix(p, oldPkg)
			edits = append(edits, edit{fset.Position(imp.Path.Pos()).Offset, fset.Position(imp.Path.End()).Offset, strconv.Quote(np)})
			if p == oldPkg && imp.Name == nil {
				qualifier = "sandbox"
			}
		}
	}
	if n := file.Name; strings.HasPrefix(n.Name, "sandboxclaim_guard") {
		edits = append(edits, edit{fset.Position(n.Pos()).Offset, fset.Position(n.End()).Offset, strings.Replace(n.Name, "sandboxclaim_guard", "podclaim_guard", 1)})
	}
	if inMovedPkg {
		n := file.Name
		newName := strings.Replace(n.Name, "sandbox", "agentpod", 1)
		if newName != n.Name {
			edits = append(edits, edit{fset.Position(n.Pos()).Offset, fset.Position(n.End()).Offset, newName})
		}
	}
	for _, d := range file.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok || fd.Recv != nil || !strings.HasPrefix(fd.Name.Name, "Test") {
			continue
		}
		sec := sectionOf(fd.Name.Name)
		if sec == "" || rename(fd.Name.Name) == "" {
			continue
		}
		if fd.Doc != nil && strings.Contains(fd.Doc.Text(), "spec:") {
			continue
		}
		off := fset.Position(fd.Pos()).Offset
		edits = append(edits, edit{off, off, specLine(sec)})
	}
	ast.Inspect(file, func(node ast.Node) bool {
		switch t := node.(type) {
		case *ast.File:
			// The package clause is handled above.
			for _, d := range t.Decls {
				ast.Inspect(d, func(n ast.Node) bool { return visit(n, fset, qualifier, counts, &edits) })
			}
			return false
		}
		return true
	})
	if !apply || len(edits) == 0 {
		return false, nil
	}
	sort.Slice(edits, func(i, j int) bool { return edits[i].off > edits[j].off })
	out := append([]byte(nil), src...)
	last := len(out) + 1
	for _, e := range edits {
		if e.end > last {
			continue // overlapping edit from the same node; the first wins
		}
		out = append(out[:e.off], append([]byte(e.text), out[e.end:]...)...)
		last = e.off
	}
	formatted, err := format.Source(out)
	if err != nil {
		return false, fmt.Errorf("format %s: %w", path, err)
	}
	if bytes.Equal(formatted, src) {
		return false, nil
	}
	return true, os.WriteFile(path, formatted, 0o644)
}

func visit(n ast.Node, fset *token.FileSet, qualifier string, counts map[string]int, edits *[]edit) bool {
	switch t := n.(type) {
	case *ast.SelectorExpr:
		if x, ok := t.X.(*ast.Ident); ok && qualifier != "" && x.Name == qualifier {
			*edits = append(*edits, edit{fset.Position(x.Pos()).Offset, fset.Position(x.End()).Offset, "agentpod"})
			// Do not visit x again as a plain identifier.
			ast.Inspect(t.Sel, func(m ast.Node) bool { return visit(m, fset, qualifier, counts, edits) })
			return false
		}
	case *ast.Ident:
		if !sandboxAny.MatchString(t.Name) {
			return true
		}
		counts[t.Name]++
		if to := rename(t.Name); to != "" {
			applied[t.Name] = to
			*edits = append(*edits, edit{fset.Position(t.Pos()).Offset, fset.Position(t.End()).Offset, to})
		}
	}
	return true
}
