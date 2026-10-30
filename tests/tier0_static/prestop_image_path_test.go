// SPDX-License-Identifier: MIT

package tier0_static

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/adapter/prestop"
	"github.com/lennylabs/lenny/pkg/controller/sandbox/podspec"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// This file pins the agent pod's preStop exec path to the image layout. The
// kubelet runs a preStop exec hook inside the container's image, so token 0
// of the rendered command has to name a file the image contains. The adapter
// image and the first-party embedded runtime images are built from the shared
// root Dockerfile onto a distroless base with no shell, so the only
// executable is the one its final stage copies in and names as ENTRYPOINT.
// The pod builder once rendered `lenny-adapter`, a name no image carries, and
// the hook failed at every pod termination (BUILD-GAPS F-4.6.37). The checks
// below fail at tier 0 when either side is renamed without the other.

// platformImageLayout is what the shared Dockerfile's final stage installs.
type platformImageLayout struct {
	BuildOutput string // the `go build -o` path in the build stage
	CopySource  string // the source of the final stage's COPY --from
	InstallPath string // the destination of the final stage's COPY --from
	Entrypoint  string // the first element of the final stage's ENTRYPOINT
}

var (
	goBuildOutputRe = regexp.MustCompile(`\bgo build\b.*\s-o\s+(\S+)`)
	copyFromRe      = regexp.MustCompile(`^COPY\s+--from=\S+\s+(\S+)\s+(\S+)$`)
)

// dockerfileInstructions joins backslash continuations and drops comments and
// blank lines, returning one string per instruction.
func dockerfileInstructions(src string) []string {
	var out []string
	var cur strings.Builder
	for _, line := range strings.Split(src, "\n") {
		trimmed := strings.TrimSpace(line)
		if cur.Len() == 0 && (trimmed == "" || strings.HasPrefix(trimmed, "#")) {
			continue
		}
		if strings.HasSuffix(trimmed, `\`) {
			cur.WriteString(strings.TrimSuffix(trimmed, `\`))
			cur.WriteString(" ")
			continue
		}
		cur.WriteString(trimmed)
		out = append(out, cur.String())
		cur.Reset()
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

// parsePlatformImageLayout reads the build output, the final stage's single
// COPY --from, and its exec-form ENTRYPOINT. Any form it cannot read is an
// error, so a restructured Dockerfile fails the check rather than passing it.
func parsePlatformImageLayout(src string) (platformImageLayout, error) {
	var layout platformImageLayout
	instrs := dockerfileInstructions(src)
	finalFrom := -1
	for i, ins := range instrs {
		if strings.HasPrefix(strings.ToUpper(ins), "FROM ") {
			finalFrom = i
		}
	}
	if finalFrom < 0 {
		return layout, fmt.Errorf("no FROM instruction")
	}
	for _, ins := range instrs[:finalFrom] {
		if m := goBuildOutputRe.FindStringSubmatch(ins); m != nil {
			if layout.BuildOutput != "" {
				return layout, fmt.Errorf("more than one go build -o in the build stage")
			}
			layout.BuildOutput = m[1]
		}
	}
	for _, ins := range instrs[finalFrom+1:] {
		switch {
		case strings.HasPrefix(ins, "COPY --from="):
			m := copyFromRe.FindStringSubmatch(ins)
			if m == nil {
				return layout, fmt.Errorf("unreadable final-stage COPY: %q", ins)
			}
			if layout.InstallPath != "" {
				return layout, fmt.Errorf("more than one COPY --from in the final stage")
			}
			layout.CopySource, layout.InstallPath = m[1], m[2]
		case strings.HasPrefix(ins, "ENTRYPOINT"):
			var argv []string
			if err := json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(ins, "ENTRYPOINT"))), &argv); err != nil || len(argv) == 0 {
				return layout, fmt.Errorf("ENTRYPOINT is not a non-empty exec-form array: %q", ins)
			}
			layout.Entrypoint = argv[0]
		}
	}
	switch {
	case layout.BuildOutput == "":
		return layout, fmt.Errorf("no go build -o in the build stage")
	case layout.InstallPath == "":
		return layout, fmt.Errorf("no COPY --from in the final stage")
	case layout.Entrypoint == "":
		return layout, fmt.Errorf("no ENTRYPOINT in the final stage")
	}
	return layout, nil
}

// layoutDefects compares a parsed layout and the rendered preStop commands
// against the binary path the pod builder renders.
func layoutDefects(layout platformImageLayout, rendered string, commands map[string][]string) []string {
	var defects []string
	if layout.CopySource != layout.BuildOutput {
		defects = append(defects, fmt.Sprintf("final-stage COPY source %q is not the go build output %q", layout.CopySource, layout.BuildOutput))
	}
	if layout.Entrypoint != layout.InstallPath {
		defects = append(defects, fmt.Sprintf("ENTRYPOINT %q is not the installed binary %q", layout.Entrypoint, layout.InstallPath))
	}
	if rendered != layout.InstallPath {
		defects = append(defects, fmt.Sprintf("podspec.PlatformBinaryPath %q is not the installed binary %q", rendered, layout.InstallPath))
	}
	for site, cmd := range commands {
		if len(cmd) < 2 || cmd[0] != layout.InstallPath || cmd[1] != prestop.Verb {
			defects = append(defects, fmt.Sprintf("%s: preStop command %v does not exec %s %s", site, cmd, layout.InstallPath, prestop.Verb))
		}
	}
	return defects
}

// renderedPreStopCommands builds every deployment-boundary render case and
// returns the preStop exec command of each hooked container by site. It fails
// the test when a case renders no hook, since §4.6.1 requires one per pod.
func renderedPreStopCommands(t *testing.T) map[string][]string {
	t.Helper()
	built, err := buildCases(renderCases())
	if err != nil {
		t.Fatalf("build render cases: %v", err)
	}
	commands := map[string][]string{}
	for _, c := range built {
		hooked := 0
		for _, ctr := range c.Pod.Spec.Containers {
			if ctr.Lifecycle == nil || ctr.Lifecycle.PreStop == nil || ctr.Lifecycle.PreStop.Exec == nil {
				continue
			}
			hooked++
			commands[c.Name+" containers["+ctr.Name+"]"] = ctr.Lifecycle.PreStop.Exec.Command
		}
		if hooked == 0 {
			t.Errorf("render case %s carries no preStop exec hook", c.Name)
		}
	}
	return commands
}

// TestPreStopExecPathMatchesDockerfileInstallPath pins token 0 of the
// rendered preStop command, in both deployment models, to the path the shared
// Dockerfile installs its binary at and names as ENTRYPOINT.
//
// spec: 4.6.1 (Warm Pool Controller (Pod Lifecycle)), 4.7.10 (Deployment Model)
func TestPreStopExecPathMatchesDockerfileInstallPath(t *testing.T) {
	t.Parallel()
	root, err := schematest.RepoRootCwd()
	if err != nil {
		t.Fatalf("repo root: %v", err)
	}
	src, err := os.ReadFile(filepath.Join(root, "Dockerfile"))
	if err != nil {
		t.Fatalf("read Dockerfile: %v", err)
	}
	layout, err := parsePlatformImageLayout(string(src))
	if err != nil {
		t.Fatalf("parse Dockerfile: %v", err)
	}
	for _, d := range layoutDefects(layout, podspec.PlatformBinaryPath, renderedPreStopCommands(t)) {
		t.Error(d)
	}
}

// TestPreStopImagePathCheckReportsDrift feeds the check a Dockerfile and
// rendered commands that disagree in each way the check exists to catch, and
// confirms each disagreement is reported.
//
// spec: 4.6.1 (Warm Pool Controller (Pod Lifecycle)), 4.7.10 (Deployment Model)
func TestPreStopImagePathCheckReportsDrift(t *testing.T) {
	t.Parallel()
	const base = `FROM golang:1.25 AS build
RUN CGO_ENABLED=0 go build -trimpath \
    -o /out/lenny ./cmd/"$BINARY"
FROM gcr.io/distroless/static:nonroot
COPY --from=build /out/lenny %s
ENTRYPOINT ["%s"]
`
	good := map[string][]string{"case": {"/usr/local/bin/lenny", prestop.Verb, "--timeout=110s"}}
	cases := []struct {
		name       string
		dockerfile string
		rendered   string
		commands   map[string][]string
		want       []string
	}{
		{
			name:       "consistent",
			dockerfile: fmt.Sprintf(base, "/usr/local/bin/lenny", "/usr/local/bin/lenny"),
			rendered:   "/usr/local/bin/lenny",
			commands:   good,
		},
		{
			name:       "install path renamed",
			dockerfile: fmt.Sprintf(base, "/usr/local/bin/lenny-adapter", "/usr/local/bin/lenny-adapter"),
			rendered:   "/usr/local/bin/lenny",
			commands:   good,
			want:       []string{"podspec.PlatformBinaryPath", "does not exec"},
		},
		{
			name:       "entrypoint diverges",
			dockerfile: fmt.Sprintf(base, "/usr/local/bin/lenny", "/bin/lenny"),
			rendered:   "/usr/local/bin/lenny",
			commands:   good,
			want:       []string{"ENTRYPOINT"},
		},
		{
			name:       "bare binary name rendered",
			dockerfile: fmt.Sprintf(base, "/usr/local/bin/lenny", "/usr/local/bin/lenny"),
			rendered:   "/usr/local/bin/lenny",
			commands:   map[string][]string{"case": {"lenny-adapter", prestop.Verb, "--timeout=110s"}},
			want:       []string{"does not exec"},
		},
	}
	for _, tc := range cases {
		layout, err := parsePlatformImageLayout(tc.dockerfile)
		if err != nil {
			t.Fatalf("%s: parse: %v", tc.name, err)
		}
		got := layoutDefects(layout, tc.rendered, tc.commands)
		if len(got) != len(tc.want) {
			t.Errorf("%s: defects = %q, want %d matching %q", tc.name, got, len(tc.want), tc.want)
			continue
		}
		for _, w := range tc.want {
			found := false
			for _, g := range got {
				if strings.Contains(g, w) {
					found = true
				}
			}
			if !found {
				t.Errorf("%s: no defect mentions %q in %q", tc.name, w, got)
			}
		}
	}

	for name, bad := range map[string]string{
		"no ENTRYPOINT":       "FROM a AS build\nRUN go build -o /out/x .\nFROM b\nCOPY --from=build /out/x /x\n",
		"shell ENTRYPOINT":    "FROM a AS build\nRUN go build -o /out/x .\nFROM b\nCOPY --from=build /out/x /x\nENTRYPOINT /x\n",
		"two COPY --from":     "FROM a AS build\nRUN go build -o /out/x .\nFROM b\nCOPY --from=build /out/x /x\nCOPY --from=build /out/x /y\nENTRYPOINT [\"/x\"]\n",
		"no build output":     "FROM a AS build\nFROM b\nCOPY --from=build /out/x /x\nENTRYPOINT [\"/x\"]\n",
		"no final-stage COPY": "FROM a AS build\nRUN go build -o /out/x .\nFROM b\nENTRYPOINT [\"/x\"]\n",
	} {
		if _, err := parsePlatformImageLayout(bad); err == nil {
			t.Errorf("%s: parse accepted an unreadable Dockerfile", name)
		}
	}
}
