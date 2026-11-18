// SPDX-License-Identifier: MIT

package sessionevents_test

import (
	"flag"
	"io"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/streamproxy/sessionevents"
)

// TestReplayBufferDepthFlagKeepsItsNameDefaultAndEnvironment checks that
// the replay buffer depth flag registers under its name with the default
// 512, takes its default from LENNY_SESSION_EVENT_REPLAY_BUFFER_DEPTH, and
// parses a value given on the command line.
//
// spec: §10.4 (Gateway Reliability)
func TestReplayBufferDepthFlagKeepsItsNameDefaultAndEnvironment(t *testing.T) {
	fs := flag.NewFlagSet("gateway", flag.ContinueOnError)
	f := sessionevents.RegisterFlags(fs)
	if err := fs.Parse(nil); err != nil {
		t.Fatal(err)
	}
	if *f.ReplayBufferDepth != 512 {
		t.Errorf("default = %d, want 512", *f.ReplayBufferDepth)
	}

	t.Setenv("LENNY_SESSION_EVENT_REPLAY_BUFFER_DEPTH", "1024")
	fs = flag.NewFlagSet("gateway", flag.ContinueOnError)
	f = sessionevents.RegisterFlags(fs)
	if err := fs.Parse(nil); err != nil {
		t.Fatal(err)
	}
	if *f.ReplayBufferDepth != 1024 {
		t.Errorf("environment default = %d, want 1024", *f.ReplayBufferDepth)
	}
	if err := fs.Parse([]string{"-session-event-replay-buffer-depth=2048"}); err != nil {
		t.Fatal(err)
	}
	if *f.ReplayBufferDepth != 2048 {
		t.Errorf("command line = %d, want 2048", *f.ReplayBufferDepth)
	}
}

// TestReplayBufferDepthValidateAcceptsOnlyTheStatedRange checks the bounds
// of the replay buffer depth: 64 and 4096 are accepted, and 63, 4097, zero,
// and a negative depth are refused with the flag's name and the value.
//
// spec: §10.4 (Gateway Reliability)
func TestReplayBufferDepthValidateAcceptsOnlyTheStatedRange(t *testing.T) {
	for _, tc := range []struct {
		depth int
		ok    bool
	}{
		{64, true}, {512, true}, {4096, true},
		{63, false}, {4097, false}, {0, false}, {-1, false},
	} {
		fs := flag.NewFlagSet("gateway", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		f := sessionevents.RegisterFlags(fs)
		*f.ReplayBufferDepth = tc.depth
		err := f.Validate()
		if tc.ok != (err == nil) {
			t.Errorf("Validate(%d) = %v, want ok=%v", tc.depth, err, tc.ok)
		}
		if err != nil && !strings.Contains(err.Error(), "--session-event-replay-buffer-depth must be in [64, 4096]") {
			t.Errorf("Validate(%d) message = %q, want the flag's name and range", tc.depth, err)
		}
	}
}
