// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

// The defs subcommand is specification tooling rather than a platform
// behavior, so this case carries no specification annotation. It pins that
// the command line hands a defs command to the defs package instead of
// parsing it as a pass's flags.
func TestRunHandsADefsCommandToTheDefsPackage(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	if err := run(context.Background(), []string{"defs", "schemas"}, &out); err != nil {
		t.Fatalf("specshift defs schemas: %v", err)
	}
	if !strings.HasPrefix(out.String(), "# Definition-block schemas\n") {
		t.Errorf("specshift defs schemas printed something other than the schema reference:\n%s", out.String())
	}
	out.Reset()
	if err := run(context.Background(), []string{"defs", "-root", t.TempDir(), "check"}, &out); err != nil {
		t.Errorf("specshift defs check over an empty tree: %v\n%s", err, out.String())
	}
}
