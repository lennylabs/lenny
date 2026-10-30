// SPDX-License-Identifier: MIT

package supervisedpod

import (
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/adapter"
)

// spec: §4.7.10 (Runtime supervisor, Launch; Liveness)
//
// TestEventTimesReadTheAdapterRecordsInEachSlogLayout pins the adapter log
// reading the cluster tests rely on: a record is found in the default
// logger's, the text handler's, and the JSON handler's layouts, a line that
// only mentions the event inside another field is not a record, and a line
// with no parseable kubelet timestamp is skipped.
func TestEventTimesReadTheAdapterRecordsInEachSlogLayout(t *testing.T) {
	log := strings.Join([]string{
		"2026-10-24T10:00:01.250000000Z 2026/10/24 10:00:01 INFO " + adapter.LogSupervisorReady,
		"2026-10-24T10:00:02.000000000Z time=x level=INFO msg=" + adapter.LogSupervisorReady,
		`2026-10-24T10:00:03.000000000Z {"level":"INFO","msg":"` + adapter.LogSupervisorReady + `"}`,
		"2026-10-24T10:00:04.000000000Z 2026/10/24 10:00:04 WARN other error=" + adapter.LogSupervisorReady + "-ish",
		"not-a-timestamp 2026/10/24 10:00:05 INFO " + adapter.LogSupervisorReady,
		"",
	}, "\n")
	got := eventTimes(log, adapter.LogSupervisorReady)
	if len(got) != 3 {
		t.Fatalf("eventTimes found %d records, want 3: %v", len(got), got)
	}
	if want := time.Date(2026, 10, 24, 10, 0, 1, 250000000, time.UTC); !got[0].Equal(want) {
		t.Errorf("first record at %s, want %s", got[0], want)
	}
	if n := len(eventTimes(log, adapter.LogSuperviseLinkFailure)); n != 0 {
		t.Errorf("eventTimes found %d link-failure records in a log with none", n)
	}
}

// spec: §4.7.10 (Runtime supervisor, Launch), §4.7.9 (step 4)
//
// TestCheckReadyAfterSupervisorRefusesAReadyAheadOfTheConnection pins the
// pre-READY ordering check: a pod with no supervisor ready record fails, a
// Ready in a second before the connection fails, and a Ready in the same
// second as the connection or later passes, because the API server truncates
// the transition time to whole seconds.
func TestCheckReadyAfterSupervisorRefusesAReadyAheadOfTheConnection(t *testing.T) {
	connected := time.Date(2026, 10, 24, 10, 0, 7, 600000000, time.UTC)
	cases := []struct {
		name     string
		readyAt  time.Time
		connects []time.Time
		wantErr  string
	}{
		{"no Ready time", time.Time{}, []time.Time{connected}, "no Ready transition"},
		{"no ready record", connected.Add(time.Second), nil, "records no supervisor ready"},
		{"Ready a second before the connection", connected.Add(-time.Second).Truncate(time.Second), []time.Time{connected}, "before the adapter accepted"},
		{"Ready in the connection's second", connected.Truncate(time.Second), []time.Time{connected}, ""},
		{"Ready after the connection", connected.Add(3 * time.Second).Truncate(time.Second), []time.Time{connected}, ""},
		{"latest record governs", connected.Truncate(time.Second), []time.Time{connected.Add(-time.Minute), connected}, ""},
		{"Ready ahead of the latest record", connected.Add(-30 * time.Second).Truncate(time.Second), []time.Time{connected.Add(-time.Minute), connected}, "before the adapter accepted"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckReadyAfterSupervisor(tc.readyAt, tc.connects)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("CheckReadyAfterSupervisor = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("CheckReadyAfterSupervisor = %v, want an error containing %q", err, tc.wantErr)
			}
		})
	}
}

// spec: §4.6.1, §5.2 (Recycle lifecycle)
//
// TestClaimSeenReadsOnlyTheWatchedPodsClaim pins the claim watch: an ADDED or
// MODIFIED event of the watched claim counts, a DELETED event alone or an
// event of another claim does not, and a truncated trailing event is
// dropped.
func TestClaimSeenReadsOnlyTheWatchedPodsClaim(t *testing.T) {
	ev := func(typ, name string) string {
		return `{"type":"` + typ + `","object":{"metadata":{"name":"` + name + `"}}}`
	}
	cases := []struct {
		name string
		raw  string
		want bool
	}{
		{"no events", "", false},
		{"added", ev("ADDED", "claim-p"), true},
		{"modified", ev("MODIFIED", "claim-p"), true},
		{"deleted only", ev("DELETED", "claim-p"), false},
		{"another claim", ev("ADDED", "claim-q"), false},
		{"truncated trailing event", ev("ADDED", "claim-q") + `{"type":"ADDED","object":{"metad`, false},
		{"added after another", ev("ADDED", "claim-q") + "\n" + ev("ADDED", "claim-p"), true},
	}
	for _, tc := range cases {
		if got := claimSeen(tc.raw, "claim-p"); got != tc.want {
			t.Errorf("%s: claimSeen = %v, want %v", tc.name, got, tc.want)
		}
	}
}
