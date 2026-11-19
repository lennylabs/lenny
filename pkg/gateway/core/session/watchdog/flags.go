// SPDX-License-Identifier: MIT

package watchdog

import (
	"flag"

	"github.com/lennylabs/lenny/pkg/common/envdefault"
)

// Flags holds the flags only the session watchdog reads.
type Flags struct {
	// MaxCreatedStateSeconds is the value of
	// --max-created-state-timeout-seconds.
	MaxCreatedStateSeconds *int

	// MaxResumingSeconds is the value of --max-resuming-seconds.
	MaxResumingSeconds *int

	// MaxReadyStateSeconds is the value of --max-ready-state-timeout-seconds.
	MaxReadyStateSeconds *int

	// MaxStartingStateSeconds is the value of
	// --max-starting-state-timeout-seconds.
	MaxStartingStateSeconds *int

	// MaxSessionAgeSeconds is the value of --max-session-age-seconds.
	MaxSessionAgeSeconds *int

	// MaxAwaitingClientActionSeconds is the value of
	// --max-awaiting-client-action-seconds.
	MaxAwaitingClientActionSeconds *int

	// MaxSuspendedPodHoldSeconds is the value of
	// --max-suspended-pod-hold-seconds.
	MaxSuspendedPodHoldSeconds *int

	// MaxSuspendedSessionSeconds is the value of
	// --max-suspended-session-seconds.
	MaxSuspendedSessionSeconds *int

	// MaxClientIdleSeconds is the value of --max-client-idle-seconds.
	MaxClientIdleSeconds *int

	// ExpiryWarningSeconds is the value of --session-expiry-warning-seconds.
	ExpiryWarningSeconds *int
}

// RegisterFlags registers the flags of the session watchdog on fs and returns
// their values, which fs fills when it parses.
func RegisterFlags(fs *flag.FlagSet) *Flags {
	f := &Flags{}
	f.MaxCreatedStateSeconds = fs.Int("max-created-state-timeout-seconds",
		envdefault.Int("LENNY_MAX_CREATED_STATE_TIMEOUT_SECONDS", DefaultMaxCreatedStateSeconds),
		"§7.1 maxCreatedStateTimeoutSeconds: the deadline on the `created` pre-running state. Threaded uniformly into the §7.1 uploadToken TTL and the watchdog's `created`-state budget so the two deadlines never drift. Default 300s. Override via LENNY_MAX_CREATED_STATE_TIMEOUT_SECONDS.")
	f.MaxResumingSeconds = fs.Int("max-resuming-seconds",
		envdefault.Int("LENNY_MAX_RESUMING_SECONDS", DefaultMaxResumingSeconds),
		"§6.2 watchdog on resuming: a session whose resume has not completed within this window branches on retry budget (retries remain → resume_pending; exhausted → awaiting_client_action). Default 300s, matching the setup-command total timeout. Override via LENNY_MAX_RESUMING_SECONDS. The controller's --claim-orphan-timeout, and the gateway's --slot-reaper-terminal-age, must be at least one second more than the largest of this value, --max-finalizing-state-timeout-seconds, and the §7.1 terminal pipeline budget, which is --workspace-seal-max-duration-seconds plus --stream-failure-report-timeout (the §5.2 post-terminal slot activity invariant), which the gateway checks at startup.")
	f.MaxReadyStateSeconds = fs.Int("max-ready-state-timeout-seconds",
		envdefault.Int("LENNY_MAX_READY_STATE_TIMEOUT_SECONDS", DefaultMaxReadyStateSeconds),
		"§11.3 maxReadyTimeoutSeconds: the deadline on the `ready` pre-running state. A session stuck longer than this is transitioned to `failed`. Default 300s. Override via LENNY_MAX_READY_STATE_TIMEOUT_SECONDS.")
	f.MaxStartingStateSeconds = fs.Int("max-starting-state-timeout-seconds",
		envdefault.Int("LENNY_MAX_STARTING_STATE_TIMEOUT_SECONDS", DefaultMaxStartingStateSeconds),
		"§11.3 maxStartingTimeoutSeconds: the deadline on the `starting` pre-running state. A session stuck longer than this is transitioned to `failed`. Default 120s. Override via LENNY_MAX_STARTING_STATE_TIMEOUT_SECONDS.")
	f.MaxSessionAgeSeconds = fs.Int("max-session-age-seconds",
		envdefault.Int("LENNY_MAX_SESSION_AGE_SECONDS", DefaultMaxSessionAgeSeconds),
		"§11.3 / §6.2 platform-wide maxSessionAgeSeconds: the active-time session age cap. Age counts only while the session is starting, running, or input_required, and is paused while it is suspended, resume_pending, resuming, or awaiting_client_action; no age cap expires a suspended session. The per-runtime `runtime.maxSessionAgeSeconds` (and per-session retryPolicy.maxSessionAgeSeconds) tighten this cap; this flag is the floor a runtime without an override inherits. Default 7200s (2h). Override via LENNY_MAX_SESSION_AGE_SECONDS.")
	f.MaxAwaitingClientActionSeconds = fs.Int("max-awaiting-client-action-seconds",
		envdefault.Int("LENNY_MAX_AWAITING_CLIENT_ACTION_SECONDS", DefaultMaxAwaitingClientActionSeconds),
		"§11.3 maxAwaitingClientActionSeconds: the deadline on the `awaiting_client_action` state. A session that has waited this long for client action is transitioned to `expired`. Default 900s. Override via LENNY_MAX_AWAITING_CLIENT_ACTION_SECONDS.")
	f.MaxSuspendedPodHoldSeconds = fs.Int("max-suspended-pod-hold-seconds",
		envdefault.Int("LENNY_MAX_SUSPENDED_POD_HOLD_SECONDS", DefaultMaxSuspendedPodHoldSeconds),
		"§6.2 / §11.3 maxSuspendedPodHoldSeconds: the wall-clock window an interrupt-suspended session holds its pod. When it ends, the gateway checkpoints the workspace and releases the pod, and the session stays `suspended`. This deploy-wide flag is the only hold source; no per-tenant hold cap applies. Default 900s. Override via LENNY_MAX_SUSPENDED_POD_HOLD_SECONDS.")
	f.MaxSuspendedSessionSeconds = fs.Int("max-suspended-session-seconds",
		envdefault.Int("LENNY_MAX_SUSPENDED_SESSION_SECONDS", DefaultMaxSuspendedSessionSeconds),
		"§6.2 maxSuspendedSessionSeconds: the wall-clock lifetime of a `suspended` session. A session suspended this long, with or without its pod, is transitioned to `expired`. Default 604800s (7 days). Override via LENNY_MAX_SUSPENDED_SESSION_SECONDS.")
	f.MaxClientIdleSeconds = fs.Int("max-client-idle-seconds",
		envdefault.Int("LENNY_MAX_CLIENT_IDLE_SECONDS", DefaultMaxIdleSeconds),
		"§6.2 maxClientIdleSeconds: the platform-default idle bound on a `running` session. A session with no qualifying activity for longer than this is suspended: the gateway seals it on the adapter, checkpoints the workspace, and releases the pod, and the session stays `suspended` until a message or a resume. A session that cannot be suspended (service mode or an MCP runtime) is transitioned to `expired` with reason expired:idle instead. The per-pool `sessionPolicy.maxClientIdleSeconds` and the §27.6 playground idle override tighten this default. Default 900s. Override via LENNY_MAX_CLIENT_IDLE_SECONDS.")
	f.ExpiryWarningSeconds = fs.Int("session-expiry-warning-seconds",
		envdefault.Int("LENNY_SESSION_EXPIRY_WARNING_SECONDS", DefaultExpiryWarningSeconds),
		"§11.3 session-expiry warning lead time: the gateway sends a `session_expiring_soon` SSE event to the client and a `DEADLINE_APPROACHING` CH-RUNTIMEOPS signal to the pod this many seconds before a session's effective maxSessionAge deadline so the agent can checkpoint and the client can extend or wrap up. Default 300s (5 minutes). Override via LENNY_SESSION_EXPIRY_WARNING_SECONDS.")
	return f
}
