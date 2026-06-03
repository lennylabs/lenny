// SPDX-License-Identifier: MIT

package runtimescaffold

import (
	"context"
	"sort"

	"github.com/lennylabs/lenny/pkg/compliance"
)

// probe.go implements the §15.4.6 declared-vs-observed level
// reconciliation that `lenny runtime validate` performs against a
// locally-built adapter binary. The conformance battery itself lives in
// cmd/lenny-compliance; this file drives it (via pkg/compliance.RunSuite)
// and derives the observed integration level from which gating checks
// passed.

// Status names the §15.4.6 reconciliation outcome.
const (
	// StatusMatch — observed level equals the declared level.
	StatusMatch = "match"
	// StatusUnderdeclared — observed level exceeds the declared level
	// (the author under-declared). Exit 0 with a WARN.
	StatusUnderdeclared = "underdeclared"
	// StatusUnderperforms — observed level is below the declared level
	// (the runtime does not meet its published claim). Exit non-zero with
	// the runtime_level_underperforms structured error.
	StatusUnderperforms = "underperforms"
)

// ObservedResult is the outcome of the §15.4.6 declared-vs-observed
// probe. Report carries the full conformance battery (the same document
// cmd/lenny-compliance --json emits).
type ObservedResult struct {
	Declared string            `json:"declared"`
	Observed string            `json:"observed"`
	Status   string            `json:"status"`
	Missing  []string          `json:"missing,omitempty"`
	Report   compliance.Report `json:"-"`
}

// checkLevel maps each conformance check name to the §15.4.6 integration
// level whose category it belongs to. The full battery runs every check;
// this map lets the validator decide which failures count against a
// runtime that only declares a lower level.
var checkLevel = map[string]compliance.Level{
	// Basic categories (§15.4.6).
	"binary_exists_and_executes":     compliance.LevelBasic,
	"empty_stdin_exits_cleanly":      compliance.LevelBasic,
	"message_emits_response":         compliance.LevelBasic,
	"heartbeat_emits_ack":            compliance.LevelBasic,
	"unknown_type_ignored":           compliance.LevelBasic,
	"shutdown_exits_within_deadline": compliance.LevelBasic,
	"sequential_messages_handled":    compliance.LevelBasic,
	// Standard categories (§15.4.6).
	"mcp_nonce_handshake":               compliance.LevelStandard,
	"platform_mcp_tool_invocation":      compliance.LevelStandard,
	"connector_mcp_server_reachability": compliance.LevelStandard,
	"tool_call_tool_result_correlation": compliance.LevelStandard,
	// Full categories (§15.4.6).
	"lifecycle_channel_opening":         compliance.LevelFull,
	"checkpoint_quiesce_resume":         compliance.LevelFull,
	"interrupt_acknowledgement":         compliance.LevelFull,
	"credential_rotation_no_disruption": compliance.LevelFull,
	"deadline_signal_handling":          compliance.LevelFull,
}

// levelRank orders the integration levels so observed and declared can be
// compared. An unknown level ranks 0.
func levelRank(l compliance.Level) int {
	switch l {
	case compliance.LevelBasic:
		return 1
	case compliance.LevelStandard:
		return 2
	case compliance.LevelFull:
		return 3
	default:
		return 0
	}
}

// deriveObservedLevel implements the §15.4.6 observed-level algorithm:
// the full fixture set is available, so the runtime's observed level is
// the highest gate it cleared — lifecycle handshake → full, else MCP
// nonce handshake → standard, else basic.
//
// spec: §15.4.6 "Declared vs. observed level reconciliation" steps 1-4.
func deriveObservedLevel(checks []compliance.Check) compliance.Level {
	pass := map[string]bool{}
	for _, c := range checks {
		pass[c.Name] = c.Pass
	}
	if pass["lifecycle_channel_opening"] {
		return compliance.LevelFull
	}
	if pass["mcp_nonce_handshake"] {
		return compliance.LevelStandard
	}
	return compliance.LevelBasic
}

// reconcileStatus compares observed against declared per §15.4.6.
func reconcileStatus(declared, observed compliance.Level) string {
	switch {
	case levelRank(observed) > levelRank(declared):
		return StatusUnderdeclared
	case levelRank(observed) < levelRank(declared):
		return StatusUnderperforms
	default:
		return StatusMatch
	}
}

// missingCapabilities lists the conformance checks the runtime did not
// pass at the levels between its observed level (exclusive) and its
// declared level (inclusive). These are the capabilities the runtime
// claims but does not exercise — the `missing` list in the §15.4.6
// runtime_level_underperforms error.
func missingCapabilities(checks []compliance.Check, observed, declared compliance.Level) []string {
	var missing []string
	for _, c := range checks {
		lvl, known := checkLevel[c.Name]
		if !known {
			continue
		}
		if levelRank(lvl) > levelRank(observed) && levelRank(lvl) <= levelRank(declared) && !c.Pass {
			missing = append(missing, c.Name)
		}
	}
	sort.Strings(missing)
	return missing
}

// failuresAtOrBelow returns the names of failing conformance checks whose
// category level is at or below declared. A non-empty result means the
// runtime failed a category it is responsible for, so `lenny runtime
// validate` exits non-zero per §15.4.6 ("exits 0 on a full pass and
// non-zero with a structured failure report otherwise").
func failuresAtOrBelow(checks []compliance.Check, declared compliance.Level) []string {
	var failed []string
	for _, c := range checks {
		lvl, known := checkLevel[c.Name]
		if !known {
			continue
		}
		if levelRank(lvl) <= levelRank(declared) && !c.Pass {
			failed = append(failed, c.Name)
		}
	}
	sort.Strings(failed)
	return failed
}

// probeObservedLevel runs the full conformance battery against the
// locally-built adapter binary and reconciles the declared level against
// the observed level per §15.4.6. The battery always runs at the Full
// level (every category is exercised) so the observed level can be
// derived regardless of what the runtime declares.
//
// It returns compliance.ErrHarnessNotFound (wrapped) when lenny-compliance
// cannot be located, so the caller can degrade to a static-only report
// rather than failing the runtime.
func probeObservedLevel(ctx context.Context, binaryPath, declared, harnessPath string) (ObservedResult, error) {
	adapter := compliance.NewAdapter(binaryPath, compliance.LevelFull)
	report, err := compliance.RunSuite(ctx, adapter, compliance.Options{HarnessPath: harnessPath})
	if err != nil {
		return ObservedResult{}, err
	}
	observed := deriveObservedLevel(report.Checks)
	res := ObservedResult{
		Declared: declared,
		Observed: string(observed),
		Status:   reconcileStatus(compliance.Level(declared), observed),
		Report:   report,
	}
	if res.Status == StatusUnderperforms {
		res.Missing = missingCapabilities(report.Checks, observed, compliance.Level(declared))
	}
	return res, nil
}
