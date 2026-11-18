// SPDX-License-Identifier: MIT

package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/cmd/lenny-test/journeys"
)

// These tests exercise the journeys tier's command-line wiring, which is
// contributor tooling, and carry no spec annotation.

func TestIsJourneysTierRecognizesTheTierFlag(t *testing.T) {
	yes := [][]string{
		{"--tier", "journeys"},
		{"--tier=journeys", "--trials", "1"},
		{"--journey", "x", "-tier", "journeys"},
	}
	for _, args := range yes {
		if !isJourneysTier(args) {
			t.Errorf("%v selects the journeys tier", args)
		}
	}
	no := [][]string{{"--tier", "unit"}, {"--tier"}, {"--group", "journeys"}, {}}
	for _, args := range no {
		if isJourneysTier(args) {
			t.Errorf("%v does not select the journeys tier", args)
		}
	}
	for _, tier := range allTiers() {
		if tier == tierJourneys {
			t.Error("the journeys tier is in allTiers, so a gate bound or a --tier list would reach a tier that spends money")
		}
	}
}

func TestParseJourneysFlagsDefaultsAndRefusals(t *testing.T) {
	f, err := parseJourneysFlags([]string{"--tier", "journeys"})
	if err != nil {
		t.Fatal(err)
	}
	if f.trials != journeys.DefaultTrials || f.runCap != 6 || f.pilotCap != 54 || f.model != "claude-sonnet-5-5" || f.pilot != "pilot" {
		t.Errorf("defaults = %+v", f)
	}
	state, err := journeys.DefaultStateDir()
	if err != nil {
		t.Fatal(err)
	}
	if f.stateDir != state || f.tokenFile != state+"/claude-token" {
		t.Errorf("the lock, ledger, and token default to %q and %q, want the host-wide %q", f.stateDir, f.tokenFile, state)
	}
	f, err = parseJourneysFlags([]string{"--tier", "journeys", "--results-dir", "/tmp/elsewhere"})
	if err != nil || f.stateDir != state {
		t.Errorf("--results-dir moved the state directory to %q (%v)", f.stateDir, err)
	}
	f, err = parseJourneysFlags([]string{"--tier", "journeys", "--state-dir", "/s", "--claude-token-file", "/t"})
	if err != nil || f.stateDir != "/s" || f.tokenFile != "/t" {
		t.Errorf("explicit state and token = %+v, %v", f, err)
	}
	f, err = parseJourneysFlags([]string{"--tier=journeys", "--journey", "operator-install,end-user-first-session", "--trials", "1", "--skip-agent"})
	if err != nil || f.journeys != "operator-install,end-user-first-session" || f.trials != 1 || !f.skipAgent {
		t.Errorf("parsed = %+v, %v", f, err)
	}
	for _, bad := range [][]string{
		{"--trials", "0"},
		{"--run-cap-usd", "0"},
		{"--run-cap-usd", "60"},
		{"stray"},
		{"--no-such-flag"},
	} {
		if _, err := parseJourneysFlags(bad); err == nil {
			t.Errorf("%v was accepted", bad)
		}
	}
}

func TestJourneysExitCodeReportsTheWorstJourney(t *testing.T) {
	sum := func(verdicts ...string) *journeys.RunSummary {
		s := &journeys.RunSummary{}
		for _, v := range verdicts {
			s.Journeys = append(s.Journeys, journeys.JourneySummary{Verdict: v})
		}
		return s
	}
	cases := []struct {
		s    *journeys.RunSummary
		err  error
		want int
	}{
		{sum(journeys.JourneyPass, journeys.JourneyPass), nil, 0},
		{sum(journeys.JourneyPass, journeys.JourneyInconclusive), nil, 2},
		{sum(journeys.JourneyInconclusive, journeys.JourneyUnstable), nil, 1},
		{sum(journeys.JourneyFail), nil, 1},
		{sum(journeys.JourneyPass), errors.New("stopped"), 2},
		{nil, nil, 2},
	}
	for i, tc := range cases {
		if got := journeysExitCode(tc.s, tc.err); got != tc.want {
			t.Errorf("case %d: exit %d, want %d", i, got, tc.want)
		}
	}
}

func TestShellQuoteQuotesWhatTheShellWouldSplit(t *testing.T) {
	got := shellQuote([]string{"claude", "-p", "it's here", "--tools", "Bash,Read", ""})
	want := `claude -p 'it'\''s here' --tools Bash,Read ''`
	if got != want {
		t.Fatalf("shellQuote = %s, want %s", got, want)
	}
	if !strings.HasPrefix(absUnder("/repo", "tests/journeys"), "/repo/") || absUnder("/repo", "/abs") != "/abs" {
		t.Error("absUnder does not resolve relative paths under the root")
	}
}
