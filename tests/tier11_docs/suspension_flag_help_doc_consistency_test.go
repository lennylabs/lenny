// SPDX-License-Identifier: MIT

// Tier-11 consistency checks between the staged session-suspension and
// resume contract, the gateway's flag help, and every reader page. The
// retired restore-failure error code and the retired per-session retry
// budget key appear on no published page and in no gateway flag help, and
// the help of each suspension and recovery flag states the default and the
// behavior the specification gives it.
//
// The tests read the repository state directly (no build tag, no
// infrastructure), the same posture as the other tier-11 doc checks.
//
// spec: 15.1 (REST API), 7.3 (Retry and Resume), 6.2 (Pod State Machine),
// 11.3 (Timeouts and Cancellation)

package tier11_docs_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/runtime/watchdog"
)

// retiredReaderTerms are the identifiers the change retired: the resume
// call's restore-failure error code and the per-session retry budget that
// retryPolicy.maxRetries replaced.
var retiredReaderTerms = []string{retiredResumeErrorCode, "maxSessionRetries"}

// spec: 15.1 (REST API), 7.3 (Retry and Resume)
// diagnosis: a published page under docs/ still names RESUME_FAILED or
// sessionPolicy.maxSessionRetries. A client that branches on the error code
// handles an answer no resume call returns, and an operator who sets the
// retry key configures a budget the gateway never reads.
func TestNoReaderPageNamesTheRetiredResumeTerms(t *testing.T) {
	root := repoRoot(t)
	docs := filepath.Join(root, "docs")
	err := filepath.WalkDir(docs, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(path) != ".md" {
			return err
		}
		body := readDocPage(t, path)
		rel, _ := filepath.Rel(root, path)
		for _, term := range retiredReaderTerms {
			if strings.Contains(body, term) {
				t.Errorf("%s names %s", rel, term)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", docs, err)
	}
}

// suspensionFlag is one gateway flag whose help states a staged default.
type suspensionFlag struct {
	name    string
	def     int
	codeDef int
	want    []string
}

// suspensionFlags are the suspension and recovery flags and the defaults
// and behavior the specification gives them.
var suspensionFlags = []suspensionFlag{
	{"max-client-idle-seconds", 900, watchdog.DefaultMaxIdleSeconds, []string{"suspended", "releases the pod", "expired"}},
	{"max-suspended-session-seconds", 604800, watchdog.DefaultMaxSuspendedSessionSeconds, []string{"`suspended`", "expired"}},
	{"max-suspended-pod-hold-seconds", 900, watchdog.DefaultMaxSuspendedPodHoldSeconds, []string{"interrupt-suspended", "releases the pod"}},
	{"max-session-age-seconds", 7200, watchdog.DefaultMaxSessionAgeSeconds, []string{"active-time", "paused while it is suspended, resume_pending, resuming, or awaiting_client_action"}},
	{"suspended-backlog-recheck-seconds", 300, -1, []string{"podless `suspended` session that holds a message"}},
}

// spec: 6.2 (Pod State Machine), 11.3 (Timeouts and Cancellation), 7.3
// (Retry and Resume)
// diagnosis: the gateway's help for a suspension or recovery flag disagrees
// with the specification: its stated default or the compiled default differs
// from the specified value, it no longer describes the behavior the flag
// controls, or a flag's help still names a retired resume term. An operator
// reading --help then tunes a behavior the gateway does not have.
func TestGatewayFlagHelpMatchesTheSuspensionContract(t *testing.T) {
	usage := gatewayFlagUsage(t)
	for _, f := range suspensionFlags {
		t.Run(f.name, func(t *testing.T) {
			help, ok := usage[f.name]
			if !ok {
				t.Fatalf("cmd/lenny-gateway registers no --%s flag", f.name)
			}
			if !strings.Contains(help, "Default "+strconv.Itoa(f.def)+"s") {
				t.Errorf("--%s help does not state the specified default %ds: %s", f.name, f.def, help)
			}
			if f.codeDef >= 0 && f.codeDef != f.def {
				t.Errorf("--%s compiled default = %d, want the specified %d", f.name, f.codeDef, f.def)
			}
			for _, w := range f.want {
				if !strings.Contains(help, w) {
					t.Errorf("--%s help does not contain %q: %s", f.name, w, help)
				}
			}
		})
	}
	for name, help := range usage {
		for _, term := range retiredReaderTerms {
			if strings.Contains(help, term) {
				t.Errorf("--%s help names %s", name, term)
			}
		}
	}
}

// gatewayFlagUsage parses cmd/lenny-gateway/flags.go and returns the usage
// string of every flag.Int, flag.String, flag.Bool, and flag.Duration call
// whose name and usage are string literals.
func gatewayFlagUsage(t *testing.T) map[string]string {
	t.Helper()
	path := filepath.Join(repoRoot(t), "cmd", "lenny-gateway", "flags.go")
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	out := map[string]string{}
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || len(call.Args) != 3 {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if pkg, ok := sel.X.(*ast.Ident); !ok || pkg.Name != "flag" {
			return true
		}
		name, okName := stringLiteral(call.Args[0])
		help, okHelp := stringLiteral(call.Args[2])
		if okName && okHelp {
			out[name] = help
		}
		return true
	})
	if len(out) == 0 {
		t.Fatalf("found no flag registrations in %s", path)
	}
	return out
}

// stringLiteral returns the value of a string literal or a concatenation of
// string literals.
func stringLiteral(e ast.Expr) (string, bool) {
	switch v := e.(type) {
	case *ast.BasicLit:
		if v.Kind != token.STRING {
			return "", false
		}
		s, err := strconv.Unquote(v.Value)
		return s, err == nil
	case *ast.BinaryExpr:
		if v.Op != token.ADD {
			return "", false
		}
		l, okL := stringLiteral(v.X)
		r, okR := stringLiteral(v.Y)
		return l + r, okL && okR
	}
	return "", false
}
