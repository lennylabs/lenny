// SPDX-License-Identifier: MIT

package sessionserver

import (
	"flag"
	"os"
	"time"

	"github.com/lennylabs/lenny/pkg/common/envdefault"
)

// Flags holds the flags only the session server reads.
type Flags struct {
	// PerRuntimePerMinute is the value of --rate-limit-per-runtime-per-min.
	PerRuntimePerMinute *int

	// PerPoolPerMinute is the value of --rate-limit-per-pool-per-min.
	PerPoolPerMinute *int

	// MaxConcurrentSessionsGlobal is the value of
	// --max-concurrent-sessions-global.
	MaxConcurrentSessionsGlobal *int

	// MaxConcurrentSessionsPerUser is the value of
	// --max-concurrent-sessions-per-user.
	MaxConcurrentSessionsPerUser *int

	// MaxConcurrentSessionsPerRuntime is the value of
	// --max-concurrent-sessions-per-runtime.
	MaxConcurrentSessionsPerRuntime *int

	// EvalPerSessionPerMinute is the value of
	// --eval-rate-limit-per-session-per-min.
	EvalPerSessionPerMinute *int

	// EvalPerTenantPerMinute is the value of
	// --eval-rate-limit-per-tenant-per-min.
	EvalPerTenantPerMinute *int

	// EnvVarBlocklist is the value of --env-var-blocklist.
	EnvVarBlocklist *string

	// DefaultIsolationProfile is the value of --default-isolation-profile.
	DefaultIsolationProfile *string

	// ArtifactRetentionSeconds is the value of
	// --session-artifact-retention-seconds.
	ArtifactRetentionSeconds *int

	// PersistDeriveFailureRows is the value of --persist-derive-failure-rows.
	PersistDeriveFailureRows *bool

	// SuspendedBacklogRecheckSeconds is the value of
	// --suspended-backlog-recheck-seconds.
	SuspendedBacklogRecheckSeconds *int
}

// RegisterFlags registers the flags of the session server on fs and returns
// their values, which fs fills when it parses.
func RegisterFlags(fs *flag.FlagSet) *Flags {
	f := &Flags{}
	f.PerRuntimePerMinute = fs.Int("rate-limit-per-runtime-per-min", 0,
		"§11.1 per-runtime session-creation requests-per-minute admission limit. Zero disables the per-runtime rate limit. F-11.1.2.")
	f.PerPoolPerMinute = fs.Int("rate-limit-per-pool-per-min", 0,
		"§11.1 per-pool session-creation requests-per-minute admission limit (skipped when no warm pool resolves). Zero disables the per-pool rate limit. F-11.1.2.")
	f.MaxConcurrentSessionsGlobal = fs.Int("max-concurrent-sessions-global",
		envdefault.Int("LENNY_MAX_CONCURRENT_SESSIONS_GLOBAL", 0),
		"§11.1 global concurrent-session admission cap (non-terminal sessions other than suspended ones across every tenant). A resume of a suspended session is checked against the cap. Zero disables the global scope. Override via LENNY_MAX_CONCURRENT_SESSIONS_GLOBAL. F-11.1.3.")
	f.MaxConcurrentSessionsPerUser = fs.Int("max-concurrent-sessions-per-user",
		envdefault.Int("LENNY_MAX_CONCURRENT_SESSIONS_PER_USER", 0),
		"§11.1 per-user concurrent-session admission cap (non-terminal sessions other than suspended ones a single user holds in their tenant). A resume of a suspended session is checked against the cap. Zero disables the per-user scope. Override via LENNY_MAX_CONCURRENT_SESSIONS_PER_USER. F-11.1.3.")
	f.MaxConcurrentSessionsPerRuntime = fs.Int("max-concurrent-sessions-per-runtime",
		envdefault.Int("LENNY_MAX_CONCURRENT_SESSIONS_PER_RUNTIME", 0),
		"§11.1 per-runtime concurrent-session admission cap (non-terminal sessions other than suspended ones targeting a single runtime in a tenant). A resume of a suspended session is checked against the cap. Zero disables the per-runtime scope. Override via LENNY_MAX_CONCURRENT_SESSIONS_PER_RUNTIME. F-11.1.3.")
	f.EvalPerSessionPerMinute = fs.Int("eval-rate-limit-per-session-per-min",
		envdefault.Int("LENNY_EVAL_RATE_LIMIT_PER_SESSION_PER_MIN", DefaultEvalPerSessionPerMin),
		"§10.7 evalRateLimit.perSessionPerMinute: per-session eval-submission requests-per-minute limit on POST /v1/sessions/{id}/eval. Default 100. Negative disables the per-session scope. Override via LENNY_EVAL_RATE_LIMIT_PER_SESSION_PER_MIN. F-10.7.4.")
	f.EvalPerTenantPerMinute = fs.Int("eval-rate-limit-per-tenant-per-min",
		envdefault.Int("LENNY_EVAL_RATE_LIMIT_PER_TENANT_PER_MIN", DefaultEvalPerTenantPerMin),
		"§10.7 evalRateLimit.perTenantPerMinute: per-tenant eval-submission requests-per-minute limit across all of a tenant's sessions. Default 10000. Negative disables the per-tenant scope. Override via LENNY_EVAL_RATE_LIMIT_PER_TENANT_PER_MIN. F-10.7.4.")
	f.EnvVarBlocklist = fs.String("env-var-blocklist", os.Getenv("LENNY_ENV_VAR_BLOCKLIST"),
		"§14 deployer extension to the env-var blocklist applied to a CreateSessionRequest's `env` field, a comma-separated list of exact names or `*` globs (e.g. AWS_SECRET_ACCESS_KEY,*_TOKEN). The platform default blocklist is always merged in first, so an operator can extend but not reduce it. Override via LENNY_ENV_VAR_BLOCKLIST.")
	f.DefaultIsolationProfile = fs.String("default-isolation-profile", os.Getenv("LENNY_DEFAULT_ISOLATION_PROFILE"),
		"§5.3 isolation profile applied to a session that omits isolationProfile on the create body. Defaults to the chart's compiled-in fallback (`sandboxed`); the e2e overlay sets `standard` so every k6 scenario lands on the warm pool the agent-workload defines.")
	f.ArtifactRetentionSeconds = fs.Int("session-artifact-retention-seconds",
		envdefault.Int("LENNY_SESSION_ARTIFACT_RETENTION_SECONDS", int(DefaultArtifactRetention/time.Second)),
		"§7.1 default artifact-retention window in seconds. Session workspace snapshots, logs, and transcripts stay GC-eligible until this long after the session reaches a terminal state. Default 7 days (604800s); clients extend per-session via POST /v1/sessions/{id}/extend-retention. Override via LENNY_SESSION_ARTIFACT_RETENTION_SECONDS.")
	// gateway.persistDeriveFailureRows defaults to false. When true, a
	// /derive that fails after the workspace copy is attempted persists a
	// terminal failed row with failureClass=derive_failure for audit.
	f.PersistDeriveFailureRows = fs.Bool("persist-derive-failure-rows",
		envdefault.Flag("LENNY_PERSIST_DERIVE_FAILURE_ROWS"),
		"§7.1 derive rule 2 gateway.persistDeriveFailureRows: when true, a POST /v1/sessions/{id}/derive that fails after the workspace copy is attempted persists a terminal failed Session row with failureClass=derive_failure for audit (reachable per §15.1). Default false keeps roll-back-without-persist. Override via LENNY_PERSIST_DERIVE_FAILURE_ROWS.")
	f.SuspendedBacklogRecheckSeconds = fs.Int("suspended-backlog-recheck-seconds",
		envdefault.Int("LENNY_SUSPENDED_BACKLOG_RECHECK_SECONDS", int(DefaultSuspendedBacklogRecheckInterval/time.Second)),
		"§7.2 suspended backlog re-check interval: how often the gateway leader resumes a podless `suspended` session that holds a message. Minimum 30s. Default 300s. Override via LENNY_SUSPENDED_BACKLOG_RECHECK_SECONDS.")
	return f
}
