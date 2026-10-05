// SPDX-License-Identifier: MIT

// Tier-11 consistency checks between BUILD-GAPS.md and the runtime SDK and
// adapter code its runtime-lifetime findings describe.
//
// The build loops read an open finding's evidence as a statement about the
// current tree. These checks hold the records to the tree in both
// directions:
//
//   - A finding filed for a gap the tree still has stays open, and a finding
//     whose gap the tree has closed does not stay open. The probes read the
//     tree's declarations (struct fields, JSON tags, proto fields, and
//     assignments), so the check flips when a carrier lands.
//   - An open finding whose half has landed records that progress, so a loop
//     working it does not re-implement the landed half. The runtime
//     connection handshake closes the nonce half of the CH-MSGSOCK
//     authentication finding, and the session_start and session_end frames
//     close the per-session context half of the first-session manifest
//     ordering finding, whose ordering half stays with the supervisor.
//
// This test reads the repository state directly (no build tag, no
// infrastructure), the same posture as the other tier-11 doc checks.
//
// spec: 15.7 (Runtime Author SDKs), 4.7.6 (Adapter Manifest Field Reference),
// 4.9 (Credential Leasing Service), 4.7.11 (Runtime connection handshake)

package tier11_docs_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// gapFinding is one BUILD-GAPS finding, open or closed: its heading, the
// text up to the next heading, and whether its checkbox is unticked and its
// marker reads OPEN.
type gapFinding struct {
	line    int
	heading string
	body    string
	open    bool
}

// findingHeading matches a checklist finding heading and captures its
// checkbox mark and its identifier.
var findingHeading = regexp.MustCompile(`^### - \[( |x)\] (F-[0-9.]+) `)

// buildGapsFindingsByID returns every checklist finding in BUILD-GAPS.md,
// keyed by its identifier.
func buildGapsFindingsByID(t testing.TB, root string) map[string]gapFinding {
	t.Helper()
	body := readDocPage(t, filepath.Join(root, "BUILD-GAPS.md"))
	out := map[string]gapFinding{}
	var id string
	for i, line := range strings.Split(body, "\n") {
		if m := findingHeading.FindStringSubmatch(line); m != nil {
			id = m[2]
			out[id] = gapFinding{
				line:    i + 1,
				heading: line,
				open:    m[1] == " " && strings.HasSuffix(strings.TrimSpace(line), "— OPEN"),
			}
			continue
		}
		if strings.HasPrefix(line, "#") {
			id = ""
			continue
		}
		if id != "" {
			f := out[id]
			f.body += line + "\n"
			out[id] = f
		}
	}
	return out
}

// requireFinding returns the finding with the given identifier and fails the
// test when BUILD-GAPS.md has none.
func requireFinding(t testing.TB, findings map[string]gapFinding, id string) gapFinding {
	t.Helper()
	f, ok := findings[id]
	if !ok {
		t.Fatalf("BUILD-GAPS.md has no finding %s (renumbered or removed?)", id)
	}
	return f
}

// parseGoFile parses the Go source file at path.
func parseGoFile(t testing.TB, path string) *ast.File {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return file
}

// structFields returns the field names and JSON tag names of the struct type
// typeName declared in the file at path. It fails the test when the file
// declares no such struct, so a rename does not turn a probe vacuous.
func structFields(t testing.TB, path, typeName string) (names, jsonTags map[string]bool) {
	t.Helper()
	names, jsonTags = map[string]bool{}, map[string]bool{}
	found := false
	ast.Inspect(parseGoFile(t, path), func(n ast.Node) bool {
		spec, ok := n.(*ast.TypeSpec)
		if !ok || spec.Name.Name != typeName {
			return true
		}
		st, ok := spec.Type.(*ast.StructType)
		if !ok {
			return true
		}
		found = true
		for _, field := range st.Fields.List {
			for _, name := range field.Names {
				names[name.Name] = true
			}
			if tag := jsonTagName(field.Tag); tag != "" {
				jsonTags[tag] = true
			}
		}
		return false
	})
	if !found {
		t.Fatalf("%s: no struct type %s (renamed or moved?)", path, typeName)
	}
	return names, jsonTags
}

// jsonTagName returns the name part of a struct field's json tag, or "".
func jsonTagName(lit *ast.BasicLit) string {
	if lit == nil {
		return ""
	}
	raw, err := strconv.Unquote(lit.Value)
	if err != nil {
		return ""
	}
	name, _, _ := strings.Cut(reflect.StructTag(raw).Get("json"), ",")
	return name
}

// packageSetsField reports whether any non-test Go file in dir writes the
// named field, either as a keyed composite-literal element or as the target
// of an assignment through a selector.
func packageSetsField(t testing.TB, dir, field string) bool {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		t.Fatalf("glob %s: %v", dir, err)
	}
	if len(paths) == 0 {
		t.Fatalf("%s: no Go files (moved?)", dir)
	}
	for _, path := range paths {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		if fileSetsField(parseGoFile(t, path), field) {
			return true
		}
	}
	return false
}

// fileSetsField reports whether file writes the named field.
func fileSetsField(file *ast.File, field string) bool {
	found := false
	ast.Inspect(file, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.KeyValueExpr:
			if ident, ok := node.Key.(*ast.Ident); ok && ident.Name == field {
				found = true
			}
		case *ast.AssignStmt:
			for _, lhs := range node.Lhs {
				if sel, ok := lhs.(*ast.SelectorExpr); ok && sel.Sel.Name == field {
					found = true
				}
			}
		}
		return !found
	})
	return found
}

// sourceMatches reports whether the file at path matches re.
func sourceMatches(t testing.TB, path string, re *regexp.Regexp) bool {
	t.Helper()
	return re.MatchString(readDocPage(t, path))
}

// protoRuntimeOptionsField matches a proto field that carries runtime
// options, in the snake-case spelling the proto style fixes.
var protoRuntimeOptionsField = regexp.MustCompile(`(?m)^\s*[\w.<>, ]+\s+runtime_options\s*=\s*\d+`)

// runtimeSDKGap is one BUILD-GAPS finding filed for a gap the tree still
// has, and the probe that reports whether the gap is still present.
type runtimeSDKGap struct {
	id      string
	present func(t testing.TB, root string) bool
	what    string
}

// runtimeSDKGaps lists the findings the runtime-lifetime work filed for gaps
// it leaves, each with a probe of the declarations its evidence names.
var runtimeSDKGaps = []runtimeSDKGap{
	{
		id:   "F-15.7.13",
		what: "the adapter manifest, the adapter proto, and the session_start frame carry no runtime options, and no Go SDK file sets CreateRequest.WorkspacePlan",
		present: func(t testing.TB, root string) bool {
			_, manifestTags := structFields(t, filepath.Join(root, "pkg", "adapter", "manifest.go"), "Manifest")
			proto := sourceMatches(t, filepath.Join(root, "schemas", "lenny-adapter.proto"), protoRuntimeOptionsField)
			sdkSetsPlan := packageSetsField(t, filepath.Join(root, "sdks", "runtime", "go", "runtime"), "WorkspacePlan")
			return !manifestTags["runtimeOptions"] && !proto && !sdkSetsPlan
		},
	},
	{
		id:   "F-15.7.14",
		what: "the Go SDK's TerminationReason has no Code or Detail field",
		present: func(t testing.TB, root string) bool {
			names, _ := structFields(t, filepath.Join(root, "sdks", "runtime", "go", "runtime", "types.go"), "TerminationReason")
			return !names["Code"] && !names["Detail"]
		},
	},
	{
		id:   "F-4.9.29",
		what: "the adapter's session_start llm object (ManifestLLM) has no headers member",
		present: func(t testing.TB, root string) bool {
			_, tags := structFields(t, filepath.Join(root, "pkg", "adapter", "manifest.go"), "ManifestLLM")
			return !tags["headers"]
		},
	},
}

// diagnosis: a runtime SDK or adapter finding in BUILD-GAPS.md disagrees with
// the tree. Either the finding was closed while the gap its evidence names is
// still in the code, or a carrier landed and the finding is still open. Close
// the finding with a resolution note when the gap is fixed, and reopen it when
// it was closed without a fix; do not change the code to match the record.
func TestBuildGapsRuntimeSDKFindingsMatchTheTree_spec_15_7(t *testing.T) {
	root := repoRoot(t)
	findings := buildGapsFindingsByID(t, root)
	for _, gap := range runtimeSDKGaps {
		t.Run(gap.id, func(t *testing.T) {
			f := requireFinding(t, findings, gap.id)
			present := gap.present(t, root)
			switch {
			case present && !f.open:
				t.Errorf("BUILD-GAPS.md:%d %s\n  is closed, but the tree still has the gap: %s", f.line, f.heading, gap.what)
			case !present && f.open:
				t.Errorf("BUILD-GAPS.md:%d %s\n  is open, but the tree no longer has the gap it names (%s no longer holds); close it with a resolution note", f.line, f.heading, gap.what)
			}
		})
	}
}

// landedHalf is an open finding with a half that has landed, the declaration
// that shows it landed, and the sentence the finding must carry to record it.
type landedHalf struct {
	id       string
	file     []string
	marker   *regexp.Regexp
	recorded string
}

// landedHalves lists the findings whose half the runtime-lifetime work
// closed while the finding stays open for its other half.
var landedHalves = []landedHalf{
	{
		id:       "F-4.7.25",
		file:     []string{"pkg", "adapter", "intrapodauth.go"},
		marker:   regexp.MustCompile(`(?m)^func authenticateRuntimeConn\(`),
		recorded: "The nonce half is resolved on both runtime listeners",
	},
	{
		id:       "F-4.7.26",
		file:     []string{"pkg", "adapter", "sessionframes.go"},
		marker:   regexp.MustCompile(`session_start`),
		recorded: "The per-session context half is resolved",
	},
}

// diagnosis: an open BUILD-GAPS finding does not record a half that has
// landed in the tree, so a build loop working the finding would re-implement
// the landed half. Add a partial-progress note naming the landed code; do not
// revert the code.
func TestBuildGapsOpenFindingsRecordLandedHalves_spec_4_7_11(t *testing.T) {
	root := repoRoot(t)
	findings := buildGapsFindingsByID(t, root)
	for _, half := range landedHalves {
		t.Run(half.id, func(t *testing.T) {
			f := requireFinding(t, findings, half.id)
			path := filepath.Join(append([]string{root}, half.file...)...)
			if _, err := os.Stat(path); err != nil || !sourceMatches(t, path, half.marker) {
				t.Skipf("not-yet-applicable: %s does not carry the landed half", filepath.Join(half.file...))
			}
			if f.open && !strings.Contains(f.body, half.recorded) {
				t.Errorf("BUILD-GAPS.md:%d %s\n  is open and does not record the landed half (%q) that %s implements",
					f.line, f.heading, half.recorded, filepath.Join(half.file...))
			}
		})
	}
}

// diagnosis: the BUILD-GAPS parser no longer tells an open finding from a
// closed one or no longer collects a finding's body, so the currency checks
// above pass or fail vacuously.
func TestBuildGapsFindingParser_spec_15_7(t *testing.T) {
	root := t.TempDir()
	doc := "# Build gaps\n\n## §1 Section\n\n" +
		"### - [ ] F-1.1 — Open gap [High] — OPEN\n\nopen body\n\n" +
		"### - [x] F-1.2 — Closed gap [Low] — CLOSED\n\nclosed body\n\n" +
		"### - [ ] F-1.3 — Unticked but closed [Low] — CLOSED\n\nother body\n\n### Coverage notes\n\nnot a finding\n"
	if err := os.WriteFile(filepath.Join(root, "BUILD-GAPS.md"), []byte(doc), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	got := buildGapsFindingsByID(t, root)
	cases := map[string]struct {
		open bool
		body string
	}{
		"F-1.1": {open: true, body: "open body"},
		"F-1.2": {open: false, body: "closed body"},
		"F-1.3": {open: false, body: "other body"},
	}
	if len(got) != len(cases) {
		t.Fatalf("parsed %d findings, want %d", len(got), len(cases))
	}
	for id, want := range cases {
		f := got[id]
		if f.open != want.open {
			t.Errorf("%s: open = %v, want %v", id, f.open, want.open)
		}
		if !strings.Contains(f.body, want.body) || strings.Contains(f.body, "not a finding") {
			t.Errorf("%s: body = %q, want it to hold %q and stop at the next heading", id, f.body, want.body)
		}
	}
}

// diagnosis: a declaration probe no longer detects the field, tag, or
// assignment it looks for, so the currency check reports a gap as present
// after its carrier has landed.
func TestRuntimeSDKGapProbes_spec_15_7(t *testing.T) {
	dir := t.TempDir()
	write := func(name, src string) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
			t.Fatalf("write fixture: %v", err)
		}
		return path
	}
	structs := write("types.go", "package x\n"+
		"type ManifestLLM struct {\n\tDeliveryMode string `json:\"deliveryMode\"`\n\tHeaders map[string]string `json:\"headers,omitempty\"`\n}\n"+
		"type TerminationReason struct {\n\tReason string\n}\n")
	names, tags := structFields(t, structs, "ManifestLLM")
	if !tags["headers"] || !tags["deliveryMode"] || !names["Headers"] {
		t.Errorf("structFields(ManifestLLM) = %v, %v; want the Headers field and both json tags", names, tags)
	}
	names, _ = structFields(t, structs, "TerminationReason")
	if names["Code"] || !names["Reason"] {
		t.Errorf("structFields(TerminationReason) = %v; want Reason and no Code", names)
	}

	setsPlan := map[string]string{
		"keyed literal": "package x\ntype R struct{ WorkspacePlan *int }\nfunc f(p *int) R { return R{WorkspacePlan: p} }\n",
		"assignment":    "package x\ntype R struct{ WorkspacePlan *int }\nfunc f(r *R, p *int) { r.WorkspacePlan = p }\n",
	}
	for name, src := range setsPlan {
		file, err := parser.ParseFile(token.NewFileSet(), name+".go", src, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		if !fileSetsField(file, "WorkspacePlan") {
			t.Errorf("fileSetsField(%s) = false, want true", name)
		}
	}
	unset, err := parser.ParseFile(token.NewFileSet(), "unset.go",
		"package x\ntype R struct{ WorkspacePlan *int; ID string }\nfunc f() R { return R{ID: \"a\"} }\n", 0)
	if err != nil {
		t.Fatalf("parse unset: %v", err)
	}
	if fileSetsField(unset, "WorkspacePlan") {
		t.Error("fileSetsField(unset) = true, want false")
	}

	if !protoRuntimeOptionsField.MatchString("message StartSessionRequest {\n  map<string, string> runtime_options = 7;\n}\n") {
		t.Error("protoRuntimeOptionsField misses a runtime_options field")
	}
	if protoRuntimeOptionsField.MatchString("  // runtime_options would carry the caller's options\n") {
		t.Error("protoRuntimeOptionsField matches a comment")
	}
}
