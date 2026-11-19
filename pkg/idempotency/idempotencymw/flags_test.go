// SPDX-License-Identifier: MIT

package idempotencymw_test

import (
	"flag"
	"testing"

	"github.com/lennylabs/lenny/pkg/idempotency/idempotencymw"
)

// TestRegisterFlagsDefinesEachFlag checks that RegisterFlags defines every
// flag the part reads, under the name the composition root defined it
// with. The flag guard in cmd/lenny-gateway pins each flag's default and
// help text. The flags are wiring that no requirement states, so the test
// carries no specification annotation.
func TestRegisterFlagsDefinesEachFlag(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	if idempotencymw.RegisterFlags(fs) == nil {
		t.Fatal("RegisterFlags returned nil")
	}
	for _, name := range []string{"idempotency-max-body-bytes"} {
		if fs.Lookup(name) == nil {
			t.Errorf("flag %q is not defined", name)
		}
	}
	if err := fs.Parse(nil); err != nil {
		t.Fatal(err)
	}
}
