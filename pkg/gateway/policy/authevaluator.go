// SPDX-License-Identifier: MIT

package policy

import (
	"context"
	"strings"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/interceptor"
)

// AuthEvaluatorPriority is the §4.8 built-in priority for AuthEvaluator.
// The §4.8 built-in interceptor table fixes it at 100, the top of the
// reserved range (priorities 1–100 are built-in only), so AuthEvaluator
// runs before every other interceptor. spec: §4.8 lines 972, 1021.
const AuthEvaluatorPriority int32 = 100

// AuthEvaluatorName identifies AuthEvaluator in audit rows and chain
// errors.
const AuthEvaluatorName = "AuthEvaluator"

// CodeAuthRequired is the §4.8 code AuthEvaluator returns when the
// PreAuth chain runs without an authenticated identity in metadata. The
// auth middleware verifies the JWT before the chain runs, so this is a
// defensive fail-closed guard rather than the primary authentication
// path.
//
// spec: §15.1 line 986 — `UNAUTHORIZED` (HTTP 401) is the canonical
// catalog code for "Missing or invalid authentication credentials".
const CodeAuthRequired = "UNAUTHORIZED"

// AuthEvaluator is the §4.8 built-in interceptor at PreAuth (priority
// 100). The auth middleware verifies the bearer JWT (or dev headers)
// and resolves the principal before the PreAuth chain runs;
// AuthEvaluator then runs as the sole interceptor at that phase
// (external interceptors are rejected from PreAuth at registration,
// §4.8 line 1023) as the fail-closed gate confirming an authenticated
// identity is present in the metadata every later phase reads.
//
// The §4.8 phase table states MODIFY at PreAuth is issued only by
// AuthEvaluator itself, to normalize request metadata. That
// normalization is performed by the auth middleware when it resolves
// the principal and populates the tenant_id / user_id metadata before
// running this chain; AuthEvaluator confirms the result and rejects a
// request that reaches the chain without an authenticated tenant
// (spec: §4.8 lines 1025–1028, "authenticated identity available at
// priority > 100"). Realizing PreAuth as a chain keeps the §4.8 SPI
// uniform across phases.
type AuthEvaluator struct{}

// NewAuthEvaluator returns the §4.8 AuthEvaluator built-in.
func NewAuthEvaluator() *AuthEvaluator { return &AuthEvaluator{} }

// Name implements interceptor.Interceptor.
func (e *AuthEvaluator) Name() string { return AuthEvaluatorName }

// Priority implements interceptor.Interceptor.
func (e *AuthEvaluator) Priority() int32 { return AuthEvaluatorPriority }

// Builtin implements interceptor.Interceptor. AuthEvaluator is a
// built-in, so it may register within the reserved priority ceiling and
// on the PreAuth phase.
func (e *AuthEvaluator) Builtin() bool { return true }

// FailPolicy implements interceptor.Interceptor. AuthEvaluator is
// fail-closed: a missing authenticated identity rejects the request.
func (e *AuthEvaluator) FailPolicy() interceptor.FailPolicy { return interceptor.FailClosed }

// Timeout implements interceptor.Interceptor.
func (e *AuthEvaluator) Timeout() time.Duration { return 0 }

// Intercept implements interceptor.Interceptor. It confirms an
// authenticated tenant is present and admits the request with
// ActionAllow; a missing tenant id returns ActionReject so the chain
// fails closed.
func (e *AuthEvaluator) Intercept(ctx context.Context, req interceptor.Request) (interceptor.Result, error) {
	tenant := req.Metadata[MetadataTenantID]
	if tenant == "" {
		tenant = req.TenantID
	}
	if strings.TrimSpace(tenant) == "" {
		return interceptor.Result{
			Action: interceptor.ActionReject,
			Code:   CodeAuthRequired,
			Reason: "the PreAuth chain ran without an authenticated tenant identity",
		}, nil
	}
	return interceptor.Result{Action: interceptor.ActionAllow}, nil
}

// Ensure AuthEvaluator satisfies the interceptor contract at compile
// time.
var _ interceptor.Interceptor = (*AuthEvaluator)(nil)
