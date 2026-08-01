// SPDX-License-Identifier: MIT

package tier0_static

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/cmd/lenny-test/verdictstatus"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// The generated gRPC bindings under pkg/proto/ are derived from
// schemas/*.proto by the `generate-proto` make target. This test
// reproduces that target into a temporary directory and diffs the
// result against the committed stubs, so a hand edit to a generated
// file, or a proto change committed without a regeneration, fails the
// static tier.
//
// The producer is the whole target rather than `buf generate` alone.
// The target prepends $(go env GOPATH)/bin to PATH so the local
// protoc-gen-go and protoc-gen-go-grpc plugins resolve, then applies
// two post-generation steps that the plugins do not perform: it
// prepends the SPDX header to every generated file that lacks one, and
// it regroups the import block with goimports under the local prefix.
// Diffing raw plugin output against the committed stubs would report
// drift on every file.
//
// A tree missing any of the producer's four binaries reports the
// unverified marker and leaves the tier unverified. Returning early
// without the marker would certify pkg/proto/ on a host that ran no
// generator, which is the fail-open behavior of the shell check this
// test replaces.

const (
	// protoSchemaDir holds the .proto sources and the buf generate
	// config. The producer runs with this as its working directory
	// because the config's out: paths are relative to it.
	protoSchemaDir = "schemas"
	// protoStubDir holds the committed generated bindings.
	protoStubDir = "pkg/proto"
	// protoLocalPrefix is the goimports -local argument the target
	// passes, which decides how the generated import block is
	// grouped.
	protoLocalPrefix = "github.com/lennylabs/lenny"
	// protoSPDXHeader is the header the target prepends to a
	// generated file that lacks one.
	protoSPDXHeader = "// SPDX-License-Identifier: MIT\n\n"
	// protoSPDXMarker identifies a file that already carries the
	// header on its first line.
	protoSPDXMarker = "SPDX-License-Identifier"
)

// protoProducerBinaries are the external binaries the target needs.
// buf runs the generation; protoc-gen-go and protoc-gen-go-grpc are
// declared as local: plugins in schemas/buf.gen.yaml and are resolved
// from PATH by buf; goimports applies the target's final rewrite.
var protoProducerBinaries = []string{"buf", "protoc-gen-go", "protoc-gen-go-grpc", "goimports"}

// protoProducerTimeout bounds the whole reproduced run.
const protoProducerTimeout = 5 * time.Minute

func TestProtoStubsMatchGeneratedOutput(t *testing.T) {
	root := schematest.RepoRoot(t)
	ctx, cancel := context.WithTimeout(context.Background(), protoProducerTimeout)
	defer cancel()

	binDir, err := goPathBin(ctx)
	if err != nil {
		t.Fatalf("locate GOPATH bin: %v", err)
	}
	// The target's PATH prepend. Setting it on the process makes both
	// the lookup below and every child process see the same PATH the
	// target runs with, so a tree whose plugins are installed only in
	// GOPATH/bin verifies here exactly as `make generate-proto`
	// succeeds there.
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	if missing := missingProducerBinaries(protoProducerBinaries, lookOnPath); len(missing) > 0 {
		// Written to stdout rather than through t.Log so a non-verbose
		// run carries it too. The tier-0 composer reads this line and
		// records the tier unverified.
		fmt.Println(unverifiedLine(protoUnverifiedReason(missing)))
		return
	}

	generated, err := reproduceProtoProducer(ctx, root, t.TempDir())
	if err != nil {
		t.Fatalf("reproduce the proto producer: %v", err)
	}
	committed, err := collectGoTree(filepath.Join(root, protoStubDir))
	if err != nil {
		t.Fatalf("read the committed stubs: %v", err)
	}
	if err := compareProtoTrees(committed, generated); err != nil {
		t.Fatalf("%v\n\nRun `make generate-proto` and commit the result.", err)
	}
}

// goPathBin returns the bin directory of the active GOPATH, which is
// where scripts/setup-dev.sh installs the codegen plugins and
// goimports.
func goPathBin(ctx context.Context) (string, error) {
	out, err := exec.CommandContext(ctx, "go", "env", "GOPATH").Output()
	if err != nil {
		return "", fmt.Errorf("go env GOPATH: %w", err)
	}
	gopath := strings.TrimSpace(string(out))
	if gopath == "" {
		return "", fmt.Errorf("go env GOPATH is empty")
	}
	return filepath.Join(gopath, "bin"), nil
}

// lookOnPath reports whether a binary resolves on the current PATH.
func lookOnPath(name string) error {
	_, err := exec.LookPath(name)
	return err
}

// missingProducerBinaries returns, in the given order, the binaries the
// lookup cannot resolve. The lookup is injected so the unavailable-
// binary path is exercised without altering the host.
func missingProducerBinaries(names []string, look func(string) error) []string {
	var missing []string
	for _, name := range names {
		if err := look(name); err != nil {
			missing = append(missing, name)
		}
	}
	return missing
}

// protoUnverifiedReason states which binaries were unavailable, so the
// tier's unverified note names what a host has to install.
func protoUnverifiedReason(missing []string) string {
	return fmt.Sprintf("proto no-drift: producer binaries unavailable on PATH: %s", strings.Join(missing, ", "))
}

// unverifiedLine formats the stdout sentinel a tier-0 Go test writes to
// report that it reached no conclusion.
func unverifiedLine(reason string) string {
	return verdictstatus.UnverifiedMarker + " " + reason
}

// reproduceProtoProducer runs the generate-proto target into a
// temporary base directory and returns the generated tree keyed by the
// path each file would occupy under pkg/proto/.
func reproduceProtoProducer(ctx context.Context, root, base string) (map[string]string, error) {
	// buf prepends the -o value to the out: path of every plugin,
	// which is ../pkg/proto relative to the config. Generating with a
	// nested base therefore lands the tree at <base>/pkg/proto.
	if err := runProtoCommand(ctx, filepath.Join(root, protoSchemaDir),
		"buf", "generate", "-o", filepath.Join(base, "work")); err != nil {
		return nil, err
	}
	genDir := filepath.Join(base, protoStubDir)
	if err := prependSPDXHeaders(genDir); err != nil {
		return nil, fmt.Errorf("apply SPDX headers: %w", err)
	}
	if err := runProtoCommand(ctx, root,
		"goimports", "-w", "-local", protoLocalPrefix, genDir); err != nil {
		return nil, err
	}
	return collectGoTree(genDir)
}

// runProtoCommand runs one step of the producer in dir and folds the
// step's own output into the error, which is where buf and the plugins
// report what went wrong.
func runProtoCommand(ctx context.Context, dir, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %w\n%s", name, strings.Join(args, " "), err, out)
	}
	return nil
}

// prependSPDXHeaders applies the target's header step to every
// generated file under dir whose first line lacks the identifier.
func prependSPDXHeaders(dir string) error {
	return filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read %s: %w", path, err)
		}
		if firstLineCarriesSPDX(string(body)) {
			return nil
		}
		if err := os.WriteFile(path, []byte(protoSPDXHeader+string(body)), 0o644); err != nil {
			return fmt.Errorf("write %s: %w", path, err)
		}
		return nil
	})
}

// firstLineCarriesSPDX mirrors the target's `head -1 | grep -q` test.
func firstLineCarriesSPDX(body string) bool {
	first, _, _ := strings.Cut(body, "\n")
	return strings.Contains(first, protoSPDXMarker)
}

// collectGoTree reads every .go file under root, keyed by its path
// relative to root and with forward slashes so the two trees compare on
// the same keys.
func collectGoTree(root string) (map[string]string, error) {
	files := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return fmt.Errorf("relativize %s: %w", path, err)
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read %s: %w", path, err)
		}
		files[filepath.ToSlash(rel)] = string(body)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk %s: %w", root, err)
	}
	return files, nil
}

// compareProtoTrees reports the drift between the committed stubs and a
// reproduced producer run, naming every file that differs, that the run
// did not produce, or that the run produced and the tree does not
// carry.
//
// A run that produced no files at all is reported as a failure rather
// than as an absence of drift: a generator that emitted nothing
// certifies nothing about the committed stubs.
func compareProtoTrees(committed, generated map[string]string) error {
	if len(generated) == 0 {
		return fmt.Errorf("the reproduced producer generated zero files, so the %d committed file(s) under %s are unverified", len(committed), protoStubDir)
	}
	var problems []string
	for path, want := range generated {
		got, ok := committed[path]
		switch {
		case !ok:
			problems = append(problems, fmt.Sprintf("%s: generated but not committed", path))
		case got != want:
			problems = append(problems, fmt.Sprintf("%s: committed content differs from the generated content", path))
		}
	}
	for path := range committed {
		if _, ok := generated[path]; !ok {
			problems = append(problems, fmt.Sprintf("%s: committed but not generated", path))
		}
	}
	if len(problems) == 0 {
		return nil
	}
	sort.Strings(problems)
	return fmt.Errorf("%s drifted from schemas/:\n  %s", protoStubDir, strings.Join(problems, "\n  "))
}

func TestProtoDriftComparisonAcceptsReproducedRun(t *testing.T) {
	tree := map[string]string{
		"adapter/v1/lenny-adapter.pb.go": "// SPDX-License-Identifier: MIT\n\npackage adapterv1\n",
	}
	same := map[string]string{
		"adapter/v1/lenny-adapter.pb.go": "// SPDX-License-Identifier: MIT\n\npackage adapterv1\n",
	}
	if err := compareProtoTrees(tree, same); err != nil {
		t.Fatalf("a committed tree matching the reproduced run must pass: %v", err)
	}
}

func TestProtoDriftComparisonNamesHandEditedStub(t *testing.T) {
	generated := map[string]string{
		"adapter/v1/lenny-adapter.pb.go":      "package adapterv1\n",
		"interceptor/v1/lenny-interceptor.go": "package interceptorv1\n",
	}
	committed := map[string]string{
		"adapter/v1/lenny-adapter.pb.go":      "package adapterv1\n// hand-edited comment\n",
		"interceptor/v1/lenny-interceptor.go": "package interceptorv1\n",
	}
	err := compareProtoTrees(committed, generated)
	if err == nil {
		t.Fatal("a hand-edited stub must fail the comparison")
	}
	if !strings.Contains(err.Error(), "adapter/v1/lenny-adapter.pb.go") {
		t.Fatalf("the failure must name the edited file, got: %v", err)
	}
	if strings.Contains(err.Error(), "lenny-interceptor.go") {
		t.Fatalf("the failure must not name an unedited file, got: %v", err)
	}
}

func TestProtoDriftComparisonReportsMissingAndExtraFiles(t *testing.T) {
	err := compareProtoTrees(
		map[string]string{"a.pb.go": "package a\n", "stale.pb.go": "package stale\n"},
		map[string]string{"a.pb.go": "package a\n", "fresh.pb.go": "package fresh\n"},
	)
	if err == nil {
		t.Fatal("a committed set differing from the generated set must fail")
	}
	for _, want := range []string{"stale.pb.go", "fresh.pb.go"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("the failure must name %s, got: %v", want, err)
		}
	}
}

func TestProtoDriftComparisonFailsOnZeroGeneratedFiles(t *testing.T) {
	err := compareProtoTrees(map[string]string{"a.pb.go": "package a\n"}, map[string]string{})
	if err == nil {
		t.Fatal("a run that generated no files must fail rather than report no drift")
	}
	if !strings.Contains(err.Error(), "zero files") {
		t.Fatalf("the failure must state that the run generated nothing, got: %v", err)
	}
}

func TestProtoProducerReportsUnverifiedWhenABinaryIsUnavailable(t *testing.T) {
	absent := map[string]bool{"protoc-gen-go-grpc": true, "goimports": true}
	missing := missingProducerBinaries(protoProducerBinaries, func(name string) error {
		if absent[name] {
			return fmt.Errorf("%s: not found", name)
		}
		return nil
	})
	if want := []string{"protoc-gen-go-grpc", "goimports"}; !equalStrings(missing, want) {
		t.Fatalf("missing binaries = %v, want %v", missing, want)
	}

	line := unverifiedLine(protoUnverifiedReason(missing))
	reasons, ok := verdictstatus.ScanUnverified(line)
	if !ok {
		t.Fatalf("the sentinel must be readable by the tier-0 status producer, got %q", line)
	}
	if len(reasons) != 1 || !strings.Contains(reasons[0], "protoc-gen-go-grpc") {
		t.Fatalf("the reported reason must name the unavailable binaries, got %v", reasons)
	}
}

func TestProtoProducerBinaryLookupReportsAllPresent(t *testing.T) {
	missing := missingProducerBinaries(protoProducerBinaries, func(string) error { return nil })
	if len(missing) != 0 {
		t.Fatalf("a host carrying every producer binary reports none missing, got %v", missing)
	}
}

func TestProtoGeneratedHeaderStepMatchesTheTarget(t *testing.T) {
	dir := t.TempDir()
	bare := filepath.Join(dir, "bare.go")
	headed := filepath.Join(dir, "headed.go")
	if err := os.WriteFile(bare, []byte("package p\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.WriteFile(headed, []byte(protoSPDXHeader+"package p\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := prependSPDXHeaders(dir); err != nil {
		t.Fatalf("prepend headers: %v", err)
	}
	got, err := os.ReadFile(bare)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != protoSPDXHeader+"package p\n" {
		t.Fatalf("a file without the header gains it, got %q", got)
	}
	got, err = os.ReadFile(headed)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != protoSPDXHeader+"package p\n" {
		t.Fatalf("a file already carrying the header is left alone, got %q", got)
	}
}

// equalStrings compares two string slices element by element.
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
