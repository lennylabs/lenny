// SPDX-License-Identifier: MIT

package kind

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// TestDedicatedClusterArgsAlwaysNamesAPrivateKubeconfig pins the helper
// every dedicated-cluster kind command goes through. A kind create or
// delete that inherits $KUBECONFIG rewrites the caller's file, and a
// create-then-delete pair against the shared cluster's kubeconfig leaves
// it with no current-context (T-17.4.30).
//
// spec: TESTING.md §12.5 (Tier 5 — E2E on Kind)
func TestDedicatedClusterArgsAlwaysNamesAPrivateKubeconfig(t *testing.T) {
	cases := []struct {
		name       string
		kubeconfig string
		args       []string
		want       []string
		wantErr    bool
	}{
		{
			name:       "create",
			kubeconfig: "/tmp/x/kubeconfig",
			args:       []string{"create", "cluster", "--name", "dedicated"},
			want:       []string{"create", "cluster", "--name", "dedicated", "--kubeconfig", "/tmp/x/kubeconfig"},
		},
		{
			name:       "delete",
			kubeconfig: "/tmp/x/kubeconfig",
			args:       []string{"delete", "cluster", "--name", "dedicated"},
			want:       []string{"delete", "cluster", "--name", "dedicated", "--kubeconfig", "/tmp/x/kubeconfig"},
		},
		{name: "empty path", kubeconfig: "", args: []string{"delete", "cluster"}, wantErr: true},
		{name: "blank path", kubeconfig: "  ", args: []string{"delete", "cluster"}, wantErr: true},
		{
			name:       "caller already names a kubeconfig",
			kubeconfig: "/tmp/x/kubeconfig",
			args:       []string{"create", "cluster", "--kubeconfig", "/home/alice/.kube/config"},
			wantErr:    true,
		},
		{
			name:       "caller already names a kubeconfig with equals",
			kubeconfig: "/tmp/x/kubeconfig",
			args:       []string{"create", "cluster", "--kubeconfig=/home/alice/.kube/config"},
			wantErr:    true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := append([]string(nil), tc.args...)
			got, err := DedicatedClusterArgs(tc.kubeconfig, tc.args...)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("DedicatedClusterArgs(%q, %q) = %q, want an error", tc.kubeconfig, tc.args, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("DedicatedClusterArgs(%q, %q): %v", tc.kubeconfig, tc.args, err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("DedicatedClusterArgs(%q, %q) = %q, want %q", tc.kubeconfig, tc.args, got, tc.want)
			}
			if !reflect.DeepEqual(tc.args, in) {
				t.Fatalf("DedicatedClusterArgs mutated its input: %q, was %q", tc.args, in)
			}
		})
	}
}

// bareKindClusterCommand matches a kind create or delete command spelled
// directly in Go source, which inherits the caller's $KUBECONFIG.
var bareKindClusterCommand = regexp.MustCompile(`exec\.Command\(\s*"kind",\s*"(create|delete)",\s*"cluster"`)

// TestNoTestCreatesOrDeletesAKindClusterAgainstTheInheritedKubeconfig
// holds every test outside this package to DedicatedClusterArgs. Only
// EnsureCluster and Cluster.Delete manage the shared cluster with the
// inherited kubeconfig, because that kubeconfig names the shared cluster.
//
// spec: TESTING.md §12.5 (Tier 5 — E2E on Kind)
func TestNoTestCreatesOrDeletesAKindClusterAgainstTheInheritedKubeconfig(t *testing.T) {
	root := filepath.Join("..", "..", "..", "tests")
	self, err := filepath.Abs(".")
	if err != nil {
		t.Fatal(err)
	}
	var offenders []string
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "testdata" {
				return filepath.SkipDir
			}
			if abs, _ := filepath.Abs(path); abs == self {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if bareKindClusterCommand.Match(b) {
			offenders = append(offenders, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	if len(offenders) > 0 {
		t.Fatalf("kind create/delete cluster without a private kubeconfig (use DedicatedClusterArgs): %q", offenders)
	}
}

// installStep3 extracts the cluster-creation step of install.sh: from the
// `kind get clusters` check through the line before KCTX is set.
func installStep3(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("install.sh")
	if err != nil {
		t.Fatalf("read install.sh: %v", err)
	}
	s := string(b)
	start := strings.Index(s, `if kind get clusters 2>/dev/null | grep -qx "${CLUSTER}"; then`)
	end := strings.Index(s, `KCTX="kind-${CLUSTER}"`)
	if start < 0 || end < 0 || end < start {
		t.Fatalf("install.sh: cluster-creation step not found (start=%d end=%d)", start, end)
	}
	return s[start:end]
}

// TestInstallRestoresTheKubeconfigCurrentContext runs the install
// script's cluster-creation step against a stub kind, for a cluster that
// already exists and for one that does not, starting from a kubeconfig
// with no current-context. The step must leave current-context at
// kind-<cluster> in both cases, so an install repairs a kubeconfig that
// another process cleared (T-17.4.30).
//
// spec: TESTING.md §12.5 (Tier 5 — E2E on Kind)
func TestInstallRestoresTheKubeconfigCurrentContext(t *testing.T) {
	// bash is a hard requirement: install.sh is a bash script, so a host
	// without it cannot run the code under test and the test fails.
	if _, err := exec.LookPath("bash"); err != nil {
		t.Fatalf("bash not on PATH: %v", err)
	}
	step := installStep3(t)

	// The stub records each invocation and models the kubeconfig effect
	// of the subcommands the step uses: create and export set the
	// current-context, and get clusters lists $STUB_CLUSTERS.
	stub := `#!/usr/bin/env bash
echo "$*" >> "${STUB_LOG}"
case "$1 $2" in
  "get clusters") printf '%s\n' ${STUB_CLUSTERS} ;;
  "create cluster"|"export kubeconfig")
    printf 'apiVersion: v1\nkind: Config\ncurrent-context: kind-%s\n' "$4" > "${KUBECONFIG}" ;;
  *) echo "stub kind: unexpected $*" >&2; exit 3 ;;
esac
`
	for _, tc := range []struct {
		name       string
		clusters   string
		wantCreate bool
	}{
		{name: "existing cluster", clusters: "lenny-e2e other", wantCreate: false},
		{name: "new cluster", clusters: "other", wantCreate: true},
		{name: "no clusters", clusters: "", wantCreate: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			bin := filepath.Join(dir, "bin")
			if err := os.Mkdir(bin, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(bin, "kind"), []byte(stub), 0o755); err != nil {
				t.Fatal(err)
			}
			kubeconfig := filepath.Join(dir, "kubeconfig")
			// The state the defect left: the shared cluster's entries
			// with no current-context.
			if err := os.WriteFile(kubeconfig, []byte("apiVersion: v1\nkind: Config\ncontexts:\n- name: kind-lenny-e2e\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			log := filepath.Join(dir, "calls")
			script := "set -euo pipefail\nlog() { :; }\nCLUSTER=lenny-e2e\nKIND_CONFIG=/dev/null\n" + step
			cmd := exec.Command("bash", "-c", script)
			cmd.Env = append(
				os.Environ(),
				"PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
				"KUBECONFIG="+kubeconfig,
				"STUB_LOG="+log,
				"STUB_CLUSTERS="+tc.clusters,
			)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("install step: %v\n%s", err, out)
			}
			calls, err := os.ReadFile(log)
			if err != nil {
				t.Fatal(err)
			}
			lines := strings.Split(strings.TrimSpace(string(calls)), "\n")
			if got := lines[len(lines)-1]; got != "export kubeconfig --name lenny-e2e" {
				t.Errorf("last kind call = %q, want the kubeconfig export; calls: %q", got, lines)
			}
			created := strings.Contains(string(calls), "create cluster --name lenny-e2e")
			if created != tc.wantCreate {
				t.Errorf("created = %v, want %v; calls: %q", created, tc.wantCreate, lines)
			}
			kc, err := os.ReadFile(kubeconfig)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(kc), "current-context: kind-lenny-e2e") {
				t.Errorf("kubeconfig after the step lacks current-context: kind-lenny-e2e:\n%s", kc)
			}
		})
	}
}
