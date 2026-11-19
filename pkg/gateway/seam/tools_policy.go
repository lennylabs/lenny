// SPDX-License-Identifier: MIT

package seam

import (
	"context"
	"errors"
	"time"
)

// The interfaces below are served to the platform tools seam by the session
// core, and CallerIdentity by the API front: the confined packages the
// seam's tools read (the interceptor chain, the caller check, the VCS
// credential pools, the tenant store, environment access, and the rate
// limiter) stay with the part that holds them, and the seam reaches them
// through these operations.

// CallerIdentity is served by the API front to the platform tools seam.
type CallerIdentity interface {
	// RequestCaller is GWSEAM-RequestCaller. ok is false when the context
	// carries no authenticated caller.
	RequestCaller(ctx context.Context) (c Caller, ok bool)
}

// Caller is the authenticated caller a request's context carries.
type Caller struct {
	Subject  string
	TenantID string
	// SessionID is the session a session-scoped caller, such as a pod's
	// runtime, acts for. It is empty for a client caller.
	SessionID string
	// CallerType names the kind of caller, such as "agent" for a platform
	// tool call from a session's pod.
	CallerType string
	// Groups is the caller's group set, which environment membership
	// reads.
	Groups []string
}

// CallerChecks is served by the session core to the platform tools seam.
type CallerChecks interface {
	// CheckCreatingCaller is GWSEAM-CheckCreatingCaller. It checks the
	// bearer the context's caller presented against the issued-token
	// record, at the given time, before the caller creates a session. It
	// returns nil, a *CallerRefusal once the refusal's audit row is
	// durable, or a transient error.
	CheckCreatingCaller(ctx context.Context, at time.Time) error
}

// CallerRefusal reports a caller whose bearer may not create sessions,
// with the reason. It matches ErrCallerRefused under errors.Is.
type CallerRefusal struct {
	Reason string
}

func (e *CallerRefusal) Error() string { return ErrCallerRefused.Error() + ": " + e.Reason }

// Unwrap returns ErrCallerRefused.
func (e *CallerRefusal) Unwrap() error { return ErrCallerRefused }

// InterceptPhase names a phase of the interceptor chain that the platform
// tools seam runs.
type InterceptPhase string

// The phases the platform tools seam runs.
const (
	InterceptPreRoute           InterceptPhase = "PreRoute"
	InterceptPreDelegation      InterceptPhase = "PreDelegation"
	InterceptPreMessageDelivery InterceptPhase = "PreMessageDelivery"
	InterceptPreToolResult      InterceptPhase = "PreToolResult"
	InterceptPostAgentOutput    InterceptPhase = "PostAgentOutput"
)

// The client API error codes an interception result or a content policy
// refusal carries that the platform tools seam branches on or reports.
const (
	// CodeInterceptorImmutableFieldViolation is the code of a result whose
	// modification altered a field the phase holds immutable; the result
	// names the fields.
	CodeInterceptorImmutableFieldViolation = "INTERCEPTOR_IMMUTABLE_FIELD_VIOLATION"
	// CodeInputTooLarge is the code of an input larger than the content
	// policy's maximum input size.
	CodeInputTooLarge = "INPUT_TOO_LARGE"
)

// Interception is served by the session core to the platform tools seam.
type Interception interface {
	// Intercept is GWSEAM-Intercept. On a rejection it records the
	// interceptor rejection audit event for the request's tenant, session,
	// and phase before it returns.
	Intercept(ctx context.Context, req InterceptRequest) InterceptResult

	// Intercepts is GWSEAM-Intercepts.
	Intercepts(phase InterceptPhase) bool
}

// InterceptRequest is one run of the interceptor chain at a phase.
type InterceptRequest struct {
	Phase     InterceptPhase
	TenantID  string
	SessionID string
	Content   []byte
	Metadata  map[string]string
	// PolicyScoped runs, of the external interceptors, only the one the
	// content policy names in InterceptorRef, and none when InterceptorRef
	// is empty. When false, every registered interceptor runs.
	PolicyScoped   bool
	InterceptorRef string
}

// InterceptAction is the outcome of an interception.
type InterceptAction int

// The interception outcomes.
const (
	// InterceptAllow passes the content through unchanged.
	InterceptAllow InterceptAction = iota + 1
	// InterceptReject refuses the content.
	InterceptReject
	// InterceptModify replaces the content with ModifiedContent.
	InterceptModify
)

// InterceptResult is the result of GWSEAM-Intercept.
type InterceptResult struct {
	Action InterceptAction
	Reason string
	// Code is the client API error code a rejection names, or empty.
	Code            string
	ModifiedContent []byte
	// RejectedBy names the interceptor that rejected.
	RejectedBy string
	// ViolatedFields names the immutable fields a modification altered,
	// when Code is CodeInterceptorImmutableFieldViolation.
	ViolatedFields []string
}

// InterceptorCooldowns is served by the session core to the platform tools
// seam.
type InterceptorCooldowns interface {
	// FailOpenCooldown is GWSEAM-FailOpenCooldown. ok is false when the
	// interceptor is unknown or is not inside a fail-open weakening
	// cooldown.
	FailOpenCooldown(ctx context.Context, interceptorRef string) (transitionAt time.Time, cooldownSeconds int, ok bool)
}

// ExportScans is served by the session core to the platform tools seam.
type ExportScans interface {
	// ResolveExportScan is GWSEAM-ResolveExportScan. It returns the export
	// scan of the interceptor a content policy names, or
	// ErrExportScanUnavailable.
	ResolveExportScan(ctx context.Context, tenantID, interceptorRef string) (ExportScan, error)
}

// ExportScan is the per-file export scan of one interceptor, which
// GWSEAM-ResolveExportScan returns.
type ExportScan interface {
	// ScanExportFiles is GWSEAM-ScanExportFiles. It returns the files as
	// the scan left them, each with the digest of its final content, or
	// the error that refused the first file the scan refused, which the
	// caller reports as it came.
	ScanExportFiles(ctx context.Context, target ExportScanTarget, files []ExportFile) ([]ExportFile, error)
}

// ExportScanTarget names the delegation an export scan runs for: the child
// session that receives the files, the content policy, the pool, and the
// largest file the policy admits, which is zero when it sets none.
type ExportScanTarget struct {
	TenantID    string
	SessionID   string
	PolicyName  string
	Pool        string
	MaxFileSize int64
}

// ExportFile is one exported file, by its path in the child's workspace.
type ExportFile struct {
	Path    string
	Content []byte
	// SHA256 is the digest of Content that the scan stamps.
	SHA256 string
}

// TenantFacts is served by the session core to the platform tools seam.
type TenantFacts interface {
	// TenantElicitationIntegrity is GWSEAM-TenantElicitationIntegrity. It
	// returns the tenant's stored elicitation content integrity mode,
	// empty when the tenant set none.
	TenantElicitationIntegrity(ctx context.Context, tenantID string) (string, error)

	// TenantCredentialProviders is GWSEAM-TenantCredentialProviders.
	TenantCredentialProviders(ctx context.Context, tenantID string) ([]string, error)

	// DelegatorRefusal is GWSEAM-DelegatorRefusal. It returns the refusal
	// the creation fence answers for a delegated child of the user in the
	// tenant, or nil when the fence admits it.
	DelegatorRefusal(ctx context.Context, tenantID, userID string) (*Refusal, error)
}

// TenantDirectory is served by the session core to the platform tools
// seam's sweeps.
type TenantDirectory interface {
	// ListTenants is GWSEAM-ListTenants.
	ListTenants(ctx context.Context) ([]string, error)
}

// EnvironmentResolver is served by the session core to the platform tools
// seam.
type EnvironmentResolver interface {
	// ResolveEnvironments is GWSEAM-ResolveEnvironments. It returns ctx
	// carrying the environment resolution of the caller the context
	// carries, in the form the shared environment conventions read, or ctx
	// unchanged when the context carries no caller.
	ResolveEnvironments(ctx context.Context) (context.Context, error)
}

// VCSTokens is served by the session core to the platform tools seam.
type VCSTokens interface {
	// IssueVCSToken is GWSEAM-IssueVCSToken. It returns a zero token when
	// no credential pool issued one, a *VCSTokenError when the host cannot
	// be served, or a transient error.
	IssueVCSToken(ctx context.Context, tenantID, gitURL, leaseScope string) (VCSToken, error)
}

// VCSToken is the HTTP Basic credential of a VCS host. A zero token means
// no credential was issued.
type VCSToken struct {
	Username string
	Token    string
}

// VCSTokenFailure names why a VCS token could not be issued.
type VCSTokenFailure int

// The VCS token failures.
const (
	// VCSHostUnsupported means no credential pool serves the host.
	VCSHostUnsupported VCSTokenFailure = iota + 1
	// VCSHostAmbiguous means several credential pools serve the host.
	VCSHostAmbiguous
	// VCSNoUsableCredential means the pool that serves the host has no
	// usable credential.
	VCSNoUsableCredential
)

// VCSTokenError reports a VCS token that could not be issued, with the host
// and provider, and for VCSHostAmbiguous the pools that matched. Message is
// the core's description, which the caller reports.
type VCSTokenError struct {
	Failure       VCSTokenFailure
	Host          string
	Provider      string
	MatchingPools []string
	Message       string
}

func (e *VCSTokenError) Error() string { return e.Message }

// RateCounter is served by the session core to the platform tools seam.
type RateCounter interface {
	// IncrementRate is GWSEAM-IncrementRate. It records one request against
	// the key's current one-minute window and returns the window's running
	// count.
	IncrementRate(ctx context.Context, key string, now time.Time) (int, error)
}

// The errors the operations above return for a failure the caller branches
// on.
var (
	// ErrCallerRefused reports a caller whose bearer may not create
	// sessions.
	ErrCallerRefused = errors.New("seam: caller may not create sessions")
	// ErrExportScanUnavailable reports an export scan whose interceptor
	// is not registered, so the export fails closed.
	ErrExportScanUnavailable = errors.New("seam: export scan unavailable")
	// ErrTokenServiceUnavailable reports a credential the Token Service
	// could not mint.
	ErrTokenServiceUnavailable = errors.New("seam: token service unavailable")
)
