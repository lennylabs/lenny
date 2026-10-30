// SPDX-License-Identifier: MIT

package supervisedpod

import (
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/adapter"
	"github.com/lennylabs/lenny/tests/testinfra/superviseprobe"
)

// spec: §4.7.10 (Runtime supervisor, Liveness)
//
// diagnosis: the supervisor-survives-signals check passed a supervisor that
// stopped answering ping. On an idle keep pod a missed pong leaves PID 1
// running and the pod Ready, so only the adapter's CH-SUPERVISE link-failure
// and not-live records reveal it; a check that does not read them, or reads
// the wrong event names, passes that supervisor here.
func TestCheckSupervisorStillLiveFailsOnAMissedPongWithPID1Running(t *testing.T) {
	running := []superviseprobe.Process{{PID: 1, State: "S"}, {PID: 7, PPID: 1, State: "S"}}
	at := []time.Time{time.Date(2026, 10, 24, 10, 0, 0, 0, time.UTC)}
	cases := []struct {
		name    string
		procs   []superviseprobe.Process
		events  map[string][]time.Time
		wantErr string
	}{
		{"live supervisor, no records", running, nil, ""},
		{"live supervisor, unrelated record", running, map[string][]time.Time{adapter.LogSupervisorReady: at}, ""},
		{"missed pong: link failure logged", running, map[string][]time.Time{adapter.LogSuperviseLinkFailure: at}, adapter.LogSuperviseLinkFailure},
		{"missed pong: not-live logged", running, map[string][]time.Time{adapter.LogRuntimeNotLive: at}, adapter.LogRuntimeNotLive},
		{"PID 1 stopped", []superviseprobe.Process{{PID: 1, State: "T"}}, nil, "state T"},
		{"PID 1 trace-stopped", []superviseprobe.Process{{PID: 1, State: "t"}}, nil, "state t"},
		{"PID 1 zombie", []superviseprobe.Process{{PID: 1, State: "Z"}}, nil, "state Z"},
		{"no PID 1", []superviseprobe.Process{{PID: 7, State: "S"}}, nil, "no PID 1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckSupervisorStillLive(tc.procs, tc.events)
			switch {
			case tc.wantErr == "" && err != nil:
				t.Fatalf("CheckSupervisorStillLive = %v, want nil", err)
			case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
				t.Fatalf("CheckSupervisorStillLive = %v, want an error naming %q", err, tc.wantErr)
			}
		})
	}
}
