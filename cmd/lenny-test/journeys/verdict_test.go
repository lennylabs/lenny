// SPDX-License-Identifier: MIT

package journeys

import (
	"errors"
	"strings"
	"testing"
)

// These tests exercise contributor tooling and carry no spec annotation.

func TestDecideLetsThePostconditionsDecideAFinishedRun(t *testing.T) {
	held := []PostconditionResult{{Name: "a", Held: true}, {Name: "b", Held: true}}
	if v := Decide(nil, StopFinished, "success", held); v.Outcome != OutcomePass {
		t.Errorf("all held: %+v", v)
	}
	mixed := []PostconditionResult{{Name: "a", Held: true}, {Name: "b", Held: false}, {Name: "c", Held: false}}
	if v := Decide(nil, StopFinished, "success", mixed); v.Outcome != OutcomeFail || !strings.Contains(v.Reason, "b, c") {
		t.Errorf("two failed: %+v", v)
	}
	if v := Decide(nil, StopFinished, "success", nil); v.Outcome != OutcomeInconclusive {
		t.Errorf("nothing evaluated: %+v", v)
	}
}

func TestDecideMakesUnfinishedRunsInconclusive(t *testing.T) {
	held := []PostconditionResult{{Name: "a", Held: true}}
	cases := []struct {
		stop Stop
		err  error
		want string
	}{
		{StopCostCap, nil, "cost cap"},
		{StopTimeLimit, nil, "time limit"},
		{StopInterrupted, nil, "interrupted"},
		{StopAgentError, nil, "infrastructure: agent error"},
		{StopFinished, errors.New("kind create failed"), "infrastructure: kind create failed"},
	}
	for _, tc := range cases {
		v := Decide(tc.err, tc.stop, "detail", held)
		if v.Outcome != OutcomeInconclusive || !strings.Contains(v.Reason, tc.want) {
			t.Errorf("%s/%v: %+v, want inconclusive with %q even though every postcondition held", tc.stop, tc.err, v, tc.want)
		}
	}
}

func TestSummarizeNeedsEveryTrialToPass(t *testing.T) {
	cases := []struct {
		outcomes []string
		want     string
	}{
		{[]string{"pass", "pass", "pass"}, JourneyPass},
		{[]string{"fail", "fail", "fail"}, JourneyFail},
		{[]string{"pass", "fail", "pass"}, JourneyUnstable},
		{[]string{"pass", "pass", "inconclusive"}, JourneyInconclusive},
		{[]string{"fail", "inconclusive"}, JourneyFail},
		{[]string{"inconclusive"}, JourneyInconclusive},
		{nil, JourneyInconclusive},
	}
	for _, tc := range cases {
		if got := Summarize(tc.outcomes); got != tc.want {
			t.Errorf("Summarize(%v) = %s, want %s", tc.outcomes, got, tc.want)
		}
	}
}
