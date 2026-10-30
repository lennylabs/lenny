// SPDX-License-Identifier: MIT

package containers

import (
	"bytes"
	"context"
	"io"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// promtoolImage is the Prometheus image used to run `promtool test
// rules`. promtool ships inside the Prometheus image, so the rule engine
// the assertions run against is the same one operators deploy. Pinned to
// the version the dev compose observability profile runs.
const promtoolImage = "prom/prometheus:v3.2.1"

// promtoolWorkDir is the in-container directory the rule file and the
// unit-test file are copied to. The unit-test document references the
// rule file by the absolute path RunPromtoolRuleTest writes below, so no
// working-directory override is needed.
const promtoolWorkDir = "/work"

// PromtoolRulesPath is the in-container path RunPromtoolRuleTest writes
// the rule file to. A unit-test document's `rule_files:` entry must name
// this exact path so promtool loads the rendered catalog under test.
const PromtoolRulesPath = promtoolWorkDir + "/rules.yaml"

// promtoolTestPath is the in-container path the unit-test file is written
// to and the argument passed to `promtool test rules`.
const promtoolTestPath = promtoolWorkDir + "/test.yaml"

// PromtoolResult is the outcome of a `promtool test rules` invocation.
type PromtoolResult struct {
	// ExitCode is the process exit code. promtool exits 0 when every
	// unit-test assertion passes and non-zero when any assertion fails.
	ExitCode int
	// Output is the combined stdout and stderr, which names the failing
	// assertions (expected vs. got alerts) when ExitCode is non-zero.
	Output string
}

// RunPromtoolRuleTest runs `promtool test rules` in a throwaway
// prom/prometheus container against rulesYAML (a Prometheus rule file in
// the `groups:` format) and testYAML (a promtool unit-test document whose
// `rule_files:` entry names PromtoolRulesPath). The container runs to
// completion; the returned result carries its exit code and output.
//
// promtool evaluates alert rules against synthetic input series in
// simulated time, so `for:` sustain windows are honored without any
// wall-clock waiting. This is the real Prometheus rule engine, which is
// what the §25.13 bundled rules are loaded into once rendered.
func RunPromtoolRuleTest(t testing.TB, rulesYAML, testYAML []byte) PromtoolResult {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image: promtoolImage,
			// promtool ships alongside the prometheus server binary in the
			// image; override the server entrypoint to run it one-shot.
			Entrypoint: []string{"/bin/promtool"},
			Cmd:        []string{"test", "rules", promtoolTestPath},
			Files: []testcontainers.ContainerFile{
				{Reader: bytes.NewReader(rulesYAML), ContainerFilePath: PromtoolRulesPath, FileMode: 0o644},
				{Reader: bytes.NewReader(testYAML), ContainerFilePath: promtoolTestPath, FileMode: 0o644},
			},
			WaitingFor: wait.ForExit().WithExitTimeout(60 * time.Second),
		},
		Started: true,
	})
	if err != nil {
		t.Fatalf("RunPromtoolRuleTest: start container: %v", err)
	}
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer stopCancel()
		_ = container.Terminate(stopCtx)
	})

	state, err := container.State(ctx)
	if err != nil {
		t.Fatalf("RunPromtoolRuleTest: container state: %v", err)
	}

	var output string
	if rc, logErr := container.Logs(ctx); logErr == nil {
		defer rc.Close()
		if b, readErr := io.ReadAll(rc); readErr == nil {
			output = string(b)
		}
	}

	return PromtoolResult{ExitCode: state.ExitCode, Output: output}
}
