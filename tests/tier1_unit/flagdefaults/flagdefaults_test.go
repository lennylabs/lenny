// SPDX-License-Identifier: MIT

package flagdefaults

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
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
				t.Errorf("§6.5 violated: %s --help does not surface flag %q", e.Binary, e.Flag)
			}
		})
	}
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
