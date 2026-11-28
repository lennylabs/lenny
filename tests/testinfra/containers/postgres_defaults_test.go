// SPDX-License-Identifier: MIT

package containers

import (
	"testing"
	"time"
)

// The tests in this file verify a test-fixture default, which implements no
// specification requirement, so they carry no spec annotation. Change 0181,
// decision 1, sets the default (GATE-I16).

// TestPostgresStartupTimeoutDefaultsToSixtySeconds pins the start timeout
// that StartPostgres and SharedPostgres use when the caller sets none: 60
// seconds, the default of the MinIO and fake GCS fixtures.
func TestPostgresStartupTimeoutDefaultsToSixtySeconds(t *testing.T) {
	t.Parallel()
	got := PostgresOptions{}.withDefaults().StartupTimeout
	if got != 60*time.Second {
		t.Fatalf("default StartupTimeout = %s, want 60s", got)
	}
}

// TestPostgresStartupTimeoutKeepsTheCallersValue checks that a timeout the
// caller sets is not replaced by the default.
func TestPostgresStartupTimeoutKeepsTheCallersValue(t *testing.T) {
	t.Parallel()
	for _, want := range []time.Duration{time.Second, 30 * time.Second, 5 * time.Minute} {
		if got := (PostgresOptions{StartupTimeout: want}).withDefaults().StartupTimeout; got != want {
			t.Errorf("StartupTimeout %s became %s", want, got)
		}
	}
}
