// SPDX-License-Identifier: MIT

package diagnostics

import (
	"encoding/json"
	"time"
)

// CauseChainEntry is one §25.6 cause-chain level: an ordered diagnostic
// step in the explanation of why a session or pod reached a failure
// state. Level 0 is the proximate cause.
type CauseChainEntry struct {
	Level     int             `json:"level"`
	Category  Category        `json:"category"`
	Summary   string          `json:"summary"`
	Details   json.RawMessage `json:"details,omitempty"`
	Timestamp time.Time       `json:"timestamp,omitempty"`
}

// causeSummary is the §25.6 human-readable summary for each cause
// category, suitable for an agent to pass through to a user.
var causeSummary = map[Category]string{
	CategoryOOMKilled:          "the pod was terminated by the out-of-memory killer",
	CategoryImagePullFailure:   "the pod's container image could not be pulled",
	CategoryResourcePressure:   "the pod could not be scheduled because of node resource pressure",
	CategorySetupCommandFailed: "a workspace setup command exited non-zero",
	CategoryPodCrash:           "the pod exited non-zero",
	CategoryBudgetExpired:      "the session was terminated for budget exhaustion",
	CategoryCredentialFailure:  "the session was terminated by a credential error",
}

// PodFailureChain builds the §25.6 cause chain for a pod failure from
// its signals. It returns a single proximate-cause entry (Level 0), or
// nil when the signals indicate a clean exit. The diagnostic service
// appends deeper levels when it cross-references session state, and
// fills each entry's Timestamp and Details from the data source.
func PodFailureChain(s Signals) []CauseChainEntry {
	category, failed := ClassifyPodFailure(s)
	if !failed {
		return nil
	}
	return []CauseChainEntry{{
		Level:    0,
		Category: category,
		Summary:  causeSummary[category],
	}}
}
