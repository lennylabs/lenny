// SPDX-License-Identifier: MIT

package journeys

import (
	"context"
	"encoding/base64"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// These tests exercise contributor tooling and carry no spec annotation.

func TestRecordStreamRedactsATokenSplitAcrossWrites(t *testing.T) {
	tok := testJWT(t, testJWTHeader)
	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	if err := os.WriteFile(path, []byte("old content\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	w, err := NewRedactor("known-secret-value").Create(path)
	if err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","content":"` + tok + ` known-secret-value"}` + "\n"
	for _, chunk := range []string{line[:30], line[30:200], line[200:], `{"partial":"` + tok[:100], tok[100:] + `"}`} {
		if n, err := w.Write([]byte(chunk)); err != nil || n != len(chunk) {
			t.Fatalf("write = %d, %v", n, err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"type":"user","content":"` + Redacted + ` ` + Redacted + `"}` + "\n" + `{"partial":"` + Redacted + `"}`
	if string(b) != want {
		t.Errorf("transcript = %q, want %q", b, want)
	}
	if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("transcript mode: %v, %v; want 0600", fi, err)
	}
}

type failingFile struct{ writeErr, closeErr error }

func (f failingFile) Write(p []byte) (int, error) { return 0, f.writeErr }
func (f failingFile) Close() error                { return f.closeErr }

func TestRecordStreamReportsWriteAndCloseFailures(t *testing.T) {
	diskFull, closeFailed := errors.New("disk full"), errors.New("close failed")
	s := &recordStream{f: failingFile{writeErr: diskFull, closeErr: closeFailed}, red: NewRedactor()}
	if n, err := s.Write([]byte("partial line")); err != nil || n != len("partial line") {
		t.Fatalf("a partial line is held: %d, %v", n, err)
	}
	if _, err := s.Write([]byte(" ends\n")); !errors.Is(err, diskFull) {
		t.Errorf("a failed write of a complete line returned %v", err)
	}
	s = &recordStream{f: failingFile{writeErr: diskFull, closeErr: closeFailed}, red: NewRedactor(), partial: []byte("tail")}
	if err := s.Close(); !errors.Is(err, diskFull) || !errors.Is(err, closeFailed) {
		t.Errorf("close returned %v, want both failures", err)
	}
	if _, err := NewRedactor().Create(filepath.Join(t.TempDir(), "missing", "transcript.jsonl")); err == nil {
		t.Error("creating a record in a missing directory succeeded")
	}
}

func TestRedactorWritesRecordFilesRedactedAndPrivate(t *testing.T) {
	tok := testJWT(t, testJWTHeader)
	b64 := base64.StdEncoding.EncodeToString([]byte(tok))
	dir := t.TempDir()
	logPath := filepath.Join(dir, "provision.log")
	if err := os.WriteFile(logPath, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	r := NewRedactor("known-secret-value")
	if err := r.WriteFile(logPath, []byte("minted "+tok+"\nsecret "+b64+"\nknown known-secret-value\n")); err != nil {
		t.Fatal(err)
	}
	jsonPath := filepath.Join(dir, "result.json")
	if err := r.WriteJSON(jsonPath, map[string]string{"finalText": "the token is " + tok}); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{logPath, jsonPath} {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		assertRemoved(t, filepath.Base(p), string(b), tok)
		assertRemoved(t, filepath.Base(p), string(b), b64)
		if strings.Contains(string(b), "known-secret-value") {
			t.Errorf("%s keeps a known value: %s", p, b)
		}
		if fi, err := os.Stat(p); err != nil || fi.Mode().Perm() != 0o600 {
			t.Errorf("%s mode: %v, %v; want 0600", p, fi, err)
		}
	}
	if err := r.WriteJSON(filepath.Join(dir, "bad.json"), map[string]any{"c": make(chan int)}); err == nil {
		t.Error("a value JSON cannot encode was written")
	}
	if err := r.WriteFile(filepath.Join(dir, "missing", "x.log"), nil); err == nil {
		t.Error("writing a record in a missing directory succeeded")
	}
}

// fileWriters are the functions that create or write files directly.
var fileWriters = map[string]bool{"WriteFile": true, "Create": true, "OpenFile": true, "CreateTemp": true}

// nonRecordWriters are the functions, other than the record writers in
// record.go, that write files directly, with what each writes. None of
// them writes under the results directory's run records.
var nonRecordWriters = map[string]string{
	"ledger.go:writeFileAtomic":       "the pilot ledger, which holds costs, outcomes, and identifiers",
	"ledger.go:AcquireLock":           "the empty host lock file",
	"grant.go:writeReadOnly":          "a granted credential in the trial's scratch directory, which the agent reads",
	"kind.go:(KindTarget).Create":     "the Kind cluster configuration in the target's scratch directory",
	"kind.go:(KindTarget).installEnv": "the install values overlay in the target's scratch directory",
	"kind.go:(KindTarget).owner":      "a temporary kubeconfig in the target's scratch directory, for reading the cluster's owner labels",
	"runner.go:copyFile":              "the documentation and chart copies in the trial's scratch directory",
	"probe.go:probeRun":               "the probe's documentation page in its scratch directory",
}

// recordWriters are the functions every record file is written through.
var recordWriters = map[string]bool{"record.go:(Redactor).WriteFile": true, "record.go:(Redactor).Create": true}

// TestOnlyRecordWritersCreateFiles reads the package's source and
// cmd_journeys.go, and fails when a function outside record.go and the
// listed non-record writers calls os.WriteFile, os.Create, os.OpenFile,
// or os.CreateTemp, so a new record path cannot write a file without the
// redactor. It also fails when a listed writer no longer exists, so the
// list stays exact.
func TestOnlyRecordWritersCreateFiles(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	files = append(files, filepath.Join("..", "cmd_journeys.go"))
	seen := map[string]bool{}
	fset := token.NewFileSet()
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			key := filepath.Base(path) + ":" + funcName(fn)
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				if call, ok := n.(*ast.CallExpr); ok && isOSFileWrite(call) {
					seen[key] = true
					if !recordWriters[key] && nonRecordWriters[key] == "" {
						t.Errorf("%s writes a file at %s outside the record writers in record.go; write a record through the Redactor, or list a non-record file in nonRecordWriters",
							key, fset.Position(call.Pos()))
					}
				}
				return true
			})
		}
	}
	var stale []string
	for key := range nonRecordWriters {
		if !seen[key] {
			stale = append(stale, key)
		}
	}
	for key := range recordWriters {
		if !seen[key] {
			stale = append(stale, key)
		}
	}
	sort.Strings(stale)
	for _, key := range stale {
		t.Errorf("%s is listed as a file writer but writes no file", key)
	}
}

func funcName(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return fn.Name.Name
	}
	typ := fn.Recv.List[0].Type
	if star, ok := typ.(*ast.StarExpr); ok {
		typ = star.X
	}
	if id, ok := typ.(*ast.Ident); ok {
		return "(" + id.Name + ")." + fn.Name.Name
	}
	return fn.Name.Name
}

func isOSFileWrite(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "os" && fileWriters[sel.Sel.Name]
}

// TestRunnerRecordsHoldNoCredentialTheHarnessNeverSaw runs a trial whose
// cluster logs, transcript, standard error, agent result, and
// postcondition output all carry a JWT, its base64 form, and an OAuth
// token that the harness was never given, as an agent that read the
// platform-admin token from a Secret would leave them, and checks that no
// file under the run's records keeps any of them.
func TestRunnerRecordsHoldNoCredentialTheHarnessNeverSaw(t *testing.T) {
	tok := testJWT(t, testJWTHeader)
	b64 := base64.StdEncoding.EncodeToString([]byte(tok))
	oauth := "sk-ant-" + "oat01-" + strings.Repeat("AbCd-_09", 12)
	leak := `{"type":"user","content":"` + tok[:60] + `\n` + tok[60:] + `\n` + b64 + ` ` + oauth + `"}`
	fx := newRunnerFixture(t, func(ft *fakeTarget) { ft.leak = "minted " + tok + "\n" })
	fx.r.Trials = 1
	fx.agent.leak = leak
	fx.agent.result.FinalText = "The admin token is " + tok
	fx.agent.result.StopDetail = "success " + b64
	fx.access.kubectl["get pods"] = []string{"Running " + tok}
	if _, err := fx.r.Run(context.Background(), []*Scenario{fixtureScenario(SetupPrerequisites)}); err != nil {
		t.Fatal(err)
	}
	var checked []string
	err := filepath.WalkDir(filepath.Join(fx.results, "run1"), func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		checked = append(checked, filepath.Base(p))
		for _, secret := range []string{tok, b64, oauth} {
			for i := 0; i+16 <= len(secret); i += 8 {
				if strings.Contains(string(b), secret[i:i+16]) {
					t.Errorf("%s keeps %q of a credential", p, secret[i:i+16])
					break
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(checked)
	want := "agent-stderr.log,prompt.md,provision.log,result.json,settings.json,summary.json,summary.md,teardown.log,transcript.jsonl"
	if strings.Join(checked, ",") != want {
		t.Errorf("checked the records %v, want %s", checked, want)
	}
}
