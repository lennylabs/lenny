// SPDX-License-Identifier: MIT

package journeys

import (
	"fmt"
	"strings"
)

// Outcomes of one trial.
const (
	OutcomePass         = "pass"
	OutcomeFail         = "fail"
	OutcomeInconclusive = "inconclusive"
)

// Outcomes of a journey across its trials. A journey passes only when
// every trial passes, and it is unstable when some trials pass and others
// fail, which points to an ambiguous page or a race.
const (
	JourneyPass         = "pass"
	JourneyFail         = "fail"
	JourneyUnstable     = "unstable"
	JourneyInconclusive = "inconclusive"
)

// Stop says why the agent stopped.
type Stop string

// Reasons the agent stopped.
const (
	// StopFinished means the agent ended its turn and reported a result.
	StopFinished Stop = "finished"
	// StopCostCap means the run reached the per-run cost cap, by the
	// harness's estimate or by Claude Code's own budget flag.
	StopCostCap Stop = "cost-cap"
	// StopTimeLimit means the run reached the scenario's time limit.
	StopTimeLimit Stop = "time-limit"
	// StopAgentError means Claude Code exited without a usable result:
	// it failed to start, lost its connection to the model, or reported
	// an error result. The model service and the login are
	// infrastructure, so the trial is inconclusive.
	StopAgentError Stop = "agent-error"
	// StopInterrupted means the run was cancelled from outside.
	StopInterrupted Stop = "interrupted"
)

// Verdict is the outcome of one trial and the reason for it.
type Verdict struct {
	Outcome string `json:"outcome"`
	Reason  string `json:"reason"`
}

// Decide computes a trial's verdict. infraErr is an infrastructure
// failure outside the agent and the postconditions: the cluster could not
// be created or prepared, or the teardown could not be verified. The
// postconditions decide only when the agent finished and the
// infrastructure held; a run that hit the cost cap or the time limit is
// inconclusive whatever the postconditions show, because the agent did
// not get to finish.
func Decide(infraErr error, stop Stop, stopDetail string, results []PostconditionResult) Verdict {
	if infraErr != nil {
		return Verdict{Outcome: OutcomeInconclusive, Reason: "infrastructure: " + infraErr.Error()}
	}
	switch stop {
	case StopFinished:
	case StopCostCap:
		return Verdict{Outcome: OutcomeInconclusive, Reason: "cost cap reached: " + stopDetail}
	case StopTimeLimit:
		return Verdict{Outcome: OutcomeInconclusive, Reason: "time limit reached: " + stopDetail}
	case StopInterrupted:
		return Verdict{Outcome: OutcomeInconclusive, Reason: "interrupted: " + stopDetail}
	default:
		return Verdict{Outcome: OutcomeInconclusive, Reason: "infrastructure: agent error: " + stopDetail}
	}
	if len(results) == 0 {
		return Verdict{Outcome: OutcomeInconclusive, Reason: "no postcondition was evaluated"}
	}
	var failed []string
	for _, r := range results {
		if !r.Held {
			failed = append(failed, r.Name)
		}
	}
	if len(failed) > 0 {
		return Verdict{Outcome: OutcomeFail, Reason: fmt.Sprintf("postcondition(s) did not hold: %s", strings.Join(failed, ", "))}
	}
	return Verdict{Outcome: OutcomePass, Reason: fmt.Sprintf("all %d postconditions held", len(results))}
}

// Summarize combines a journey's trial outcomes: pass when every trial
// passed, fail when every decided trial failed, unstable when decided
// trials disagree, and inconclusive when no trial was decided.
// Inconclusive trials do not count toward a pass: a journey with two
// passes and one inconclusive trial is inconclusive, because pass^3 needs
// three passes.
func Summarize(outcomes []string) string {
	pass, fail, other := 0, 0, 0
	for _, o := range outcomes {
		switch o {
		case OutcomePass:
			pass++
		case OutcomeFail:
			fail++
		default:
			other++
		}
	}
	switch {
	case pass > 0 && fail > 0:
		return JourneyUnstable
	case fail > 0:
		return JourneyFail
	case pass > 0 && other == 0:
		return JourneyPass
	default:
		return JourneyInconclusive
	}
}
