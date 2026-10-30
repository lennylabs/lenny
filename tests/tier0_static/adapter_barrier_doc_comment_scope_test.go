// SPDX-License-Identifier: MIT

package tier0_static

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// The adapter's own `CheckpointBarrier` doc comment is the rule as an
// implementer of the pod side reads it, and it sits outside the reach of the
// proto-comment gate next to it. §10.1.2 step 3 validates a gateway-to-pod
// RPC against the generation the pod holds for the session the RPC names,
// and §10.1.8 step 1 accepts a barrier naming a bound session for which the
// pod holds no fenced generation. This gate pins that wording against the
// pod-wide sentence the comment carried while the fenced generation was one
// value per process.

// barrierDocFileRel is the adapter source carrying the handler.
const barrierDocFileRel = "pkg/adapter/coordination.go"

// barrierDocFunc is the handler whose doc comment the gate reads.
const barrierDocFunc = "CheckpointBarrier"

// barrierDocWant are the phrases the normalized doc comment must carry.
var barrierDocWant = []string{
	"against the generation the pod holds for the session the request names",
	"a barrier naming a bound session for which the pod holds no fenced generation is accepted and records no value",
}

// barrierDocReject is the pod-wide wording the comment must not carry. The
// gate holds the whole phrase rather than the words "last fenced" alone,
// because the rejection message the handler returns names a last fenced
// value for one session and is a legitimate use of those words.
var barrierDocReject = []string{
	"against the last fenced value",
	"against the last fenced generation",
}

// goDocComment returns the doc comment of the named top-level function or
// method in src, collapsed to one line of prose so a phrase matches without
// depending on where the comment wraps.
func goDocComment(src, name string) (string, bool) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "src.go", src, parser.ParseComments)
	if err != nil {
		return "", false
	}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name == nil || fn.Name.Name != name || fn.Doc == nil {
			continue
		}
		var b strings.Builder
		for _, c := range fn.Doc.List {
			b.WriteString(" ")
			b.WriteString(strings.TrimSpace(strings.TrimPrefix(c.Text, "//")))
		}
		return strings.Join(strings.Fields(b.String()), " "), true
	}
	return "", false
}

// barrierDocViolations reports every way the handler's doc comment fails the
// gate: a comment that is missing, one that omits a phrase the session unit
// requires, and one that still carries the pod-wide wording.
func barrierDocViolations(src string) []string {
	var out []string
	text, ok := goDocComment(src, barrierDocFunc)
	if !ok {
		return []string{barrierDocFunc + ": doc comment not found"}
	}
	for _, want := range barrierDocWant {
		if !strings.Contains(text, want) {
			out = append(out, barrierDocFunc+": missing "+want)
		}
	}
	for _, reject := range barrierDocReject {
		if strings.Contains(text, reject) {
			out = append(out, barrierDocFunc+": carries "+reject)
		}
	}
	sort.Strings(out)
	return out
}

// spec: 10.1.2 (coordinator handoff protocol, step 3), 10.1.8 (rolling
//
//	updates and the CheckpointBarrier protocol, step 1), 4.7 (runtime
//	adapter)
//
// diagnosis: the adapter's CheckpointBarrier doc comment states the
//
//	coordination generation as one value per pod and states the
//	validation unconditionally. A maintainer reading it reconstructs a
//	gate that refuses the drain barrier of a bound session no
//	coordinator has fenced on this pod, which is the barrier §10.1.8
//	step 1 accepts.
func TestCheckpointBarrierDocCommentIsSessionScoped(t *testing.T) {
	t.Parallel()

	path := filepath.Join(schematest.RepoRoot(t), barrierDocFileRel)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", barrierDocFileRel, err)
	}
	if got := barrierDocViolations(string(data)); len(got) > 0 {
		t.Errorf("%s %s doc comment: %v", barrierDocFileRel, barrierDocFunc, got)
	}
}

// spec: 10.1.2 (coordinator handoff protocol, step 3), 10.1.8 (rolling
//
//	updates and the CheckpointBarrier protocol, step 1)
//
// diagnosis: the gate no longer detects the pod-wide doc-comment wording, so
//
//	the adapter's own statement of the barrier rule can regress to one
//	value per pod with the tier green.
func TestBarrierDocCommentGateDetectsPodWideWording(t *testing.T) {
	t.Parallel()

	const podWide = `package adapter

// CheckpointBarrier implements the graceful-drain barrier RPC. The adapter
// validates the request's coordination generation against the last fenced
// value, quiesces tool-call dispatch, and holds the quiesced state open.
func (s *Server) CheckpointBarrier() {}
`
	got := barrierDocViolations(podWide)
	joined := strings.Join(got, "\n")
	for _, want := range []string{
		barrierDocFunc + ": carries against the last fenced value",
		barrierDocFunc + ": missing against the generation the pod holds for the session the request names",
		barrierDocFunc + ": missing a barrier naming a bound session for which the pod holds no fenced generation is accepted and records no value",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("gate did not report %q, got:\n%s", want, joined)
		}
	}

	if got := barrierDocViolations("package adapter\n"); len(got) != 1 || got[0] != barrierDocFunc+": doc comment not found" {
		t.Errorf("gate on a source without the handler: got %v", got)
	}
}
