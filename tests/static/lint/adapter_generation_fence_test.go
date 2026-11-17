// SPDX-License-Identifier: MIT

package lint

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/tests/static/internal/adapterproto"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// This gate holds the generation fence on the gateway-to-pod requests. The pod
// validates the coordination generation a gateway-to-pod request carries and
// rejects a stale one, so a request message of the `Adapter` service that
// declares `coordination_generation` is either read by its handler or recorded
// as a gap. The gate lists every such request in the adapter proto and requires
// that the handler the adapter serves it with reads the field, or that an open
// or planned issue under `issues/` names the request message.
//
// It replaces the coverage half of the claim register's proto check, which
// 0116.S5 retired with the register: a request that gains the field without a
// handler that reads it, and without an issue that records the gap, fails here.
//
// Reading the field is the static proxy for comparing it. Every handler that
// enforces the fence today reads the field in its own body through the
// generated getter, so the gate looks for that call in the handler's body. A
// handler that reads the field and then ignores it passes; the contract-tier
// suites own what the handler does with the value.

const (
	// generationFenceField is the fence field a gateway-to-pod request carries.
	generationFenceField = "coordination_generation"
	// generationFenceGetter is the generated getter a handler reads the field
	// through.
	generationFenceGetter = "GetCoordinationGeneration"
	// gatewayToPodService is the proto service the gateway dials on the pod.
	gatewayToPodService = "Adapter"
	// adapterHandlerDir is the package whose `*Server` methods serve it.
	adapterHandlerDir = "pkg/adapter"
	// adapterServerType is the receiver type of those methods.
	adapterServerType = "Server"
	// issueRecordsDir is the folder of the issue records. Its `closed/`
	// subfolder holds closed issues, which record no live gap.
	issueRecordsDir = "issues"
)

// issueStatusLine reads an issue's status from its front matter.
var issueStatusLine = regexp.MustCompile(`(?m)^status:\s*(\w+)`)

// fenceIssue is one open or planned issue record.
type fenceIssue struct {
	path string
	text string
}

// fenceInputs is everything the gate reads: the proto text, the adapter's
// handler declarations by method name, and the open or planned issues.
type fenceInputs struct {
	proto    string
	handlers map[string]*ast.FuncDecl
	issues   []fenceIssue
}

// unfencedRequestFindings returns one finding per gateway-to-pod request that
// carries the fence field with neither a handler that reads it nor an issue
// that names it, and a finding when no request carries the field, which would
// make the gate pass vacuously.
func unfencedRequestFindings(in fenceInputs) []string {
	fields := adapterproto.Fields(in.proto)
	methods := adapterproto.ServiceMethods(in.proto, gatewayToPodService)
	names := make([]string, 0, len(methods))
	for name := range methods {
		names = append(names, name)
	}
	sort.Strings(names)

	var findings []string
	carried := 0
	for _, method := range names {
		request := methods[method]
		if _, ok := fields[request][generationFenceField]; !ok {
			continue
		}
		carried++
		handler, served := in.handlers[method]
		if served && readsGenerationFence(handler) {
			continue
		}
		if issue := issueNamingRequest(in.issues, request); issue != "" {
			continue
		}
		if !served {
			findings = append(findings, fmt.Sprintf(
				"%s.%s carries %s, %s declares no (*%s).%s handler, and no open or planned issue names %s",
				gatewayToPodService, method, generationFenceField, adapterHandlerDir, adapterServerType, method, request,
			))
			continue
		}
		findings = append(findings, fmt.Sprintf(
			"%s.%s carries %s, its handler (*%s).%s does not read it through %s, and no open or planned issue names %s",
			gatewayToPodService, method, generationFenceField, adapterServerType, method, generationFenceGetter, request,
		))
	}
	if carried == 0 {
		findings = append(findings, fmt.Sprintf(
			"no request of the %s service declares %s, so the gate checks nothing",
			gatewayToPodService, generationFenceField,
		))
	}
	return findings
}

// readsGenerationFence reports whether a handler's body calls the fence
// getter.
func readsGenerationFence(fn *ast.FuncDecl) bool {
	if fn.Body == nil {
		return false
	}
	found := false
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		if sel, ok := n.(*ast.SelectorExpr); ok && sel.Sel.Name == generationFenceGetter {
			found = true
		}
		return !found
	})
	return found
}

// issueNamingRequest returns the path of the first issue that names the
// request message as a word together with the fence field, or the empty
// string when none does.
func issueNamingRequest(issues []fenceIssue, request string) string {
	word := regexp.MustCompile(`(^|[^A-Za-z0-9_])` + regexp.QuoteMeta(request) + `($|[^A-Za-z0-9_])`)
	for _, issue := range issues {
		if word.MatchString(issue.text) && strings.Contains(issue.text, generationFenceField) {
			return issue.path
		}
	}
	return ""
}

// adapterHandlers parses Go sources and returns their methods on
// `*Server`, keyed by method name.
func adapterHandlers(sources map[string]string) (map[string]*ast.FuncDecl, error) {
	handlers := map[string]*ast.FuncDecl{}
	fset := token.NewFileSet()
	for name, src := range sources {
		file, err := parser.ParseFile(fset, name, src, parser.SkipObjectResolution)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", name, err)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv == nil || len(fn.Recv.List) == 0 {
				continue
			}
			star, ok := fn.Recv.List[0].Type.(*ast.StarExpr)
			if !ok {
				continue
			}
			if ident, ok := star.X.(*ast.Ident); ok && ident.Name == adapterServerType {
				handlers[fn.Name.Name] = fn
			}
		}
	}
	return handlers, nil
}

// liveIssue reports whether an issue record is open or planned. A record with
// no status line is read as open, as the issue helpers read it.
func liveIssue(text string) bool {
	m := issueStatusLine.FindStringSubmatch(text)
	if m == nil {
		return true
	}
	return m[1] == "open" || m[1] == "planned"
}

// loadFenceInputs reads the proto, the adapter package's non-test sources,
// and the open or planned issues from a tree root.
func loadFenceInputs(root string) (fenceInputs, error) {
	proto, err := os.ReadFile(filepath.Join(root, adapterproto.Path))
	if err != nil {
		return fenceInputs{}, err
	}
	entries, err := os.ReadDir(filepath.Join(root, adapterHandlerDir))
	if err != nil {
		return fenceInputs{}, err
	}
	sources := map[string]string{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(root, adapterHandlerDir, name))
		if err != nil {
			return fenceInputs{}, err
		}
		sources[filepath.Join(adapterHandlerDir, name)] = string(b)
	}
	handlers, err := adapterHandlers(sources)
	if err != nil {
		return fenceInputs{}, err
	}
	issues, err := liveIssues(filepath.Join(root, issueRecordsDir))
	if err != nil {
		return fenceInputs{}, err
	}
	return fenceInputs{proto: string(proto), handlers: handlers, issues: issues}, nil
}

// liveIssues returns the open and planned issue records under dir, leaving
// out its `closed/` subfolder.
func liveIssues(dir string) ([]fenceIssue, error) {
	var issues []fenceIssue
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != dir && d.Name() == "closed" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".md") || d.Name() == "README.md" || d.Name() == "INDEX.md" {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if text := string(b); liveIssue(text) {
			issues = append(issues, fenceIssue{path: path, text: text})
		}
		return nil
	})
	return issues, err
}

// spec: §28.5.1 (Gateway-to-pod), §10.1.2 (Coordinator Handoff Protocol)
//
// Every gateway-to-pod request that carries the coordination generation is
// read by its handler, or an open or planned issue records that it is not.
func TestGatewayToPodGenerationFenceIsReadOrRecorded(t *testing.T) {
	t.Parallel()
	in, err := loadFenceInputs(schematest.RepoRoot(t))
	if err != nil {
		t.Fatalf("read the gate's inputs: %v", err)
	}
	for _, f := range unfencedRequestFindings(in) {
		t.Error(f)
	}
}

// The cases below test the check itself rather than a requirement, so they
// carry no spec annotation.

const fenceFixtureProto = `service Adapter {
  rpc Interrupt(InterruptRequest) returns (InterruptResponse) {}
  rpc Checkpoint(stream CheckpointRequest) returns (stream CheckpointResponse) {}
  rpc CoordinatorFence(CoordinatorFenceRequest) returns (CoordinatorFenceResponse) {}
  rpc StartSession(StartSessionRequest) returns (StartSessionResponse) {}
}

service GatewayControl {
  rpc CallPlatformTool(CallPlatformToolRequest) returns (CallPlatformToolResponse) {}
}

message InterruptRequest {
  SessionId session_id = 1;
  int64 coordination_generation = 4;
}

message CheckpointRequest {
  oneof msg {
    CheckpointStart start = 1;
  }
  int64 coordination_generation = 4;
}

message CoordinatorFenceRequest {
  SessionId session_id = 1;
  int64 coordination_generation = 2;
}

message StartSessionRequest {
  SessionId session_id = 1;
}

message CallPlatformToolRequest {
  int64 coordination_generation = 3;
}
`

const fenceFixtureHandlers = `package adapter

type Server struct{}

func (s *Server) Interrupt(ctx context.Context, req *InterruptRequest) (*InterruptResponse, error) {
	return nil, nil
}

func (s *Server) Checkpoint(stream CheckpointServer) error {
	return nil
}

func (s *Server) CoordinatorFence(ctx context.Context, req *CoordinatorFenceRequest) (*CoordinatorFenceResponse, error) {
	gen := req.GetCoordinationGeneration()
	_ = gen
	return nil, nil
}

func (c client) Interrupt() {}
`

func fenceFixtureInputs(t *testing.T, handlerSrc string, issues ...fenceIssue) fenceInputs {
	t.Helper()
	handlers, err := adapterHandlers(map[string]string{"server.go": handlerSrc})
	if err != nil {
		t.Fatalf("parse the fixture handlers: %v", err)
	}
	return fenceInputs{proto: fenceFixtureProto, handlers: handlers, issues: issues}
}

// The check reports a fenced request whose handler does not read the field and
// that no issue names, and it reads neither a request without the field nor a
// request of the pod-to-gateway service.
func TestGenerationFenceCheckReportsAnUnreadUnrecordedRequest(t *testing.T) {
	t.Parallel()
	got := unfencedRequestFindings(fenceFixtureInputs(t, fenceFixtureHandlers))
	joined := strings.Join(got, "\n")
	for _, want := range []string{"Adapter.Interrupt carries", "Adapter.Checkpoint carries"} {
		if !strings.Contains(joined, want) {
			t.Errorf("findings %q do not report %q", got, want)
		}
	}
	for _, unwanted := range []string{"CoordinatorFence", "StartSession", "CallPlatformTool"} {
		if strings.Contains(joined, unwanted) {
			t.Errorf("findings %q report %s, which the check must accept or not read", got, unwanted)
		}
	}
}

// An open or planned issue that names the request and the field records the
// gap, and a closed issue, an issue that names only the request, and a method
// on another receiver do not.
func TestGenerationFenceCheckAcceptsOnlyALiveIssueNamingTheRequest(t *testing.T) {
	t.Parallel()
	recorded := func(status string) fenceIssue {
		return fenceIssue{path: "issues/CHGWPOD/x.md", text: "---\nstatus: " + status +
			"\n---\n`InterruptRequest` carries `coordination_generation`.\n`CheckpointRequest` carries `coordination_generation`.\n"}
	}
	for _, status := range []string{"open", "planned"} {
		in := fenceFixtureInputs(t, fenceFixtureHandlers, recorded(status))
		if got := unfencedRequestFindings(in); len(got) != 0 {
			t.Errorf("a %s issue naming both requests left findings %q", status, got)
		}
	}
	closed := recorded("closed")
	if !liveIssue(recorded("open").text) || !liveIssue(recorded("planned").text) || liveIssue(closed.text) {
		t.Errorf("liveIssue misreads the status line")
	}
	requestOnly := fenceIssue{path: "issues/CHGWPOD/y.md", text: "---\nstatus: open\n---\n`InterruptRequest` and `CheckpointRequest`.\n"}
	if got := unfencedRequestFindings(fenceFixtureInputs(t, fenceFixtureHandlers, requestOnly)); len(got) != 2 {
		t.Errorf("an issue that names the requests without the field was accepted: %q", got)
	}
	prefix := fenceIssue{path: "issues/CHGWPOD/z.md", text: "---\nstatus: open\n---\n`InterruptRequestV2` carries `coordination_generation`.\n"}
	if got := unfencedRequestFindings(fenceFixtureInputs(t, fenceFixtureHandlers, prefix)); len(got) != 2 {
		t.Errorf("an issue naming a longer message name was read as naming the request: %q", got)
	}
}

// A request with no handler is reported as such, and a handler that reads the
// field passes.
func TestGenerationFenceCheckReportsAMissingHandlerAndAcceptsAReadingOne(t *testing.T) {
	t.Parallel()
	reading := strings.Replace(fenceFixtureHandlers,
		"func (s *Server) Interrupt(ctx context.Context, req *InterruptRequest) (*InterruptResponse, error) {\n\treturn nil, nil",
		"func (s *Server) Interrupt(ctx context.Context, req *InterruptRequest) (*InterruptResponse, error) {\n\t_ = req.GetCoordinationGeneration()\n\treturn nil, nil", 1)
	noCheckpoint := strings.Replace(reading, "func (s *Server) Checkpoint(stream CheckpointServer) error {\n\treturn nil\n}\n", "", 1)
	got := unfencedRequestFindings(fenceFixtureInputs(t, noCheckpoint))
	if len(got) != 1 || !strings.Contains(got[0], "declares no (*Server).Checkpoint handler") {
		t.Errorf("findings %q, want only the missing Checkpoint handler", got)
	}
}

// A proto in which no gateway-to-pod request carries the field is reported, so
// a renamed field or service cannot pass the gate vacuously.
func TestGenerationFenceCheckRefusesAVacuousProto(t *testing.T) {
	t.Parallel()
	in := fenceInputs{proto: "service Adapter {\n  rpc Ping(PingRequest) returns (PingResponse) {}\n}\n\nmessage PingRequest {\n  int64 id = 1;\n}\n"}
	got := unfencedRequestFindings(in)
	if len(got) != 1 || !strings.Contains(got[0], "checks nothing") {
		t.Errorf("findings %q, want the vacuous-gate finding", got)
	}
}
