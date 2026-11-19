// SPDX-License-Identifier: MIT

package ratelimit_test

import (
	"flag"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/api/ratelimit"
)

// TestRegisterFlagsDefinesEachFlag checks that RegisterFlags defines every
// flag the part reads, under the name the composition root defined it
// with. The flag guard in cmd/lenny-gateway pins each flag's default and
// help text. The flags are wiring that no requirement states, so the test
// carries no specification annotation.
func TestRegisterFlagsDefinesEachFlag(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	if ratelimit.RegisterFlags(fs) == nil {
		t.Fatal("RegisterFlags returned nil")
	}
	for _, name := range []string{"rate-limit-global-per-min", "rate-limit-per-user-per-min", "rate-limit-per-tenant-per-min", "rate-limit-failopen-max-seconds"} {
		if fs.Lookup(name) == nil {
			t.Errorf("flag %q is not defined", name)
		}
	}
	if err := fs.Parse(nil); err != nil {
		t.Fatal(err)
	}
}
