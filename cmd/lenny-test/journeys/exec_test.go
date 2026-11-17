// SPDX-License-Identifier: MIT

package journeys

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

// These tests exercise contributor tooling and carry no spec annotation.

func TestExecRunnerReturnsStandardOutputAlone(t *testing.T) {
	var log bytes.Buffer
	out, err := ExecRunner(context.Background(), &log, []string{"JOURNEY_TEST_VAR=v1"}, t.TempDir(), "sh", "-c", `echo "out $JOURNEY_TEST_VAR"; echo warning >&2`)
	if err != nil {
		t.Fatal(err)
	}
	if out != "out v1\n" {
		t.Errorf("output = %q, want standard output alone", out)
	}
	if !strings.Contains(log.String(), "warning") || !strings.Contains(log.String(), "out v1") {
		t.Errorf("log = %q, want both streams", log.String())
	}
	_, err = ExecRunner(context.Background(), nil, nil, t.TempDir(), "sh", "-c", `echo broken >&2; exit 3`)
	if err == nil || !strings.Contains(err.Error(), "broken") || !strings.Contains(err.Error(), "exit status 3") {
		t.Errorf("a failing command: %v", err)
	}
}
