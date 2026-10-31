// SPDX-License-Identifier: MIT

package flagdefaults

import (
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/lennylabs/lenny/pkg/credential"
)

// requiredFlags lists every operationally-tunable flag the gateway
// must expose. The flag name is the bare token (no leading dashes);
// the assertion accepts either `-<flag>` or `--<flag>` in --help
// output, matching Go's flag.Flag.PrintDefaults() format.
var requiredFlags = []struct {
	Binary string
	Flag   string
}{
	{"lenny-gateway", "cluster-qps"},
	{"lenny-gateway", "cluster-burst"},
	{"lenny-gateway", "token-service-grpc-addr"},
	// spec: §4.9 Credential Storage Topology. The admin credential-pool
	// handlers parse every secretRef against the credentials namespace.
	{"lenny-gateway", "credentials-namespace"},
	// spec: §4.9 Token Service secret resolution. Every gateway credential
	// read is one get through the Token Service's Kubernetes client.
	{"lenny-token-service", "cluster-qps"},
	{"lenny-token-service", "cluster-burst"},
	// spec: §4.9 Token Service secret resolution rule 1. Every Token
	// Service credential Secret read targets the credentials namespace.
	{"lenny-token-service", "secret-namespace"},
}

// credentialsNamespaceFlags lists the flag each binary carries the
// credentials namespace on.
var credentialsNamespaceFlags = []struct {
	Binary string
	Flag   string
}{
	{"lenny-gateway", "credentials-namespace"},
	{"lenny-token-service", "secret-namespace"},
}

// helpCache memoises --help output per binary across the parallel subtests.
var (
	helpCacheMu sync.Mutex
	helpCache   = map[string]string{}
)

func TestRequiredFlagsExposedInHelp(t *testing.T) {
	repo := repoRoot(t)
	for _, e := range requiredFlags {
		e := e
		t.Run(e.Binary+"/"+e.Flag, func(t *testing.T) {
			t.Parallel()
			help, err := loadHelp(repo, e.Binary)
			if err != nil {
				t.Skipf("could not run --help for %s: %v", e.Binary, err)
			}
			if !strings.Contains(help, "-"+e.Flag) {
				t.Errorf("%s --help does not surface flag %q", e.Binary, e.Flag)
			}
		})
	}
}

// diagnosis: a failure means a binary started without the chart's explicit
// argument targets a namespace other than credential.DefaultNamespace, so the
// gateway canonicalizes a reference into one namespace and the Token Service
// reads a same-named Secret from another.
// spec: §4.9 (Credential Lifecycle and Secret Management) — Token Service
// secret resolution rule 1; §17.6 (Packaging and Installation)
func TestCredentialsNamespaceFlagDefaultsAgree_spec_4_9(t *testing.T) {
	repo := repoRoot(t)
	for _, e := range credentialsNamespaceFlags {
		t.Run(e.Binary, func(t *testing.T) {
			help, err := loadHelp(repo, e.Binary)
			if err != nil {
				t.Fatalf("run --help for %s: %v", e.Binary, err)
			}
			if got := flagDefault(help, e.Flag); got != credential.DefaultNamespace {
				t.Errorf("%s --%s default = %q, want %q", e.Binary, e.Flag, got, credential.DefaultNamespace)
			}
		})
	}
}

// flagDefault returns the default the --help text of flag states, or "" when
// the text states none.
func flagDefault(help, flagName string) string {
	re := regexp.MustCompile(`-` + regexp.QuoteMeta(flagName) + ` string\n[^\n]*\(default "([^"]*)"\)`)
	m := re.FindStringSubmatch(help)
	if m == nil {
		return ""
	}
	return m[1]
}

func loadHelp(repo, binary string) (string, error) {
	helpCacheMu.Lock()
	if h, ok := helpCache[binary]; ok {
		helpCacheMu.Unlock()
		return h, nil
	}
	helpCacheMu.Unlock()
	cmd := exec.Command("go", "run", "./cmd/"+binary, "--help")
	cmd.Dir = repo
	out, _ := cmd.CombinedOutput()
	helpCacheMu.Lock()
	helpCache[binary] = string(out)
	helpCacheMu.Unlock()
	return string(out), nil
}

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", ".."))
}
