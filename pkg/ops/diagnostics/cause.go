// SPDX-License-Identifier: MIT

// Package diagnostics implements the §25.6 diagnostic cause analysis.
// The DiagnosticService that serves the diagnostic endpoints reads pod
// and session state from Postgres and the Kubernetes API; this file
// holds the pure cause-classification logic it applies to those
// signals, so the classification has one tested implementation
// independent of the data source.
package diagnostics

import "strings"

// Category is a §25.6 CauseChainEntry category.
type Category string

const (
	// CategoryPodCrash is a non-zero pod exit not otherwise explained.
	CategoryPodCrash Category = "POD_CRASH"
	// CategoryResourcePressure is a node-pressure or scheduling failure.
	CategoryResourcePressure Category = "RESOURCE_PRESSURE"
	// CategoryOOMKilled is a pod terminated by the out-of-memory killer.
	CategoryOOMKilled Category = "OOM_KILLED"
	// CategoryImagePullFailure is a failure to pull the pod's image.
	CategoryImagePullFailure Category = "IMAGE_PULL_FAILURE"
	// CategorySetupCommandFailed is a non-zero exit during workspace
	// setup-command execution.
	CategorySetupCommandFailed Category = "SETUP_COMMAND_FAILED"
	// CategoryBudgetExpired is a session terminated for budget
	// exhaustion. It is derived from session state, not pod signals.
	CategoryBudgetExpired Category = "BUDGET_EXPIRED"
	// CategoryCredentialFailure is a session terminated by a credential
	// error. It is derived from session state, not pod signals.
	CategoryCredentialFailure Category = "CREDENTIAL_FAILURE"
)

// Signals are the pod-failure signals the §25.6 cause chain
// cross-references: the exit code, the OOM-killed flag, whether the
// failure occurred during workspace setup, and the Kubernetes-event
// indications of an image-pull failure or node resource pressure.
// These fields are available from both Postgres (agent_pod_state) and
// the Kubernetes API, so the classification works under either source.
type Signals struct {
	ExitCode         int
	OOMKilled        bool
	InSetupPhase     bool
	ImagePullError   bool
	ResourcePressure bool
}

// ClassifyPodFailure determines the §25.6 cause category of a pod
// failure from its signals. ok is false when the signals indicate no
// failure (a clean exit). A pre-start failure (image pull, resource
// pressure) and an OOM kill take precedence over exit-code analysis,
// since a pod that never started has no meaningful exit code; a
// non-zero exit during setup is a setup-command failure, and any other
// non-zero exit is a generic pod crash.
func ClassifyPodFailure(s Signals) (Category, bool) {
	switch {
	case s.OOMKilled:
		return CategoryOOMKilled, true
	case s.ImagePullError:
		return CategoryImagePullFailure, true
	case s.ResourcePressure:
		return CategoryResourcePressure, true
	case s.InSetupPhase && s.ExitCode != 0:
		return CategorySetupCommandFailed, true
	case s.ExitCode != 0:
		return CategoryPodCrash, true
	default:
		return "", false
	}
}

// SessionStateCause maps a session's §7.3 terminal-failure fields
// (sessions.failure_class / failure_reason) onto the two §25.6 cause
// categories that are derived from session state rather than pod
// signals: a budget exhaustion and a credential failure. ok is false
// when the failure is not one of these (a clean session, or a failure a
// pod signal already explains). The match is substring-insensitive over
// both fields because the §7.3 classifier names budget failures with a
// `budget`-rooted reason (`budget_exceeded`, `delegation_budget_exceeded`)
// and credential failures with a `credential`-rooted class/reason
// (`credential_error`, `credential_pool_exhausted`). spec: §25.6 line
// 2890, §7.3 failure classification. F-25.6.6.
func SessionStateCause(failureClass, failureReason string) (Category, bool) {
	hay := strings.ToLower(failureClass + " " + failureReason)
	switch {
	case strings.Contains(hay, "budget"):
		return CategoryBudgetExpired, true
	case strings.Contains(hay, "credential"):
		return CategoryCredentialFailure, true
	default:
		return "", false
	}
}
