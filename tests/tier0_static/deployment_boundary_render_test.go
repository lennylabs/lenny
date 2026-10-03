// SPDX-License-Identifier: MIT

package tier0_static

import (
	"flag"
	"fmt"
	"io"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/lennylabs/lenny/pkg/controller/sandbox/podspec"
	"github.com/lennylabs/lenny/pkg/runtimekit"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

// This file holds the render half of the deployment-boundary gate: the named
// podspec.Inputs cases it builds pods from, the table that maps each place a
// built pod carries argv or environment to the binary that reads it, and the
// argv parse that decides what a binary accepts.

// runtimeSDKBinary is the reader the render-site table names for the sidecar
// runtime container's environment. The runtime image is a deployer's binary,
// so the gate admits only the runtime SDK's socket variable there.
const runtimeSDKBinary = "runtime SDK"

// renderCase is one named podspec.Inputs value. The union of the cases must
// set every Inputs field (I6).
type renderCase struct {
	Name string
	In   podspec.Inputs
}

// renderCases returns the gate's render cases. Each maximal case is a copy of
// its minimal case followed by one assignment per remaining field in Inputs
// declaration order, so a step that adds a field adds one line here and
// realigns no other step's lines.
func renderCases() []renderCase {
	minimal := func(model podspec.DeploymentModel) podspec.Inputs {
		return podspec.Inputs{
			Name:             "boundary",
			Namespace:        "lenny-agents",
			RuntimeImage:     "runtime@sha256:" + strings.Repeat("1", 64),
			AdapterImage:     "adapter@sha256:" + strings.Repeat("2", 64),
			IsolationProfile: string(isolation.ProfileStandard),
			DeploymentModel:  string(model),
		}
	}
	maximal := func(model podspec.DeploymentModel) podspec.Inputs {
		in := minimal(model)
		in.Labels = map[string]string{"lenny.dev/pool": "boundary"}
		in.GatewayGRPCAddr = "lenny-gateway.lenny-system.svc:8443"
		in.RuntimeClassNameOverrides = map[isolation.Profile]string{isolation.ProfileSandboxed: "gvisor-boundary"}
		// false is the value that renders --require-so-peercred=false.
		in.RequireSoPeercred = boundaryPtr(false)
		in.EgressCapture = &podspec.EgressCapture{Image: "capture@sha256:" + strings.Repeat("3", 64), Upstream: "10.0.0.1:443"}
		in.MaxTerminationGraceSeconds = boundaryPtr(int64(300))
		in.TerminationGraceSeconds = boundaryPtr(int64(200))
		in.PreConnect = true
		in.TopologySpreadConstraints = []corev1.TopologySpreadConstraint{{
			MaxSkew: 1, TopologyKey: "topology.kubernetes.io/zone", WhenUnsatisfiable: corev1.ScheduleAnyway,
		}}
		in.SATokenAudience = "lenny-gateway-boundary"
		in.ServiceAccountName = "lenny-agent"
		in.WorkspaceTier = podspec.WorkspaceTierT4
		in.SharedAssetsArg = "W10="
		in.WorkspaceSizeLimitBytes = boundaryPtr(int64(1 << 30))
		in.ObjectStoreCAConfigMap = "lenny-objectstore-ca"
		in.DedicatedDNSClusterIP = "10.96.0.53"
		in.ReleaseNamespace = "lenny-system"
		in.Resources = &corev1.ResourceRequirements{Limits: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("2")}}
		in.AdapterUID = 2001
		in.AgentUID = 2002
		in.CredReadersGID = 2003
		return in
	}
	return []renderCase{
		{Name: "sidecar-minimal", In: minimal(podspec.DeploymentSidecar)},
		{Name: "sidecar-maximal", In: maximal(podspec.DeploymentSidecar)},
		{Name: "embedded-minimal", In: minimal(podspec.DeploymentEmbedded)},
		{Name: "embedded-maximal", In: maximal(podspec.DeploymentEmbedded)},
	}
}

func boundaryPtr[T any](v T) *T { return &v }

// builtCase is one render case with the pod podspec.Build returned for it.
type builtCase struct {
	Name    string
	Sidecar bool
	Pod     *corev1.Pod
}

// buildCases builds every case. A Build error fails the gate, because a case
// the builder refuses renders nothing the gate can measure.
func buildCases(cases []renderCase) ([]builtCase, error) {
	out := make([]builtCase, 0, len(cases))
	for _, c := range cases {
		pod, err := podspec.Build(c.In)
		if err != nil {
			return nil, fmt.Errorf("render case %s: podspec.Build: %w", c.Name, err)
		}
		model := podspec.DeploymentModel(c.In.DeploymentModel)
		out = append(out, builtCase{Name: c.Name, Sidecar: model == "" || model == podspec.DeploymentSidecar, Pod: pod})
	}
	return out, nil
}

// copyCases deep-copies built cases so a negative test perturbs its own pods.
func copyCases(cases []builtCase) []builtCase {
	out := make([]builtCase, len(cases))
	for i, c := range cases {
		out[i] = builtCase{Name: c.Name, Sidecar: c.Sidecar, Pod: c.Pod.DeepCopy()}
	}
	return out
}

// argvUse is one rendered argv attributed to a binary and a flag set.
type argvUse struct {
	Case, Site   string
	Binary, Set  string
	Args         []string
	TrailingArgv bool
}

// envUse is one rendered variable attributed to the binary that reads it.
type envUse struct {
	Case, Site, Binary, Name string
}

// embeddedRuntimeBinaries are the in-tree binaries the embedded runtime
// container's argv and environment are checked against. The image is a
// deployer's first-party binary; mapping it to both reference runtimes
// asserts the contract each of them keeps.
var embeddedRuntimeBinaries = []string{"echo-embedded", "preconnect-echo"}

// attribute applies the render-site table to one built pod. It returns the
// argv and environment uses the table maps and one entry per site carrying
// argv or environment that no row maps.
//
// The table:
//
//	sidecar  adapter                  args      lenny-adapter, main set
//	sidecar  adapter                  env       lenny-adapter
//	any      any container            preStop   token 1 is prestop: lenny-adapter, prestop set
//	sidecar  runtime                  env       the runtime SDK socket variable only
//	embedded runtime                  args      each embedded runtime binary, main set
//	embedded runtime                  env       each embedded runtime binary
//	any      egress-capture           args      lenny-egress-capture, main set
func attribute(c builtCase) ([]argvUse, []envUse, []string) {
	var argv []argvUse
	var env []envUse
	var unmapped []string
	unmap := func(site string) { unmapped = append(unmapped, "unmapped render site: "+c.Name+" "+site) }
	addEnv := func(site string, ctr corev1.Container, binaries ...string) {
		for _, e := range ctr.Env {
			for _, b := range binaries {
				env = append(env, envUse{Case: c.Name, Site: site, Binary: b, Name: e.Name})
			}
		}
	}
	addArgs := func(site string, args []string, binaries ...string) {
		for _, b := range binaries {
			argv = append(argv, argvUse{Case: c.Name, Site: site, Binary: b, Args: append([]string(nil), args...)})
		}
	}
	visit := func(kind string, ctr corev1.Container) {
		prefix := fmt.Sprintf("%s[%s]", kind, ctr.Name)
		argsMapped, envMapped := false, false
		if kind == "containers" {
			switch {
			case c.Sidecar && ctr.Name == "adapter":
				addArgs(prefix+".args", ctr.Args, "lenny-adapter")
				addEnv(prefix+".env", ctr, "lenny-adapter")
				argsMapped, envMapped = true, true
			case c.Sidecar && ctr.Name == "runtime":
				addEnv(prefix+".env", ctr, runtimeSDKBinary)
				envMapped = true
			case !c.Sidecar && ctr.Name == "runtime":
				addArgs(prefix+".args", ctr.Args, embeddedRuntimeBinaries...)
				addEnv(prefix+".env", ctr, embeddedRuntimeBinaries...)
				argsMapped, envMapped = true, true
			case ctr.Name == podspec.EgressCaptureContainerName:
				addArgs(prefix+".args", ctr.Args, "lenny-egress-capture")
				argsMapped = true
			}
		}
		if len(ctr.Command) > 0 {
			unmap(prefix + ".command")
		}
		if len(ctr.Args) > 0 && !argsMapped {
			unmap(prefix + ".args")
		}
		if len(ctr.Env) > 0 && !envMapped {
			unmap(prefix + ".env")
		}
		if len(ctr.EnvFrom) > 0 {
			unmap(prefix + ".envFrom")
		}
		attributeLifecycle(c, prefix, ctr, &argv, unmap)
		for name, probe := range map[string]*corev1.Probe{"livenessProbe": ctr.LivenessProbe, "readinessProbe": ctr.ReadinessProbe, "startupProbe": ctr.StartupProbe} {
			if probe != nil && probe.Exec != nil && len(probe.Exec.Command) > 0 {
				unmap(prefix + "." + name + ".exec.command")
			}
		}
	}
	for _, ctr := range c.Pod.Spec.InitContainers {
		visit("initContainers", ctr)
	}
	for _, ctr := range c.Pod.Spec.Containers {
		visit("containers", ctr)
	}
	for _, ctr := range c.Pod.Spec.EphemeralContainers {
		visit("ephemeralContainers", corev1.Container(ctr.EphemeralContainerCommon))
	}
	sort.Strings(unmapped)
	return argv, env, unmapped
}

// attributeLifecycle maps a container's lifecycle exec hooks. A preStop exec
// whose token 1 is `prestop` is the adapter's drain subcommand, parsed from
// token 2 against the prestop set. The gate does not check token 0 against
// the image, and it does not check the embedded runtime's preStop against the
// embedded runtime binaries.
func attributeLifecycle(c builtCase, prefix string, ctr corev1.Container, argv *[]argvUse, unmap func(string)) {
	if ctr.Lifecycle == nil {
		return
	}
	if h := ctr.Lifecycle.PreStop; h != nil && h.Exec != nil && len(h.Exec.Command) > 0 {
		site := prefix + ".lifecycle.preStop.exec.command"
		cmd := h.Exec.Command
		if len(cmd) >= 2 && cmd[1] == "prestop" {
			*argv = append(*argv, argvUse{
				Case: c.Name, Site: site, Binary: "lenny-adapter", Set: "prestop",
				Args: append([]string(nil), cmd[2:]...),
			})
		} else {
			unmap(site)
		}
	}
	if h := ctr.Lifecycle.PostStart; h != nil && h.Exec != nil && len(h.Exec.Command) > 0 {
		unmap(prefix + ".lifecycle.postStart.exec.command")
	}
}

// attributeCases applies attribute to every case.
func attributeCases(cases []builtCase) ([]argvUse, []envUse, []string) {
	var argv []argvUse
	var env []envUse
	var unmapped []string
	for _, c := range cases {
		a, e, u := attribute(c)
		argv = append(argv, a...)
		env = append(env, e...)
		unmapped = append(unmapped, u...)
	}
	return argv, env, unmapped
}

// countedValue counts successful sets of a flag, so a flag rendered twice in
// one argv is visible: the flag package keeps the last value silently.
type countedValue struct {
	flag.Value
	n *int
}

func (v countedValue) Set(s string) error {
	if err := v.Value.Set(s); err != nil {
		return err
	}
	*v.n++
	return nil
}

// IsBoolFlag preserves the boolean form of a wrapped boolean flag.
func (v countedValue) IsBoolFlag() bool {
	b, ok := v.Value.(interface{ IsBoolFlag() bool })
	return ok && b.IsBoolFlag()
}

// declaredValue returns a fresh flag.Value of the type a declaration method
// declares. The opaque forms accept any string.
func declaredValue(method string) flag.Value {
	tmp := flag.NewFlagSet("declared", flag.ContinueOnError)
	switch strings.TrimSuffix(method, "Var") {
	case "String":
		tmp.String("v", "", "")
	case "Bool":
		tmp.Bool("v", false, "")
	case "Int":
		tmp.Int("v", 0, "")
	case "Int64":
		tmp.Int64("v", 0, "")
	case "Uint":
		tmp.Uint("v", 0, "")
	case "Uint64":
		tmp.Uint64("v", 0, "")
	case "Float64":
		tmp.Float64("v", 0, "")
	case "Duration":
		tmp.Duration("v", 0, "")
	case "BoolFunc":
		tmp.BoolFunc("v", "", func(string) error { return nil })
	default:
		tmp.Func("v", "", func(string) error { return nil })
	}
	return tmp.Lookup("v").Value
}

// parseArgv rebuilds the flag set u names from the binary's declarations with
// their declared types, parses u's argv with ContinueOnError and discarded
// output, and returns the names Visit reports set, plus one defect per parse
// error, flag set twice, or positional argument at a site that admits none.
// The rebuilt set is at least as strict as the binary: a flag the scan does
// not model, such as one a third-party package registers, is undefined here.
func parseArgv(decls []declaredFlag, u argvUse) (map[string]bool, []string) {
	fs := flag.NewFlagSet(u.Binary+" "+u.Set, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	counts := map[string]*int{}
	for _, d := range decls {
		if d.Set != u.Set || fs.Lookup(d.Name) != nil {
			continue
		}
		n := new(int)
		counts[d.Name] = n
		fs.Var(countedValue{Value: declaredValue(d.Method), n: n}, d.Name, "")
	}
	where := fmt.Sprintf("%s %s (%s)", u.Case, u.Site, u.Binary)
	var defects []string
	if err := fs.Parse(u.Args); err != nil {
		defects = append(defects, fmt.Sprintf("%s: %v", where, err))
	}
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	names := make([]string, 0, len(counts))
	for name := range counts {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if *counts[name] > 1 {
			defects = append(defects, fmt.Sprintf("%s: flag --%s set %d times; the flag package keeps the last value", where, name, *counts[name]))
		}
	}
	if fs.NArg() > 0 && !u.TrailingArgv {
		defects = append(defects, fmt.Sprintf("%s: positional argument %q at a site that admits none", where, fs.Arg(0)))
	}
	return set, defects
}

// flagSubject is a flag's subject in the register vocabulary.
func flagSubject(binary, set, name string) string {
	return binary + " " + declaredFlag{Set: set, Name: name}.key()
}

// envSubject is an environment variable's subject in the register vocabulary.
func envSubject(binary, name string) string {
	return binary + " env " + name
}

// renderedSet maps each rendered subject to the cases that render it.
type renderedSet map[string]map[string]bool

func (r renderedSet) add(subject, caseName string) {
	if r[subject] == nil {
		r[subject] = map[string]bool{}
	}
	r[subject][caseName] = true
}

// renderedInputs computes the rendered set. A flag is rendered in a case when
// its parsed argv sets it, or when the case puts one of the flag's bound
// variables in the environment of a container mapped to the flag's binary.
func renderedInputs(argv []argvUse, env []envUse, inputs map[string]binaryInputs) renderedSet {
	r := renderedSet{}
	for _, u := range argv {
		set, _ := parseArgv(inputs[u.Binary].Flags, u)
		for name := range set {
			r.add(flagSubject(u.Binary, u.Set, name), u.Case)
		}
	}
	for _, u := range env {
		if u.Binary == runtimeSDKBinary {
			continue
		}
		r.add(envSubject(u.Binary, u.Name), u.Case)
		for _, read := range inputs[u.Binary].Env {
			if read.Name == u.Name && read.BoundTo != "" {
				r.add(u.Binary+" "+read.BoundTo, u.Case)
			}
		}
	}
	return r
}

// sdkSocketVariable is the one variable the sidecar runtime container may
// carry.
const sdkSocketVariable = runtimekit.SocketEnvVar
