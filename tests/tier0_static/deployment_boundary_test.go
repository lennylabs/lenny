// SPDX-License-Identifier: MIT

package tier0_static

import (
	"context"
	"fmt"
	"go/ast"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"

	corev1 "k8s.io/api/core/v1"

	"github.com/lennylabs/lenny/pkg/controller/sandbox/podspec"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// The deployment-boundary gate holds the agent pod's two halves together: the
// inputs the pod's binaries read, and the inputs the pod builder renders. The
// adapter's render is controller Go code rather than a chart template, so no
// behavioral tier sees an input the builder never sets. Such an input holds
// its compiled default in every deployed pod, and the behavior it configures
// is off or untunable without any test failing.
//
// The gate measures both halves from the source and compares them:
//
//   - I1 and I2 are a bijection. Every flag the adapter declares and every
//     standalone variable it reads is rendered by some render case or carried
//     by exactly one register row, and every register row names an input that
//     is declared and unrendered.
//   - I3 holds each default row to the source: the recorded default equals the
//     compiled default, a mount-path default equals a rendered mount, and a
//     superseded variable's replacement flag renders.
//   - I4 and I5 are the reverse direction. Every rendered argv parses against
//     the binary that runs at its site, and every rendered variable is read by
//     that binary. A rendered flag a binary does not declare exits the
//     container with status 2 before it serves anything.
//   - I6 and I7 tie a render case to production: every podspec.Inputs field is
//     set by some case, and by the production composite literal that builds
//     the pod.
//   - I8 makes the scan fail closed: a declaration or read it cannot resolve
//     to a literal name is a defect.
//
// The two registers are tests/registers/exceptions-deployment-boundary.yaml,
// whose rows are gaps a remediation step will render, and
// tests/registers/deployment-boundary-defaults.yaml, whose rows are inputs
// whose compiled default is correct in a deployed pod.

// boundaryMeasurement is the I/O the gate tests share: the scanned inputs of
// each binary, the built render cases and their attribution, and the two
// registers.
type boundaryMeasurement struct {
	fset          *token.FileSet
	files         map[string][]parsedFile // by binary name
	inputs        map[string]binaryInputs // by binary name
	scanDefects   []string
	producerFiles []parsedFile
	cases         []builtCase
	argv          []argvUse
	env           []envUse
	unmapped      []string
	rendered      renderedSet
	exceptions    []string
	defaults      []boundaryDefaultRow
}

var (
	boundaryOnce   sync.Once
	boundaryResult *boundaryMeasurement
	boundaryErr    error
)

// measureBoundary runs the shared measurement once. The I/O returns errors
// rather than taking t, so every caller fails on a stored error before it
// reads the result.
func measureBoundary(t *testing.T) *boundaryMeasurement {
	t.Helper()
	boundaryOnce.Do(func() { boundaryResult, boundaryErr = measureBoundaryOnce(context.Background()) })
	if boundaryErr != nil {
		t.Fatalf("deployment-boundary measurement: %v", boundaryErr)
	}
	return boundaryResult
}

func measureBoundaryOnce(ctx context.Context) (*boundaryMeasurement, error) {
	root, err := schematest.RepoRootCwd()
	if err != nil {
		return nil, err
	}
	m := &boundaryMeasurement{
		fset:   token.NewFileSet(),
		files:  map[string][]parsedFile{},
		inputs: map[string]binaryInputs{},
	}
	cache := map[string]*ast.File{}
	var all []parsedFile
	for _, b := range boundaryBinaries {
		srcs, err := listClosure(ctx, root, b.Dir)
		if err != nil {
			return nil, err
		}
		if m.files[b.Name], err = parseSourceFiles(m.fset, srcs, cache); err != nil {
			return nil, err
		}
		all = append(all, m.files[b.Name]...)
	}
	consts := collectStringConsts(all)
	seen := map[string]bool{}
	for _, b := range boundaryBinaries {
		in, defects := scanInputs(m.fset, m.files[b.Name], consts)
		m.inputs[b.Name] = in
		for _, d := range defects {
			if !seen[d] {
				seen[d] = true
				m.scanDefects = append(m.scanDefects, d)
			}
		}
	}
	sort.Strings(m.scanDefects)
	producerSrcs, err := listClosure(ctx, root, boundaryProducerDir)
	if err != nil {
		return nil, err
	}
	if m.producerFiles, err = parseSourceFiles(m.fset, producerSrcs, cache); err != nil {
		return nil, err
	}
	if m.cases, err = buildCases(renderCases()); err != nil {
		return nil, err
	}
	m.argv, m.env, m.unmapped = attributeCases(m.cases)
	m.rendered = renderedInputs(m.argv, m.env, m.inputs)
	if m.exceptions, m.defaults, err = readBoundaryRegisters(root); err != nil {
		return nil, err
	}
	return m, nil
}

// readBoundaryRegisters loads both register files.
func readBoundaryRegisters(root string) ([]string, []boundaryDefaultRow, error) {
	excBody, err := os.ReadFile(filepath.Join(root, boundaryExceptionsPath))
	if err != nil {
		return nil, nil, fmt.Errorf("read %s: %w", boundaryExceptionsPath, err)
	}
	exc, err := parseExceptionSubjects(excBody)
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w", boundaryExceptionsPath, err)
	}
	defBody, err := os.ReadFile(filepath.Join(root, boundaryDefaultsPath))
	if err != nil {
		return nil, nil, fmt.Errorf("read %s: %w", boundaryDefaultsPath, err)
	}
	defs, err := parseDefaultRows(defBody)
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w", boundaryDefaultsPath, err)
	}
	return exc, defs, nil
}

// boundaryBinaryNamed returns the table row for a binary.
func boundaryBinaryNamed(t *testing.T, name string) boundaryBinary {
	t.Helper()
	for _, b := range boundaryBinaries {
		if b.Name == name {
			return b
		}
	}
	t.Fatalf("no boundary binary named %s", name)
	return boundaryBinary{}
}

// registeredSubjects is the union of both registers' subjects.
func registeredSubjects(exc []string, defs []boundaryDefaultRow) map[string]bool {
	out := map[string]bool{}
	for _, s := range exc {
		out[s] = true
	}
	for _, r := range defs {
		out[r.Subject] = true
	}
	return out
}

// inputSubjects lists a binary's subjects: every declared flag and every
// standalone read. A bound read takes its disposition from its flag.
func inputSubjects(b boundaryBinary, in binaryInputs) []string {
	var out []string
	seen := map[string]bool{}
	add := func(s string) {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	for _, f := range in.Flags {
		add(b.Name + " " + f.key())
	}
	for _, r := range in.Env {
		if r.BoundTo == "" {
			add(envSubject(b.Name, r.Name))
		}
	}
	return out
}

// unregisteredInputs is I1: a subject that no case renders and no register
// row carries.
func unregisteredInputs(b boundaryBinary, in binaryInputs, rendered renderedSet, registered map[string]bool) []string {
	var defects []string
	for _, s := range inputSubjects(b, in) {
		if len(rendered[s]) == 0 && !registered[s] {
			defects = append(defects, fmt.Sprintf("unregistered input %q: render it or add a register row", s))
		}
	}
	return defects
}

// staleRows is I2 for one forward binary: a row whose input is rendered or no
// longer read, and a subject both files carry. Rows naming another binary are
// left to that binary's call.
func staleRows(b boundaryBinary, in binaryInputs, rendered renderedSet, exc []string, defs []boundaryDefaultRow) []string {
	declared := map[string]bool{}
	for _, s := range inputSubjects(b, in) {
		declared[s] = true
	}
	boundTo := map[string]string{}
	for _, r := range in.Env {
		if r.BoundTo != "" {
			boundTo[envSubject(b.Name, r.Name)] = b.Name + " " + r.BoundTo
		}
	}
	inDefaults := map[string]bool{}
	for _, r := range defs {
		inDefaults[r.Subject] = true
	}
	var defects []string
	check := func(subject, label string) {
		if !strings.HasPrefix(subject, b.Name+" ") {
			return
		}
		switch {
		case boundTo[subject] != "":
			defects = append(defects, fmt.Sprintf("%s %q: names a read bound to the default of %s; register the flag", label, subject, boundTo[subject]))
		case !declared[subject]:
			defects = append(defects, fmt.Sprintf("%s %q: no binary reads it any longer; delete the row in this change", label, subject))
		case len(rendered[subject]) > 0:
			defects = append(defects, fmt.Sprintf("%s %q: the input is now rendered; delete the row in this change", label, subject))
		}
	}
	for _, s := range exc {
		if inDefaults[s] {
			defects = append(defects, fmt.Sprintf("row %q appears in both register files; keep it in exactly one", s))
		}
		check(s, "stale row")
	}
	for _, r := range defs {
		check(r.Subject, "stale default row")
	}
	return defects
}

// rowsNamingNoForwardBinary reports register rows whose subject names no
// binary the gate checks in the forward direction.
func rowsNamingNoForwardBinary(exc []string, defs []boundaryDefaultRow) []string {
	subjects := append([]string(nil), exc...)
	for _, r := range defs {
		subjects = append(subjects, r.Subject)
	}
	var defects []string
	for _, s := range subjects {
		known := false
		for _, b := range boundaryBinaries {
			known = known || (b.Forward && strings.HasPrefix(s, b.Name+" "))
		}
		if !known {
			defects = append(defects, fmt.Sprintf("row %q names no binary the gate checks in the forward direction", s))
		}
	}
	return defects
}

// defaultRowDefects is I3. It reports at most one defect per row: the
// compiled default first, then the mount-path or superseded clause across the
// sidecar cases in table order.
func defaultRowDefects(b boundaryBinary, in binaryInputs, defs []boundaryDefaultRow, cases []builtCase, rendered renderedSet) []string {
	decls := map[string]declaredFlag{}
	for _, f := range in.Flags {
		decls[b.Name+" "+f.key()] = f
	}
	var defects []string
	for _, row := range defs {
		if !strings.HasPrefix(row.Subject, b.Name+" ") {
			continue
		}
		if d := compiledDefaultDefect(row, decls); d != "" {
			defects = append(defects, d)
			continue
		}
		if d := sidecarClauseDefect(row, cases, rendered); d != "" {
			defects = append(defects, d)
		}
	}
	return defects
}

// compiledDefaultDefect compares a row's recorded default with the source.
// A row whose subject is not declared is I2's to report.
func compiledDefaultDefect(row boundaryDefaultRow, decls map[string]declaredFlag) string {
	if isEnvSubject(row.Subject) {
		if row.Default != boundaryUnsetDefault {
			return fmt.Sprintf("default row %q: an env row records %q, want %q", row.Subject, row.Default, boundaryUnsetDefault)
		}
		return ""
	}
	d, ok := decls[row.Subject]
	switch {
	case !ok:
		return ""
	case d.Default == opaqueDefault:
		return fmt.Sprintf("default row %q: the flag is declared with %s, which has no comparable default", row.Subject, d.Method)
	case d.Default != row.Default:
		return fmt.Sprintf("default row %q: declaration default is %q, row records %q", row.Subject, d.Default, row.Default)
	}
	return ""
}

// sidecarClauseDefect checks a mount-path or superseded row in every sidecar
// case.
func sidecarClauseDefect(row boundaryDefaultRow, cases []builtCase, rendered renderedSet) string {
	for _, c := range cases {
		if !c.Sidecar {
			continue
		}
		switch row.Basis {
		case "mount-path":
			if !adapterMountsPath(c.Pod, row.Default) {
				return fmt.Sprintf("default row %q: case %s mounts nothing at %q on the adapter container", row.Subject, c.Name, row.Default)
			}
		case "superseded":
			if !rendered[row.SupersededBy][c.Name] {
				return fmt.Sprintf("default row %q: case %s does not render %s, which supersedes it", row.Subject, c.Name, row.SupersededBy)
			}
		}
	}
	return ""
}

// adapterMountsPath reports whether the pod's adapter container mounts a
// volume at p.
func adapterMountsPath(pod *corev1.Pod, p string) bool {
	for _, ctr := range pod.Spec.Containers {
		if ctr.Name != "adapter" {
			continue
		}
		for _, vm := range ctr.VolumeMounts {
			if vm.MountPath == p {
				return true
			}
		}
	}
	return false
}

// reverseArgvDefects is I4 over the parsed argv.
func reverseArgvDefects(uses []argvUse, inputs map[string]binaryInputs) []string {
	var defects []string
	for _, u := range uses {
		in, ok := inputs[u.Binary]
		if !ok {
			defects = append(defects, fmt.Sprintf("%s %s: the gate scanned no binary named %s", u.Case, u.Site, u.Binary))
			continue
		}
		_, d := parseArgv(in.Flags, u)
		defects = append(defects, d...)
	}
	return defects
}

// reverseEnvDefects is I5.
func reverseEnvDefects(uses []envUse, inputs map[string]binaryInputs) []string {
	var defects []string
	for _, u := range uses {
		if u.Binary == runtimeSDKBinary {
			if u.Name != sdkSocketVariable {
				defects = append(defects, fmt.Sprintf("%s %s env %q: the sidecar runtime container carries only %s",
					u.Case, u.Site, u.Name, sdkSocketVariable))
			}
			continue
		}
		read := false
		for _, r := range inputs[u.Binary].Env {
			read = read || r.Name == u.Name
		}
		if !read {
			defects = append(defects, fmt.Sprintf("%s %s env %q: no read in the %s closure", u.Case, u.Site, u.Name, u.Binary))
		}
	}
	return defects
}

// inputsFieldNames lists the exported podspec.Inputs fields in declaration
// order.
func inputsFieldNames() []string {
	typ := reflect.TypeOf(podspec.Inputs{})
	var out []string
	for i := 0; i < typ.NumField(); i++ {
		if f := typ.Field(i); f.IsExported() {
			out = append(out, f.Name)
		}
	}
	return out
}

// uncoveredInputsFields is I6: a field zero in every case.
func uncoveredInputsFields(cases []renderCase) []string {
	var defects []string
	for _, name := range inputsFieldNames() {
		covered := false
		for _, c := range cases {
			covered = covered || !reflect.ValueOf(c.In).FieldByName(name).IsZero()
		}
		if !covered {
			defects = append(defects, fmt.Sprintf("podspec.Inputs.%s is zero in every render case; add a case that sets it", name))
		}
	}
	return defects
}

// renderedFor recomputes the rendered set over perturbed cases.
func renderedFor(cases []builtCase, inputs map[string]binaryInputs) renderedSet {
	argv, env, _ := attributeCases(cases)
	return renderedInputs(argv, env, inputs)
}

// perturbedCases deep-copies the measured cases and applies edit to the pod of
// the named case.
func perturbedCases(t *testing.T, m *boundaryMeasurement, caseName string, edit func(pod *corev1.Pod)) []builtCase {
	t.Helper()
	cases := copyCases(m.cases)
	for _, c := range cases {
		if c.Name == caseName {
			edit(c.Pod)
			return cases
		}
	}
	t.Fatalf("no render case named %s", caseName)
	return nil
}

// boundaryContainer returns the named container of a pod.
func boundaryContainer(t *testing.T, pod *corev1.Pod, name string) *corev1.Container {
	t.Helper()
	for i := range pod.Spec.Containers {
		if pod.Spec.Containers[i].Name == name {
			return &pod.Spec.Containers[i]
		}
	}
	t.Fatalf("pod %s carries no container named %s", pod.Name, name)
	return nil
}

// assertDefects fails unless got holds exactly one entry per want substring,
// matched in order after sorting.
func assertDefects(t *testing.T, label string, got []string, want ...string) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("%s: got %d defects, want %d:\n  %s", label, len(got), len(want), strings.Join(got, "\n  "))
		return
	}
	sorted := append([]string(nil), got...)
	sort.Strings(sorted)
	for i, w := range want {
		if !strings.Contains(sorted[i], w) {
			t.Errorf("%s: defect %q does not contain %q", label, sorted[i], w)
		}
	}
}

// spec: 4.7.10 (Deployment Model), 28.5.3 (Intra-pod), 10.3 (mTLS PKI), 4.7.6 (Adapter Manifest Field Reference)
// diagnosis: the adapter binary reads a flag or an environment variable that
// no rendered agent pod sets and that no row in either deployment-boundary
// register accounts for. In a deployed pod the input holds its compiled
// default, so the behavior it configures is either off or untunable. Render
// the input in the pod builder, or add a row: an exception row blocked on
// the step that renders it, or a default row stating why the default stands.
func TestDeploymentBoundaryEveryAdapterInputIsRenderedOrRegistered(t *testing.T) {
	t.Parallel()
	m := measureBoundary(t)
	for _, d := range m.scanDefects {
		t.Errorf("unresolvable declaration or read: %s", d)
	}
	adapter := m.inputs["lenny-adapter"]
	if len(adapter.Flags) == 0 {
		t.Fatalf("the scan found no declared adapter flag; the gate would pass vacuously")
	}
	registered := registeredSubjects(m.exceptions, m.defaults)
	for _, b := range boundaryBinaries {
		if !b.Forward {
			continue
		}
		for _, d := range unregisteredInputs(b, m.inputs[b.Name], m.rendered, registered) {
			t.Error(d)
		}
	}
	envNames := map[string]bool{}
	for _, r := range adapter.Env {
		envNames[r.Name] = true
	}
	t.Logf("deployment boundary: %d gap rows, %d default rows, %d declared flags, %d env reads",
		len(m.exceptions), len(m.defaults), len(adapter.Flags), len(envNames))
}

// spec: 4.7.10 (Deployment Model)
// diagnosis: a deployment-boundary register row names an input that is now
// rendered, that no binary reads any longer, or that both register files
// carry. The change that rendered or removed the input left its row behind;
// delete the row in that change so the register stays an exact debt count.
func TestDeploymentBoundaryEveryRegisterRowNamesAnUnrenderedInput(t *testing.T) {
	t.Parallel()
	m := measureBoundary(t)
	for _, d := range rowsNamingNoForwardBinary(m.exceptions, m.defaults) {
		t.Error(d)
	}
	for _, b := range boundaryBinaries {
		if !b.Forward {
			continue
		}
		for _, d := range staleRows(b, m.inputs[b.Name], m.rendered, m.exceptions, m.defaults) {
			t.Error(d)
		}
	}
}

// spec: 4.7.10 (Deployment Model), 6.4 (Pod Filesystem Layout), 11.3 (Timeouts and Cancellation), 28.5.3 (Intra-pod), 6.1 (What a Pre-Warmed Pod Looks Like)
// diagnosis: a default row no longer describes the binary. A flag's compiled
// default changed, a mount path the default must equal moved, or the flag
// that supersedes a variable stopped rendering. Review the row: the reason
// it gave for accepting the default may no longer hold.
func TestDeploymentBoundaryDefaultRowsMatchTheCompiledDefaults(t *testing.T) {
	t.Parallel()
	m := measureBoundary(t)
	for _, b := range boundaryBinaries {
		if !b.Forward {
			continue
		}
		for _, d := range defaultRowDefects(b, m.inputs[b.Name], m.defaults, m.cases, m.rendered) {
			t.Error(d)
		}
	}
}

// spec: 4.7.10 (Deployment Model), 4.6.1 (Warm Pool Controller (Pod Lifecycle))
// diagnosis: the pod builder renders an argument the binary in that container
// does not accept, renders a flag twice, or renders argv at a site the gate
// does not map. A container started from that spec exits with status 2 from
// Go's flag package before it serves anything, and every wait on the pod runs
// to its timeout. Declare the flag in the binary, fix the render, or map the
// new site in the render-site table.
func TestDeploymentBoundaryEveryRenderedArgvParsesAgainstItsBinary(t *testing.T) {
	t.Parallel()
	m := measureBoundary(t)
	if len(m.argv) == 0 {
		t.Fatalf("the render cases carry no argv; the gate would pass vacuously")
	}
	for _, d := range m.unmapped {
		t.Error(d)
	}
	for _, d := range reverseArgvDefects(m.argv, m.inputs) {
		t.Error(d)
	}
}

// spec: 4.7.10 (Deployment Model)
// diagnosis: the pod builder sets an environment variable that the binary in
// that container never reads, so the configuration it carries is lost. The
// variable's name drifted from the reader's constant, or the reader was
// removed.
func TestDeploymentBoundaryEveryRenderedVariableIsReadByItsContainer(t *testing.T) {
	t.Parallel()
	m := measureBoundary(t)
	if len(m.env) == 0 {
		t.Fatalf("the render cases carry no environment; the gate would pass vacuously")
	}
	for _, d := range reverseEnvDefects(m.env, m.inputs) {
		t.Error(d)
	}
}

// spec: 4.7.10 (Deployment Model)
// diagnosis: a podspec.Inputs field is zero in every render case, so a flag
// the builder renders only when that field is set is invisible to the gate
// and would count as unrendered or be missed by the reverse check. Add the
// field to a render case.
func TestDeploymentBoundaryRenderCasesSetEveryInputsField(t *testing.T) {
	t.Parallel()
	for _, d := range uncoveredInputsFields(renderCases()) {
		t.Error(d)
	}
}

// spec: 4.7.10 (Deployment Model), 4.6.1 (Warm Pool Controller (Pod Lifecycle))
// diagnosis: a podspec.Inputs field is set by no production composite
// literal, so a flag the builder renders from it appears rendered in the
// gate's cases and never reaches a deployed pod. Set the field where the
// sandbox reconciler builds the pod.
func TestDeploymentBoundaryEveryInputsFieldHasAProductionProducer(t *testing.T) {
	t.Parallel()
	m := measureBoundary(t)
	for _, d := range scanInputsProducers(m.producerFiles, inputsFieldNames()) {
		t.Error(d)
	}
}

// adapterPkg is the adapter's import path, under which the negative tests
// scan inline files as part of the adapter binary.
const adapterPkg = boundaryModulePrefix + "cmd/lenny-adapter"

// rescanAdapterWith scans the adapter closure plus inline files.
func rescanAdapterWith(t *testing.T, m *boundaryMeasurement, extra ...sourceFile) binaryInputs {
	t.Helper()
	parsed, err := parseSourceFiles(m.fset, extra, nil)
	if err != nil {
		t.Fatalf("parse inline source: %v", err)
	}
	files := append(append([]parsedFile(nil), m.files["lenny-adapter"]...), parsed...)
	in, defects := scanInputs(m.fset, files, collectStringConsts(files))
	if len(defects) > 0 {
		t.Fatalf("inline source does not scan cleanly:\n  %s", strings.Join(defects, "\n  "))
	}
	return in
}

// spec: 4.7.10 (Deployment Model)
// diagnosis: the gate no longer detects an adapter flag or variable added
// without a render or a register row, so an input that holds its compiled
// default in every deployed pod passes tier 0 green.
func TestDeploymentBoundaryGateReportsAFlagAddedWithoutARender(t *testing.T) {
	t.Parallel()
	m := measureBoundary(t)
	b := boundaryBinaryNamed(t, "lenny-adapter")
	probe := sourceFile{Pkg: adapterPkg, Name: "cmd/lenny-adapter/zz_boundary_probe.go", Src: []byte(`package main

import (
	"flag"
	"os"
	"strconv"
)

func probeEnvIntOr(name string, def int) int {
	v := os.Getenv(name)
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

func probe() {
	_ = flag.String("probe-unrendered", "", "")
	_ = os.Getenv("LENNY_PROBE_STANDALONE")
	_ = flag.Int("probe-bound", probeEnvIntOr("LENNY_PROBE_BOUND", 1), "")
}
`)}
	in := rescanAdapterWith(t, m, probe)
	registered := registeredSubjects(m.exceptions, m.defaults)
	got := unregisteredInputs(b, in, m.rendered, registered)
	want := []string{
		`unregistered input "lenny-adapter --probe-unrendered": render it or add a register row`,
		`unregistered input "lenny-adapter env LENNY_PROBE_STANDALONE": render it or add a register row`,
		`unregistered input "lenny-adapter --probe-bound": render it or add a register row`,
	}
	sort.Strings(got)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("unregistered inputs:\n got %q\nwant %q", got, want)
	}
	for _, d := range got {
		if strings.Contains(d, "LENNY_PROBE_BOUND") {
			t.Errorf("a read bound to a flag default was reported as its own subject: %s", d)
		}
	}

	// A row for each subject clears the violation.
	exc := append(append([]string(nil), m.exceptions...),
		"lenny-adapter --probe-unrendered", "lenny-adapter env LENNY_PROBE_STANDALONE", "lenny-adapter --probe-bound")
	if got := unregisteredInputs(b, in, m.rendered, registeredSubjects(exc, m.defaults)); len(got) != 0 {
		t.Errorf("rows for the probe subjects left defects: %q", got)
	}

	// A package-level declaration is a declaration.
	pkgLevel := sourceFile{Pkg: adapterPkg, Name: "cmd/lenny-adapter/zz_boundary_pkglevel.go", Src: []byte(`package main

import "flag"

var probe = flag.String("probe-pkglevel", "", "")
`)}
	in = rescanAdapterWith(t, m, pkgLevel)
	assertDefects(t, "package-level declaration", unregisteredInputs(b, in, m.rendered, registered),
		`unregistered input "lenny-adapter --probe-pkglevel"`)
}

// spec: 4.7.10 (Deployment Model)
// diagnosis: the gate no longer detects a register row whose input is
// rendered, no longer read, or carried by both files, so a stale row that
// overstates the remaining debt passes tier 0 green.
func TestDeploymentBoundaryGateReportsAStaleRow(t *testing.T) {
	t.Parallel()
	m := measureBoundary(t)
	b := boundaryBinaryNamed(t, "lenny-adapter")
	in := m.inputs[b.Name]

	assertDefects(t, "(a) rendered exception row",
		staleRows(b, in, m.rendered, []string{"lenny-adapter --runtime-socket"}, m.defaults),
		`stale row "lenny-adapter --runtime-socket": the input is now rendered`)
	assertDefects(t, "(b) undeclared exception row",
		staleRows(b, in, m.rendered, []string{"lenny-adapter --probe-retired"}, m.defaults),
		`stale row "lenny-adapter --probe-retired": no binary reads it any longer`)
	assertDefects(t, "(c) subject in both files",
		staleRows(b, in, m.rendered, []string{"lenny-adapter --sessions-root"}, m.defaults),
		"appears in both register files")
	addr := boundaryDefaultRow{Subject: "lenny-adapter --addr", Default: ":50051", Basis: "spec-value", Reason: "probe"}
	assertDefects(t, "(d) rendered default row",
		staleRows(b, in, m.rendered, nil, append(append([]boundaryDefaultRow(nil), m.defaults...), addr)),
		`stale default row "lenny-adapter --addr": the input is now rendered`)

	// (e) A bound variable rendered on the adapter renders its flag.
	const ackFlag = "lenny-adapter --heartbeat-ack-timeout-seconds"
	var defsWithoutAck []boundaryDefaultRow
	for _, r := range m.defaults {
		if r.Subject != ackFlag {
			defsWithoutAck = append(defsWithoutAck, r)
		}
	}
	ackCases := perturbedCases(t, m, "sidecar-maximal", func(pod *corev1.Pod) {
		ctr := boundaryContainer(t, pod, "adapter")
		ctr.Env = append(ctr.Env, corev1.EnvVar{Name: "LENNY_ADAPTER_HEARTBEAT_ACK_TIMEOUT_SECONDS", Value: "10"})
	})
	assertDefects(t, "(e) exception row rendered through its bound variable",
		staleRows(b, in, renderedFor(ackCases, m.inputs), []string{ackFlag}, defsWithoutAck),
		`stale row "`+ackFlag+`": the input is now rendered`)

	// (f) The same for a default row.
	keepaliveCases := perturbedCases(t, m, "sidecar-maximal", func(pod *corev1.Pod) {
		ctr := boundaryContainer(t, pod, "adapter")
		ctr.Env = append(ctr.Env, corev1.EnvVar{Name: "LENNY_ADAPTER_KEEPALIVE_TIME_MS", Value: "20000"})
	})
	assertDefects(t, "(f) default row rendered through its bound variable",
		staleRows(b, in, renderedFor(keepaliveCases, m.inputs), nil, m.defaults),
		`stale default row "lenny-adapter --keepalive-time-ms": the input is now rendered`)
}

// spec: 4.7.10 (Deployment Model)
// diagnosis: the gate no longer detects a default row that has drifted from
// the source, so a row whose reason no longer holds passes tier 0 green.
func TestDeploymentBoundaryGateReportsAChangedDefault(t *testing.T) {
	t.Parallel()
	m := measureBoundary(t)
	b := boundaryBinaryNamed(t, "lenny-adapter")
	in := m.inputs[b.Name]
	withRow := func(subject string, edit func(*boundaryDefaultRow)) []boundaryDefaultRow {
		out := append([]boundaryDefaultRow(nil), m.defaults...)
		for i := range out {
			if out[i].Subject == subject {
				edit(&out[i])
				return out
			}
		}
		t.Fatalf("no default row for %s", subject)
		return nil
	}

	assertDefects(t, "(a) recorded default differs",
		defaultRowDefects(b, in, withRow("lenny-adapter --sessions-root", func(r *boundaryDefaultRow) { r.Default = "/elsewhere" }), m.cases, m.rendered),
		`default row "lenny-adapter --sessions-root"`)

	moved := perturbedCases(t, m, "sidecar-minimal", func(pod *corev1.Pod) {
		ctr := boundaryContainer(t, pod, "adapter")
		for i := range ctr.VolumeMounts {
			if ctr.VolumeMounts[i].MountPath == "/sessions" {
				ctr.VolumeMounts[i].MountPath = "/moved"
			}
		}
	})
	assertDefects(t, "(b) mount moved",
		defaultRowDefects(b, in, m.defaults, moved, m.rendered),
		`default row "lenny-adapter --sessions-root": case sidecar-minimal`)

	noUID := copyCases(m.cases)
	for _, c := range noUID {
		if !c.Sidecar {
			continue
		}
		ctr := boundaryContainer(t, c.Pod, "adapter")
		var args []string
		for _, a := range ctr.Args {
			if !strings.HasPrefix(a, "--runtime-uid") {
				args = append(args, a)
			}
		}
		ctr.Args = args
	}
	assertDefects(t, "(c) superseding flag no longer rendered",
		defaultRowDefects(b, in, m.defaults, noUID, renderedFor(noUID, m.inputs)),
		`default row "lenny-adapter env LENNY_RUNTIME_UID"`)

	assertDefects(t, "(d) env row records a value",
		defaultRowDefects(b, in, withRow("lenny-adapter env LENNY_DEMOTE_TIMEOUT_SECONDS", func(r *boundaryDefaultRow) { r.Default = "5" }), m.cases, m.rendered),
		`default row "lenny-adapter env LENNY_DEMOTE_TIMEOUT_SECONDS"`)
}

// spec: 4.7.10 (Deployment Model)
// diagnosis: the gate no longer detects a rendered argument the binary in
// that container rejects, so a pod spec whose container exits with status 2
// passes tier 0 green.
func TestDeploymentBoundaryGateReportsAnUndeclaredRenderedFlag(t *testing.T) {
	t.Parallel()
	m := measureBoundary(t)
	argvDefects := func(cases []builtCase) []string {
		argv, _, _ := attributeCases(cases)
		return reverseArgvDefects(argv, m.inputs)
	}
	adapterArgs := func(caseName string, edit func([]string) []string) []builtCase {
		return perturbedCases(t, m, caseName, func(pod *corev1.Pod) {
			ctr := boundaryContainer(t, pod, "adapter")
			ctr.Args = edit(ctr.Args)
		})
	}
	preStop := func(edit func([]string) []string) []builtCase {
		return perturbedCases(t, m, "sidecar-maximal", func(pod *corev1.Pod) {
			h := boundaryContainer(t, pod, "adapter").Lifecycle.PreStop.Exec
			h.Command = edit(h.Command)
		})
	}

	assertDefects(t, "(a) undeclared adapter flag",
		argvDefects(adapterArgs("sidecar-maximal", func(a []string) []string { return append(a, "--probe-undeclared=x") })),
		"flag provided but not defined: -probe-undeclared")
	assertDefects(t, "(b) undeclared preStop flag",
		argvDefects(preStop(func(c []string) []string { return append(c, "--probe-undeclared=x") })),
		"flag provided but not defined: -probe-undeclared")
	assertDefects(t, "(c) duplicate flag",
		argvDefects(adapterArgs("sidecar-maximal", func(a []string) []string { return append(a, "--addr=:1") })),
		"flag --addr set 2 times")
	assertDefects(t, "(d) positional argument",
		argvDefects(adapterArgs("sidecar-maximal", func(a []string) []string { return append(a, "extra") })),
		`positional argument "extra"`)

	initCases := perturbedCases(t, m, "sidecar-maximal", func(pod *corev1.Pod) {
		pod.Spec.InitContainers = append(pod.Spec.InitContainers, corev1.Container{Name: "probe-init", Args: []string{"x"}})
	})
	_, _, unmapped := attributeCases(initCases)
	assertDefects(t, "(e) unmapped init container", unmapped, "unmapped render site: sidecar-maximal initContainers[probe-init].args")

	assertDefects(t, "(f) duration without a unit",
		argvDefects(preStop(func(c []string) []string {
			out := append([]string(nil), c...)
			for i := range out {
				if strings.HasPrefix(out[i], "--timeout=") {
					out[i] = "--timeout=110"
				}
			}
			return out
		})),
		"-timeout")

	embedded := perturbedCases(t, m, "embedded-maximal", func(pod *corev1.Pod) {
		ctr := boundaryContainer(t, pod, "runtime")
		ctr.Args = append(ctr.Args, "--runtime-uid=1")
	})
	assertDefects(t, "(g) adapter-only flag on the embedded runtime", argvDefects(embedded),
		"(echo-embedded): flag provided but not defined: -runtime-uid",
		"(preconnect-echo): flag provided but not defined: -runtime-uid")

	capture := perturbedCases(t, m, "sidecar-maximal", func(pod *corev1.Pod) {
		ctr := boundaryContainer(t, pod, podspec.EgressCaptureContainerName)
		ctr.Args = append(ctr.Args, "--runtime-uid=1")
	})
	assertDefects(t, "(h) adapter-only flag on the egress-capture sidecar", argvDefects(capture),
		"(lenny-egress-capture): flag provided but not defined: -runtime-uid")
}

// spec: 4.7.10 (Deployment Model)
// diagnosis: the gate no longer detects a rendered variable the binary in
// that container never reads, so configuration the pod silently drops passes
// tier 0 green.
func TestDeploymentBoundaryGateReportsAnUnreadRenderedVariable(t *testing.T) {
	t.Parallel()
	m := measureBoundary(t)
	envDefects := func(cases []builtCase) []string {
		_, env, _ := attributeCases(cases)
		return reverseEnvDefects(env, m.inputs)
	}

	adapterFoo := perturbedCases(t, m, "sidecar-maximal", func(pod *corev1.Pod) {
		ctr := boundaryContainer(t, pod, "adapter")
		ctr.Env = append(ctr.Env, corev1.EnvVar{Name: "FOO", Value: "bar"})
	})
	assertDefects(t, "unread adapter variable", envDefects(adapterFoo), `env "FOO": no read in the lenny-adapter closure`)

	socketX := perturbedCases(t, m, "sidecar-maximal", func(pod *corev1.Pod) {
		ctr := boundaryContainer(t, pod, "runtime")
		for i := range ctr.Env {
			if ctr.Env[i].Name == sdkSocketVariable {
				ctr.Env[i].Name = "LENNY_ADAPTER_SOCKETX"
			}
		}
	})
	assertDefects(t, "drifted runtime socket variable", envDefects(socketX), `env "LENNY_ADAPTER_SOCKETX": the sidecar runtime container carries only`)

	embeddedUID := perturbedCases(t, m, "embedded-maximal", func(pod *corev1.Pod) {
		ctr := boundaryContainer(t, pod, "runtime")
		ctr.Env = append(ctr.Env, corev1.EnvVar{Name: "LENNY_RUNTIME_UID", Value: "1"})
	})
	assertDefects(t, "adapter-only variable on the embedded runtime", envDefects(embeddedUID),
		"no read in the echo-embedded closure",
		"no read in the preconnect-echo closure")
}

// i8Marker tags the line of an inline source that the scan must report.
const i8Marker = "// I8"

// spec: 4.7.10 (Deployment Model)
// diagnosis: the gate no longer fails on a declaration or read it cannot
// resolve, so a flag or variable hidden behind an unresolvable form passes
// tier 0 green as though the binary did not read it.
func TestDeploymentBoundaryGateFailsClosedOnAnUnresolvableDeclaration(t *testing.T) {
	t.Parallel()
	m := measureBoundary(t)
	sources := map[string]string{
		"variable_flag_name": `package main
import "flag"
func probe() {
	name := "x"
	_ = flag.String(name, "", "") // I8
}`,
		"flagset_parameter": `package main
import "flag"
func probe(fs *flag.FlagSet) { // I8
	fs.String("a", "", "")
}`,
		"flagset_field": `package main
import "flag"
type probe struct {
	fs *flag.FlagSet // I8
}`,
		"package_level_flagset": `package main
import "flag"
var fs = flag.NewFlagSet("pkg", flag.ContinueOnError) // I8
`,
		"reassigned_flagset": `package main
import "flag"
func probe() {
	fs := flag.NewFlagSet("reassigned", flag.ContinueOnError)
	fs = nil // I8
	_ = fs.Parse(nil)
}`,
		"env_helper_as_value": `package main
import "os"
func probeLookup(name string) string { return os.Getenv(name) }
func probe() {
	get := probeLookup // I8
	_ = get("X")
}`,
		"duplicate_flagset_name": `package main
import "flag"
func a() {
	fs := flag.NewFlagSet("dup", flag.ContinueOnError)
	_ = fs.Parse(nil)
}
func b() {
	fs := flag.NewFlagSet("dup", flag.ContinueOnError) // I8
	_ = fs.Parse(nil)
}`,
		"computed_env_name": `package main
import "os"
func probe() {
	prefix := "P_"
	_ = os.Getenv(prefix + "X") // I8
}`,
		"expand_env": `package main
import "os"
func probe() {
	_ = os.ExpandEnv("$HOME") // I8
}`,
		"env_name_from_variable": `package main
import (
	"os"
	other "github.com/lennylabs/lenny/pkg/boundaryprobe"
)
func probe() {
	_ = os.Getenv(other.Var) // I8
}`,
		"commandline_alias": `package main
import "flag"
func probe() {
	fs := flag.CommandLine // I8
	fs.String("x", "", "")
}`,
		"declaration_function_as_value": `package main
import "flag"
func probe() {
	decl := flag.Int // I8
	decl("x", 1, "")
}`,
		"getenv_as_value": `package main
import "os"
func probe() {
	get := os.Getenv // I8
	_ = get("X")
}`,
		"bound_flagset_method_value": `package main
import "flag"
func probe() {
	fs := flag.NewFlagSet("method-value", flag.ContinueOnError)
	decl := fs.String // I8
	_ = decl("hidden", "", "")
}`,
		"env_helper_reassigned_param": `package main
import "os"
func probeLookup(name string) string {
	name = "LENNY_PFX_" + name // I8
	return os.Getenv(name)
}
func probe() {
	_ = probeLookup("X")
}`,
	}
	names := make([]string, 0, len(sources))
	for name := range sources {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		src := sources[name]
		file := "cmd/lenny-adapter/zz_" + name + ".go"
		line := 0
		for i, l := range strings.Split(src, "\n") {
			if strings.HasSuffix(l, i8Marker) {
				line = i + 1
			}
		}
		parsed, err := parseSourceFiles(m.fset, []sourceFile{{Pkg: adapterPkg, Name: file, Src: []byte(src)}}, nil)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		_, defects := scanInputs(m.fset, parsed, collectStringConsts(parsed))
		assertDefects(t, name, defects, fmt.Sprintf("%s:%d: ", file, line))
	}
}

// spec: 4.7.10 (Deployment Model)
// diagnosis: the gate no longer rejects a malformed register, so a register
// that cannot say what it exempts passes tier 0 green and exempts whatever
// the loader happens to read.
func TestDeploymentBoundaryGateRejectsAMalformedRegister(t *testing.T) {
	t.Parallel()
	exceptionBodies := map[string]string{
		"wrong kind": "kind: residual-register\nversion: 1\nentries: []\n",
		"no kind":    "version: 1\nentries: []\n",
		"empty":      "",
	}
	for name, body := range exceptionBodies {
		if _, err := parseExceptionSubjects([]byte(body)); err == nil {
			t.Errorf("exception register (%s) loaded without an error", name)
		}
	}
	if subjects, err := parseExceptionSubjects([]byte("kind: exception-register\nversion: 1\nentries:\n  - subject: lenny-adapter --x\n")); err != nil || len(subjects) != 1 {
		t.Errorf("a well-formed exception register did not load: %v, %q", err, subjects)
	}

	const header = "kind: deployment-boundary-defaults\nversion: 1\nentries:\n"
	const row = "  - subject: lenny-adapter --x\n    default: a\n    basis: spec-value\n    reason: r\n"
	defaultsBodies := map[string]string{
		"unknown key":             header + row + "    blocker: R12\n",
		"unknown basis":           header + "  - subject: lenny-adapter --x\n    default: a\n    basis: guess\n    reason: r\n",
		"empty reason":            header + "  - subject: lenny-adapter --x\n    default: a\n    basis: spec-value\n    reason: \"\"\n",
		"duplicate subject":       header + row + row,
		"misplaced superseded_by": header + row + "    superseded_by: lenny-adapter --y\n",
		"missing superseded_by":   header + "  - subject: lenny-adapter env X\n    default: unset\n    basis: superseded\n    reason: r\n",
		"env default not unset":   header + "  - subject: lenny-adapter env X\n    default: \"5\"\n    basis: spec-value\n    reason: r\n",
	}
	for name, body := range defaultsBodies {
		if _, err := parseDefaultRows([]byte(body)); err == nil {
			t.Errorf("defaults register (%s) loaded without an error", name)
		}
	}
	if rows, err := parseDefaultRows([]byte(header + row)); err != nil || len(rows) != 1 {
		t.Errorf("a well-formed defaults register did not load: %v, %v", err, rows)
	}
}

// spec: 4.7.10 (Deployment Model)
// diagnosis: the gate no longer detects an Inputs field no render case sets,
// so a flag rendered only from that field escapes both directions of the
// check and passes tier 0 green.
func TestDeploymentBoundaryGateReportsAnUncoveredInputsField(t *testing.T) {
	t.Parallel()
	var minimal []renderCase
	for _, c := range renderCases() {
		if strings.HasSuffix(c.Name, "-minimal") {
			minimal = append(minimal, c)
		}
	}
	if len(minimal) != 2 {
		t.Fatalf("expected the sidecar and embedded minimal cases, found %d", len(minimal))
	}
	var want []string
	for _, name := range inputsFieldNames() {
		set := false
		for _, c := range minimal {
			set = set || !reflect.ValueOf(c.In).FieldByName(name).IsZero()
		}
		if !set {
			want = append(want, fmt.Sprintf("podspec.Inputs.%s is zero in every render case; add a case that sets it", name))
		}
	}
	if len(want) == 0 {
		t.Fatalf("the minimal cases set every Inputs field; the perturbation proves nothing")
	}
	if got := uncoveredInputsFields(minimal); !reflect.DeepEqual(got, want) {
		t.Errorf("uncovered fields:\n got %q\nwant %q", got, want)
	}
}

// spec: 4.7.10 (Deployment Model)
// diagnosis: the gate no longer detects an Inputs field no production
// composite literal sets, so a flag rendered only under a test value passes
// tier 0 green while no deployed pod carries it.
func TestDeploymentBoundaryGateReportsAnUnproducedInputsField(t *testing.T) {
	t.Parallel()
	fset := token.NewFileSet()
	parsed, err := parseSourceFiles(fset, []sourceFile{{
		Pkg:  boundaryModulePrefix + "pkg/boundaryprobe",
		Name: "pkg/boundaryprobe/producer.go",
		Src: []byte(`package boundaryprobe

import "github.com/lennylabs/lenny/pkg/controller/sandbox/podspec"

var _ = podspec.Inputs{Name: "x"}
`),
	}}, nil)
	if err != nil {
		t.Fatalf("parse inline producer: %v", err)
	}
	got := scanInputsProducers(parsed, []string{"Name", "ObjectStoreCAConfigMap"})
	want := []string{"podspec.Inputs.ObjectStoreCAConfigMap: no production composite literal sets it"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("producer defects:\n got %q\nwant %q", got, want)
	}
}
