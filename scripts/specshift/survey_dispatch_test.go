// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

// The digest, where, and since subcommands are contributor tooling rather
// than a platform behavior, so this case carries no specification
// annotation. It pins that the command line hands each of them to the
// survey package instead of parsing it as a pass's flags.
func TestRunHandsTheSurveyCommandsToTheSurveyPackage(t *testing.T) {
	t.Parallel()
	for _, command := range []string{"digest", "where", "since"} {
		err := run(context.Background(), []string{command}, &bytes.Buffer{})
		if err == nil || !strings.Contains(err.Error(), "usage: specshift "+command) {
			t.Errorf("specshift %s with no argument returned %v; want the survey package's usage error", command, err)
		}
	}
}
