// SPDX-License-Identifier: MIT

package main

import (
	"flag"
	"os"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// TestSessionPrimaryReadTimeoutFlag_spec_12_3 pins the
// --session-primary-read-timeout flag: it defaults to
// sessionstore.DefaultPrimaryReadTimeout, LENNY_SESSION_PRIMARY_READ_TIMEOUT
// overrides it, and the startup gate rejects a non-positive value.
//
// spec: §12.3 (Postgres HA Requirements)
func TestSessionPrimaryReadTimeoutFlag_spec_12_3(t *testing.T) {
	register := func(t *testing.T) *gatewayFlags {
		t.Helper()
		savedCmdLine, savedArgs := flag.CommandLine, os.Args
		t.Cleanup(func() { flag.CommandLine, os.Args = savedCmdLine, savedArgs })
		flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ContinueOnError)
		os.Args = []string{savedArgs[0]}
		f := &gatewayFlags{}
		f.registerSessionFlags()
		return f
	}
	t.Run("default", func(t *testing.T) {
		t.Setenv("LENNY_SESSION_PRIMARY_READ_TIMEOUT", "")
		f := register(t)
		if got := *f.sessionPrimaryReadTimeout; got != sessionstore.DefaultPrimaryReadTimeout {
			t.Errorf("default = %v, want %v", got, sessionstore.DefaultPrimaryReadTimeout)
		}
	})
	t.Run("environment override", func(t *testing.T) {
		t.Setenv("LENNY_SESSION_PRIMARY_READ_TIMEOUT", "750ms")
		f := register(t)
		if got := *f.sessionPrimaryReadTimeout; got != 750*time.Millisecond {
			t.Errorf("value = %v, want 750ms from the environment", got)
		}
	})
	t.Run("non-positive fails startup", func(t *testing.T) {
		for _, d := range []time.Duration{0, -time.Second} {
			if err := validatePrimaryReadTimeout(d); err == nil {
				t.Errorf("validatePrimaryReadTimeout(%v) = nil, want an error", d)
			}
		}
		if err := validatePrimaryReadTimeout(time.Second); err != nil {
			t.Errorf("validatePrimaryReadTimeout(1s) = %v, want nil", err)
		}
	})
}
