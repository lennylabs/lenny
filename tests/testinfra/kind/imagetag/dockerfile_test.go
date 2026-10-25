// SPDX-License-Identifier: MIT

package imagetag

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// repoRoot is the repository root relative to this package directory.
const repoRoot = "../../../.."

// spec: TESTING.md §12.5 (Tier 5 — E2E on Kind)
//
// The analyzer reads each Dockerfile the Kind install builds as the build
// does: the root Dockerfile compiles ./cmd/$BINARY and copies go.mod, go.sum,
// and the runbooks; the supervisor Dockerfile copies only supervisor/; the
// fixture and probe Dockerfiles compile their own packages; and the adapter
// overlay reads nothing from its context.
func TestAnalyzeDockerfileReadsTheRepositoryDockerfiles(t *testing.T) {
	cases := []struct {
		dockerfile string
		args       map[string]string
		want       dockerfileInputs
	}{
		{
			dockerfile: "Dockerfile",
			args:       map[string]string{"BINARY": "lenny-gateway"},
			want: dockerfileInputs{
				Sources:    []string{"docs/runbooks", "go.mod", "go.sum"},
				GoPackages: []string{"./cmd/lenny-gateway"},
			},
		},
		{
			dockerfile: "Dockerfile",
			args:       map[string]string{"BINARY": "runtimes/echo"},
			want: dockerfileInputs{
				Sources:    []string{"docs/runbooks", "go.mod", "go.sum"},
				GoPackages: []string{"./cmd/runtimes/echo"},
			},
		},
		{
			dockerfile: "Dockerfile.supervisor",
			want:       dockerfileInputs{Sources: []string{"supervisor"}, GoPackages: []string{}},
		},
		{
			dockerfile: "tests/testinfra/heartbeatsilence/Dockerfile",
			args:       map[string]string{"BASE_IMAGE": "x"},
			want: dockerfileInputs{
				Sources:    []string{"go.mod", "go.sum"},
				GoPackages: []string{"./tests/testinfra/heartbeatsilence/cmd/heartbeat-silence"},
			},
		},
		{
			dockerfile: "cmd/runtimes/supervise-probe/Dockerfile",
			want: dockerfileInputs{
				Sources:    []string{"go.mod", "go.sum", "supervisor"},
				GoPackages: []string{"./cmd/runtimes/supervise-probe"},
			},
		},
		{
			dockerfile: "cmd/runtimes/supervise-probe/adapter-overlay.Dockerfile",
			want:       dockerfileInputs{Sources: []string{}, GoPackages: []string{}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.dockerfile+" "+tc.args["BINARY"], func(t *testing.T) {
			text, err := os.ReadFile(filepath.Join(repoRoot, tc.dockerfile))
			if err != nil {
				t.Fatal(err)
			}
			got, err := analyzeDockerfile(string(text), tc.args)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("analyzeDockerfile(%s) = %+v, want %+v", tc.dockerfile, got, tc.want)
			}
		})
	}
}

// spec: TESTING.md §12.5 (Tier 5 — E2E on Kind)
//
// A Dockerfile the analyzer cannot bound falls back to the whole context, and
// one it cannot parse is an error rather than a guessed input set.
func TestAnalyzeDockerfileFallsBackOrFailsOnUnboundedInputs(t *testing.T) {
	cases := []struct {
		name      string
		text      string
		args      map[string]string
		wantWhole bool
		wantErr   string
	}{
		{
			name:      "non-Go command after a whole-context copy",
			text:      "FROM golang AS b\nCOPY . .\nRUN go build -o /x ./cmd/a && ./scripts/gen.sh\n",
			wantWhole: true,
		},
		{
			name:      "whole-context copy with no Go build",
			text:      "FROM alpine\nCOPY . /src\nRUN true\n",
			wantWhole: true,
		},
		{
			name:      "JSON-form RUN after a whole-context copy",
			text:      "FROM golang\nCOPY . .\nRUN [\"go\", \"build\", \"./cmd/a\"]\n",
			wantWhole: true,
		},
		{
			name:      "whole-context copy in an earlier stage does not leak into a later one",
			text:      "FROM golang AS b\nCOPY . .\nRUN go build ./cmd/a\nFROM alpine\nRUN apk add curl\n",
			wantWhole: false,
		},
		{
			name: "continuation lines, env prefixes, and a test guard are Go-only",
			text: "FROM golang\nARG BIN\nCOPY . .\nRUN test -n \"$BIN\" || { echo missing >&2; exit 1; }\n" +
				"RUN CGO_ENABLED=0 GOOS=linux go build \\\n  -o /out ./cmd/\"${BIN}\"\n",
			args:      map[string]string{"BIN": "a"},
			wantWhole: false,
		},
		{
			name:    "JSON-form COPY is rejected",
			text:    "FROM alpine\nCOPY [\"a\", \"/a\"]\n",
			wantErr: "JSON-form",
		},
		{
			name:    "unresolved build argument in a go build path",
			text:    "FROM golang\nCOPY . .\nRUN go build ./cmd/$BINARY\n",
			wantErr: "BINARY",
		},
		{
			name:    "unresolved build argument in a COPY source",
			text:    "FROM alpine\nCOPY ${SRC} /x\n",
			wantErr: "SRC",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := analyzeDockerfile(tc.text, tc.args)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want one naming %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.WholeContext != tc.wantWhole {
				t.Errorf("WholeContext = %v, want %v (%+v)", got.WholeContext, tc.wantWhole, got)
			}
		})
	}
}

// spec: TESTING.md §12.5 (Tier 5 — E2E on Kind)
//
// COPY --from reads another stage rather than the context, ARG defaults apply
// when the build passes no value, and a passed value overrides the default.
func TestAnalyzeDockerfileArgumentsAndStageCopies(t *testing.T) {
	text := "ARG DIR=docs\nFROM alpine AS a\nCOPY --chown=1:1 ${DIR}/x /x\nFROM scratch\nCOPY --from=a /x /x\n"
	got, err := analyzeDockerfile(text, nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"docs/x"}; !reflect.DeepEqual(got.Sources, want) {
		t.Errorf("default ARG: Sources = %v, want %v", got.Sources, want)
	}
	got, err = analyzeDockerfile(text, map[string]string{"DIR": "other"})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"other/x"}; !reflect.DeepEqual(got.Sources, want) {
		t.Errorf("passed ARG: Sources = %v, want %v", got.Sources, want)
	}
}
