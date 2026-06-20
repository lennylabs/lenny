// SPDX-License-Identifier: MIT

package stack

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWriteReadState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stack.json")
	want := State{
		StartedAt:      time.Now().UTC().Truncate(time.Second),
		SupervisorPID:  111,
		GatewayPID:     222,
		ControllerPID:  333,
		K3sPID:         444,
		HTTPAddr:       "127.0.0.1:8080",
		HTTPSAddr:      "127.0.0.1:8443",
		PostgresDSN:    "postgres://lenny@127.0.0.1:15433/lenny",
		RedisURL:       "redis://127.0.0.1:6390/0",
		KubeconfigPath: "/state/k3s/kubeconfig.yaml",
		K3sEnabled:     true,
	}
	if err := writeState(path, want); err != nil {
		t.Fatalf("writeState: %v", err)
	}
	got, ok, err := readState(path)
	if err != nil {
		t.Fatalf("readState: %v", err)
	}
	if !ok {
		t.Fatal("readState reported no state file")
	}
	if got != want {
		t.Errorf("round-trip mismatch:\n got %+v\nwant %+v", got, want)
	}
}

func TestReadStateMissingFile(t *testing.T) {
	_, ok, err := readState(filepath.Join(t.TempDir(), "absent.json"))
	if err != nil {
		t.Fatalf("readState of a missing file should not error: %v", err)
	}
	if ok {
		t.Error("readState reported ok for a missing file")
	}
}

func TestReadStateCorruptFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stack.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatalf("seed corrupt file: %v", err)
	}
	if _, _, err := readState(path); err == nil {
		t.Error("expected readState to error on a corrupt file")
	}
}

func TestRemoveState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stack.json")
	if err := writeState(path, State{SupervisorPID: 1}); err != nil {
		t.Fatalf("writeState: %v", err)
	}
	if err := removeState(path); err != nil {
		t.Fatalf("removeState: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("state file still present after removeState")
	}
	// removeState is idempotent: removing an absent file is not an
	// error.
	if err := removeState(path); err != nil {
		t.Errorf("second removeState errored: %v", err)
	}
}

// processAlive, the state-file liveness probe, delegates to the
// build-tagged process-control substrate. Its boundary behavior is
// covered cross-platform by TestProcessAliveBoundaries in process_test.go.
