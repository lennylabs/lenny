// SPDX-License-Identifier: MIT

package runtimescaffold

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

// TestValidateName checks the §24.18 runtime-name format: lowercase
// letters, digits, and hyphens, not starting or ending with a hyphen,
// within the length bound.
func TestValidateName(t *testing.T) {
	good := []string{"echo", "my-agent", "rt0", "claude-code", "a", "a-b-c-1"}
	for _, n := range good {
		if err := validateName(n); err != nil {
			t.Errorf("validateName(%q): unexpected error %v", n, err)
		}
	}
	bad := []string{
		"",                                   // empty
		"My-Agent",                           // uppercase
		"my_agent",                           // underscore is outside [a-z0-9-]
		"-leading",                           // leading hyphen
		"trailing-",                          // trailing hyphen
		"has space",                          // space
		"dots.here",                          // dot
		strings.Repeat("a", maxNameLength+1), // over the length bound
	}
	for _, n := range bad {
		if err := validateName(n); err == nil {
			t.Errorf("validateName(%q): expected an error, got nil", n)
		}
	}
}

// TestIsUnsupportedCombination checks the §24.18 matrix: binary supports
// only minimal; binary+coding and binary+chat are rejected, every other
// pair is accepted.
func TestIsUnsupportedCombination(t *testing.T) {
	cases := []struct {
		lang Language
		tmpl Template
		want bool
	}{
		{LangBinary, TemplateMinimal, false},
		{LangBinary, TemplateCoding, true},
		{LangBinary, TemplateChat, true},
		{LangGo, TemplateMinimal, false},
		{LangGo, TemplateCoding, false},
		{LangGo, TemplateChat, false},
		{LangPython, TemplateCoding, false},
		{LangTypeScript, TemplateChat, false},
	}
	for _, c := range cases {
		if got := IsUnsupportedCombination(c.lang, c.tmpl); got != c.want {
			t.Errorf("IsUnsupportedCombination(%s, %s) = %v, want %v",
				c.lang, c.tmpl, got, c.want)
		}
	}
}

// TestGenerateRejectedCombinationsExit5 checks that binary+coding and
// binary+chat exit 5 and write nothing to the filesystem.
func TestGenerateRejectedCombinationsExit5(t *testing.T) {
	for _, tmpl := range []Template{TemplateCoding, TemplateChat} {
		base := t.TempDir()
		var stdout, stderr bytes.Buffer
		code := Generate(Spec{
			Name:     "rejected",
			Language: LangBinary,
			Template: tmpl,
		}, base, &stdout, &stderr)
		if code != ExitUnsupportedCombination {
			t.Errorf("binary+%s: exit %d, want %d", tmpl, code, ExitUnsupportedCombination)
		}
		// No directory should have been created.
		if _, err := os.Stat(filepath.Join(base, "rejected")); !os.IsNotExist(err) {
			t.Errorf("binary+%s: target directory was created despite rejection", tmpl)
		}
		if !strings.Contains(stderr.String(), "not a supported combination") {
			t.Errorf("binary+%s: stderr lacks an explanation: %q", tmpl, stderr.String())
		}
	}
}

// TestGenerateInvalidNameExit2 checks that an invalid runtime name exits
// 2 before any file is written.
func TestGenerateInvalidNameExit2(t *testing.T) {
	base := t.TempDir()
	var stdout, stderr bytes.Buffer
	code := Generate(Spec{
		Name:     "Invalid_Name",
		Language: LangGo,
		Template: TemplateMinimal,
	}, base, &stdout, &stderr)
	if code != ExitInvalidName {
		t.Fatalf("invalid name: exit %d, want %d", code, ExitInvalidName)
	}
	entries, _ := os.ReadDir(base)
	if len(entries) != 0 {
		t.Errorf("invalid name: %d entries created, want 0", len(entries))
	}
}

// TestGenerateTargetExistsExit3 checks that an existing target directory
// without --force exits 3, and that --force overwrites it.
func TestGenerateTargetExistsExit3(t *testing.T) {
	base := t.TempDir()
	if err := os.Mkdir(filepath.Join(base, "taken"), 0o755); err != nil {
		t.Fatalf("pre-create target: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := Generate(Spec{
		Name:     "taken",
		Language: LangGo,
		Template: TemplateMinimal,
	}, base, &stdout, &stderr)
	if code != ExitTargetExists {
		t.Fatalf("existing target: exit %d, want %d", code, ExitTargetExists)
	}
	if !strings.Contains(stderr.String(), "already exists") {
		t.Errorf("existing target: stderr lacks the cause: %q", stderr.String())
	}

	// --force overwrites the directory and succeeds.
	stdout.Reset()
	stderr.Reset()
	code = Generate(Spec{
		Name:     "taken",
		Language: LangGo,
		Template: TemplateMinimal,
		Force:    true,
	}, base, &stdout, &stderr)
	if code != ExitOK {
		t.Fatalf("existing target with --force: exit %d, want %d, stderr=%q",
			code, ExitOK, stderr.String())
	}
	if !fileExists(filepath.Join(base, "taken", "main.go")) {
		t.Error("existing target with --force: main.go was not written")
	}
}

// TestEmittedFileSetPerCell checks that each supported Language x
// Template cell emits the expected file set.
func TestEmittedFileSetPerCell(t *testing.T) {
	common := []string{
		".github/workflows/ci.yml",
		".gitignore",
		"Dockerfile",
		"Makefile",
		"README.md",
		"runtime.yaml",
	}
	withGo := func(extra ...string) []string {
		return sortedConcat(common, append([]string{"go.mod", "main.go"}, extra...))
	}
	cases := []struct {
		lang Language
		tmpl Template
		want []string
	}{
		{LangGo, TemplateMinimal, withGo()},
		{LangGo, TemplateCoding, withGo()},
		{LangGo, TemplateChat, withGo()},
		{LangBinary, TemplateMinimal, withGo()},
		{
			LangPython, TemplateMinimal,
			sortedConcat(common, []string{"main.py", "pyproject.toml", "requirements.txt"}),
		},
		{
			LangPython, TemplateCoding,
			sortedConcat(common, []string{"main.py", "pyproject.toml", "requirements.txt"}),
		},
		{
			LangTypeScript, TemplateMinimal,
			sortedConcat(common, []string{"main.ts", "package.json", "tsconfig.json"}),
		},
		{
			LangTypeScript, TemplateChat,
			sortedConcat(common, []string{"main.ts", "package.json", "tsconfig.json"}),
		},
	}
	for _, c := range cases {
		got := EmittedFiles(c.lang, c.tmpl)
		if !equalStrings(got, c.want) {
			t.Errorf("EmittedFiles(%s, %s) = %v, want %v", c.lang, c.tmpl, got, c.want)
		}
	}
}

// TestGenerateWritesFileSet checks that a successful Generate writes
// exactly the file set EmittedFiles reports, for every supported cell.
func TestGenerateWritesFileSet(t *testing.T) {
	cells := []struct {
		lang Language
		tmpl Template
	}{
		{LangGo, TemplateMinimal},
		{LangGo, TemplateCoding},
		{LangGo, TemplateChat},
		{LangPython, TemplateMinimal},
		{LangPython, TemplateCoding},
		{LangPython, TemplateChat},
		{LangTypeScript, TemplateMinimal},
		{LangTypeScript, TemplateCoding},
		{LangTypeScript, TemplateChat},
		{LangBinary, TemplateMinimal},
	}
	for _, c := range cells {
		base := t.TempDir()
		var stdout, stderr bytes.Buffer
		code := Generate(Spec{
			Name:     "rt",
			Language: c.lang,
			Template: c.tmpl,
		}, base, &stdout, &stderr)
		if code != ExitOK {
			t.Errorf("%s/%s: exit %d, want 0, stderr=%q", c.lang, c.tmpl, code, stderr.String())
			continue
		}
		got := walkRelFiles(t, filepath.Join(base, "rt"))
		want := EmittedFiles(c.lang, c.tmpl)
		if !equalStrings(got, want) {
			t.Errorf("%s/%s: wrote %v, want %v", c.lang, c.tmpl, got, want)
		}
		// The runtime.yaml declares the integration level the matrix
		// specifies for the cell.
		level := integrationLevelFor(c.lang, c.tmpl)
		manifest, _ := os.ReadFile(filepath.Join(base, "rt", "runtime.yaml"))
		if !strings.Contains(string(manifest), "integrationLevel: "+level) {
			t.Errorf("%s/%s: runtime.yaml lacks integrationLevel: %s", c.lang, c.tmpl, level)
		}
		// spec: §5.1 line 51 ("Labels are required from v1"): the admin
		// registration path (POST /v1/admin/runtimes, the path `lenny
		// runtime publish` and `lenny-ctl admin runtimes register` both
		// use) unconditionally rejects a manifest with no labels. A
		// scaffold with none can never be published as scaffolded.
		if !strings.Contains(string(manifest), "labels:") {
			t.Errorf("%s/%s: runtime.yaml has no labels field; POST /v1/admin/runtimes "+
				"rejects a runtime with no labels (§5.1 line 51)", c.lang, c.tmpl)
		}
	}
}

// TestScaffoldedREADMEPointsToBecomingReferenceRuntime_spec_26_12
// asserts the scaffolder-emitted README links the §26.12 proposal flow
// so a runtime author following the documented entrypoint sees the
// path to the first-party reference catalog and the three artifacts
// the proposal carries (scaffolded skeleton, conformance results,
// appendix entry).
// spec: §26.12.
func TestScaffoldedREADMEPointsToBecomingReferenceRuntime_spec_26_12(t *testing.T) {
	base := t.TempDir()
	var stdout, stderr bytes.Buffer
	if code := Generate(Spec{
		Name:     "rt",
		Language: LangGo,
		Template: TemplateMinimal,
	}, base, &stdout, &stderr); code != ExitOK {
		t.Fatalf("generate: exit %d, stderr=%q", code, stderr.String())
	}
	raw, err := os.ReadFile(filepath.Join(base, "rt", "README.md"))
	if err != nil {
		t.Fatalf("read README: %v", err)
	}
	body := string(raw)
	for _, marker := range []string{
		"Becoming a reference runtime",
		"§26.12",
		"github.com/lennylabs/runtime-templates",
		"lenny runtime validate",
		"§15.4.6",
	} {
		if !strings.Contains(body, marker) {
			t.Errorf("scaffolded README lacks marker %q", marker)
		}
	}
}

// TestGenerateBinaryMinimalHasNoSDKImport checks that the binary+minimal
// skeleton carries no runtime-author SDK dependency (§24.18 Basic-level
// "zero Lenny knowledge" promise).
func TestGenerateBinaryMinimalHasNoSDKImport(t *testing.T) {
	base := t.TempDir()
	var stdout, stderr bytes.Buffer
	if code := Generate(Spec{
		Name:     "raw",
		Language: LangBinary,
		Template: TemplateMinimal,
	}, base, &stdout, &stderr); code != ExitOK {
		t.Fatalf("binary/minimal: exit %d, stderr=%q", code, stderr.String())
	}
	dir := filepath.Join(base, "raw")
	for _, f := range []string{"main.go", "go.mod", "Dockerfile"} {
		raw, err := os.ReadFile(filepath.Join(dir, f))
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		for _, marker := range []string{
			"runtime-sdk-go", "lenny-runtime", "@lennylabs/runtime-sdk",
		} {
			if strings.Contains(string(raw), marker) {
				t.Errorf("binary/minimal %s references the SDK marker %q", f, marker)
			}
		}
	}
	// The binary skeleton declares integrationLevel: basic.
	manifest, _ := os.ReadFile(filepath.Join(dir, "runtime.yaml"))
	if !strings.Contains(string(manifest), "integrationLevel: basic") {
		t.Error("binary/minimal runtime.yaml is not Basic level")
	}
}

// TestGenerateGoCellsCompile generates each Go-language cell into a temp
// directory and confirms the emitted Go project compiles against the
// in-repo runtime-author SDK. The published SDK module
// (github.com/lennylabs/runtime-sdk-go) is replaced with a temp module
// built from the in-repo SDK source so the build needs no network.
func TestGenerateGoCellsCompile(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping compile test in -short mode")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not on PATH")
	}

	sdkMod := buildSDKReplacementModule(t)

	cells := []struct {
		lang Language
		tmpl Template
	}{
		{LangGo, TemplateMinimal},
		{LangGo, TemplateCoding},
		{LangGo, TemplateChat},
		{LangBinary, TemplateMinimal},
	}
	for _, c := range cells {
		c := c
		t.Run(string(c.lang)+"-"+string(c.tmpl), func(t *testing.T) {
			base := t.TempDir()
			var stdout, stderr bytes.Buffer
			if code := Generate(Spec{
				Name:     "buildme",
				Language: c.lang,
				Template: c.tmpl,
			}, base, &stdout, &stderr); code != ExitOK {
				t.Fatalf("generate: exit %d, stderr=%q", code, stderr.String())
			}
			dir := filepath.Join(base, "buildme")

			// The binary cell has no SDK dependency, so it builds as
			// generated. The SDK cells need the replace directive.
			if c.lang != LangBinary {
				appendToFile(t, filepath.Join(dir, "go.mod"),
					"\nreplace github.com/lennylabs/runtime-sdk-go => "+sdkMod+"\n")
			}

			runGo(t, dir, "build", "./...")
		})
	}
}

// buildSDKReplacementModule copies the in-repo Go runtime-author SDK
// into a temp directory and adds a go.mod declaring the published
// module path, so a generated skeleton can `replace` the published
// module with it. The in-repo SDK package imports only the standard
// library, so the single-directory copy is self-contained.
func buildSDKReplacementModule(t *testing.T) string {
	t.Helper()
	repoRoot := findRepoRoot(t)
	srcDir := filepath.Join(repoRoot, "sdks", "runtime", "go", "runtime")
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		t.Fatalf("read SDK source %s: %v", srcDir, err)
	}
	dst := t.TempDir()
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		// The skeleton compiles against the SDK's exported surface; the
		// SDK's own test files are not needed and would drag in test
		// helpers.
		if strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(srcDir, e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		if err := os.WriteFile(filepath.Join(dst, e.Name()), raw, 0o644); err != nil {
			t.Fatalf("write %s: %v", e.Name(), err)
		}
	}
	if err := os.WriteFile(filepath.Join(dst, "go.mod"),
		[]byte("module github.com/lennylabs/runtime-sdk-go\n\ngo 1.25\n"), 0o644); err != nil {
		t.Fatalf("write SDK go.mod: %v", err)
	}
	return dst
}

// findRepoRoot walks up from the test file until it finds the
// repository go.mod (module github.com/lennylabs/lenny).
func findRepoRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	dir := filepath.Dir(thisFile)
	for {
		gomod := filepath.Join(dir, "go.mod")
		if raw, err := os.ReadFile(gomod); err == nil &&
			strings.Contains(string(raw), "module github.com/lennylabs/lenny") {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not locate the repository root from the test file")
		}
		dir = parent
	}
}

// runGo runs a go subcommand in dir and fails the test on a non-zero
// exit, reporting the combined output.
func runGo(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("go", args...)
	cmd.Dir = dir
	// Resolve the module graph offline where possible; the replacement
	// module is local and the binary cell has no dependencies.
	cmd.Env = append(os.Environ(), "GOFLAGS=-mod=mod")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go %s in %s failed: %v\n%s", strings.Join(args, " "), dir, err, out)
	}
}

// appendToFile appends content to the file at path.
func appendToFile(t *testing.T, path, content string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open %s for append: %v", path, err)
	}
	defer f.Close()
	if _, err := f.WriteString(content); err != nil {
		t.Fatalf("append to %s: %v", path, err)
	}
}

// walkRelFiles returns every regular file under root, as paths relative
// to root with forward slashes, sorted.
func walkRelFiles(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	sort.Strings(out)
	return out
}

// sortedConcat concatenates the slices and returns a sorted copy.
func sortedConcat(a, b []string) []string {
	out := make([]string, 0, len(a)+len(b))
	out = append(out, a...)
	out = append(out, b...)
	sort.Strings(out)
	return out
}

// equalStrings reports whether a and b have the same elements in the
// same order.
func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
