// SPDX-License-Identifier: MIT

//go:build e2e_kind

// Tier-5 e2e Kind test for a mixed-credential deployment: several
// clients presenting genuinely different §10.2 credentials at the same
// time, against the same tenant and the same live session.
//
// The existing live-cluster auth tests each drive one credential at a
// time. auth_modes_test.go exercises the §10.2 standard Bearer chain
// alone, prompt_journey_auth_modes_test.go replays the §7.1 journey once
// per mode sequentially (dev headers, then bearer), and
// admin_scope_narrowing_test.go presents a single scope-narrowed bearer
// to the admin API with nothing else in flight. None of them proves the
// gateway resolves authorization per request from the credential
// actually presented when several distinct credentials are live against
// one tenant at once.
//
// §10.2's boundary table assigns a different mechanism to each boundary
// (Client → Gateway, Automated clients, Gateway ↔ Pod, Pod → Gateway),
// and a deployed cluster runs all of them concurrently. §27.3 records
// that the `oidc` and `apiKey` playground auth modes both hand the
// gateway the same standard bearer, so a mixed-mode scenario has to be
// built from credentials that genuinely differ rather than from those
// two labels. This test therefore runs, simultaneously and on one
// tenant:
//
//   - the §17.4 dev-header identity, driving the §7.1 prompt round-trip
//     through a real warm pod (which puts the Gateway ↔ Pod mTLS +
//     projected-SA-token boundary on the wire at the same instant);
//   - an RFC 9068 scope-narrowed Bearer JWT (§25.1), which must be
//     admitted on the admin endpoint its scope covers and refused on one
//     it does not;
//   - an unscoped Bearer JWT of the same role and tenant, which must be
//     admitted on the very endpoint the narrowed token is refused;
//   - a Bearer JWT for a different tenant, which must not reach the
//     first tenant's session however broad its scope; and
//   - an unverifiable Bearer, which must fail closed throughout.
//
// Every probe repeats for as long as the prompt round-trip is in flight,
// so each verdict is observed while the other credentials are live.
package tier5_e2e_kind_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/tests/testinfra/kind"
	"github.com/lennylabs/lenny/tests/testinfra/sessiondriver"
)

// mixedAuthTenant is the tenant every credential in this test acts in,
// except the deliberate foreign-tenant probe. tests/testinfra/kind/
// e2e-values.yaml seeds it through bootstrap.tenants, so it is a
// registered tenant and the auth chain's §10.2 TENANT_NOT_FOUND
// rejection cannot fire for reasons unrelated to what is under test.
const mixedAuthTenant = "acme"

// mintMixedAuthBearer signs an RFC 9068-shaped Bearer JWT over the
// §17.4 dev-bearer-trust HMAC key the e2e install already provisions
// (see loadE2EBearerTrustSigner in admin_scope_narrowing_test.go for why
// that key, rather than POST /v1/oauth/token, is the reachable way to
// mint a live-verifiable token on this install).
//
// subject must not collide with a bootstrap-seeded user row: the
// gateway's PlatformRoles override resolves stored role assignments by
// (tenant, subject) and would otherwise supersede the JWT's own `roles`
// claim. An empty scope omits the claim entirely (jwt.Claims tags Scope
// omitempty), which §25.1 defines as "no scope restriction".
func mintMixedAuthBearer(t *testing.T, signer *jwt.HMACSigner, subject, tenant string, roles []auth.Role, scope string) string {
	t.Helper()
	now := time.Now()
	tok, err := signer.Sign(jwt.Claims{
		Subject:    subject,
		TenantID:   tenant,
		CallerType: "agent",
		Roles:      roles,
		Scope:      scope,
		Audience:   []string{e2eBearerTrustAudience},
		IssuedAt:   now.Unix(),
		Expiry:     now.Add(15 * time.Minute).Unix(),
		JWTID:      subject + "-" + now.Format(time.RFC3339Nano),
	})
	if err != nil {
		t.Fatalf("sign bearer for subject %q (tenant %q, scope %q): %v", subject, tenant, scope, err)
	}
	return tok
}

// mixedAuthProbe is one credential presented on one endpoint, together
// with the verdict §10.2 and §25.1 require for that pairing.
type mixedAuthProbe struct {
	// name identifies the credential/endpoint pairing in failures.
	name string
	// method and path address the gateway endpoint under probe.
	method string
	path   string
	// bearer is the credential presented as `Authorization: Bearer`.
	bearer string
	// wantStatus is the required HTTP status on every repetition.
	wantStatus int
	// wantCode is the required §15.2 error-envelope code, empty when the
	// probe must succeed and therefore carries no error envelope.
	wantCode string
}

// runMixedAuthProbe issues p repeatedly against the live gateway until
// stop is closed, running at least once, and returns a description of
// the first repetition whose verdict differed from what p requires. An
// empty string means every repetition matched.
//
// It deliberately does not touch *testing.T: it runs on a goroutine
// alongside the prompt round-trip, where a t.Fatalf would abort the
// wrong goroutine.
func runMixedAuthProbe(ctx context.Context, hc *http.Client, baseURL string, p mixedAuthProbe, stop <-chan struct{}) string {
	for reps := 0; ; reps++ {
		status, body, err := mixedAuthRequest(ctx, hc, baseURL, p)
		if err != nil {
			return fmt.Sprintf("%s: request %s %s failed: %v", p.name, p.method, p.path, err)
		}
		if status != p.wantStatus {
			return fmt.Sprintf("%s: %s %s returned %d on repetition %d, want %d (body %v)",
				p.name, p.method, p.path, status, reps, p.wantStatus, body)
		}
		if p.wantCode != "" {
			errObj, _ := body["error"].(map[string]any)
			code, _ := errObj["code"].(string)
			if code != p.wantCode {
				return fmt.Sprintf("%s: %s %s error code = %q on repetition %d, want %q (body %v)",
					p.name, p.method, p.path, code, reps, p.wantCode, body)
			}
		}
		select {
		case <-stop:
			return ""
		case <-ctx.Done():
			return ""
		case <-time.After(250 * time.Millisecond):
		}
	}
}

// mixedAuthRequest performs one probe request and decodes the JSON body.
func mixedAuthRequest(ctx context.Context, hc *http.Client, baseURL string, p mixedAuthProbe) (int, map[string]any, error) {
	req, err := http.NewRequestWithContext(ctx, p.method, baseURL+p.path, nil)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+p.bearer)
	res, err := hc.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		return 0, nil, err
	}
	var out map[string]any
	if len(bytes.TrimSpace(raw)) > 0 {
		if err := json.Unmarshal(raw, &out); err != nil {
			return res.StatusCode, nil, fmt.Errorf("decode body %q: %w", raw, err)
		}
	}
	return res.StatusCode, out, nil
}

// spec: §10.2 (spec/10_gateway-internals.md, Authentication) boundary
// table — "Client → Gateway | OIDC/OAuth 2.1 (MCP-standard protected
// resource server)", "Automated clients | Service-to-service auth
// (client credentials grant)", "Gateway ↔ Pod | mTLS + projected service
// account token (audience-bound, short TTL)", "Pod → Gateway | Projected
// service account token (audience: deployment-specific, short TTL)";
// §25.1 (spec/25_agent-operability.md, Scoped Tokens) "A request for a
// tool not permitted by any scope returns `403 SCOPE_FORBIDDEN` with a
// response body listing the caller's active scopes.", "Absent `scope`
// claim: no scope restriction — the token's role ceiling applies
// unmodified.", "Scopes do not replace tenancy — a `tenant-admin` caller
// is still constrained to its tenant regardless of scope. Scopes
// restrict *actions*; tenancy restricts *resources*. Both are enforced
// independently."; §7.1 (spec/07_session-lifecycle.md, Normal Flow)
// steps 16-18.
//
// diagnosis: a failure here means the deployed gateway does not resolve
// authorization per request from the credential presented once several
// distinct credentials are live against one tenant and one session. A
// failing "narrowed bearer refused off-scope" probe means a scope
// narrowing evaporates in the presence of a concurrent broader
// credential (authority leaking between callers). A failing "unscoped
// bearer admitted" probe means the reverse, a narrowing bleeding onto an
// unrelated caller. A failing "foreign tenant" probe means the §25.1
// tenancy boundary is not enforced independently of scope, which is a
// cross-tenant read. A failing "unverifiable bearer" probe means the
// chain stops failing closed under concurrent load. A failure of the
// prompt round-trip itself means the Gateway ↔ Pod boundary cannot carry
// its own credential while client-facing credentials are in flight.
func TestMixedAuthModesShareOneTenantAndSession(t *testing.T) {
	c := kind.InstallLenny(t)
	signer := loadE2EBearerTrustSigner(t, c)

	// Credential 1: the §17.4 dev-header identity. It provisions the
	// tenant precondition and owns the session the other credentials
	// observe.
	dev := sessiondriver.New(t)

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	// §10.6 leaves an unset noEnvironmentPolicy resolving to deny-all,
	// and the §11.1 environment admission gate then rejects an
	// environment-less create with 403 before any auth assertion here
	// runs. This shared Kind cluster is long-lived, so an earlier run can
	// have left acme at the stricter policy; pin it explicitly.
	if err := dev.AllowSessionsWithNoEnvironment(ctx, mixedAuthTenant); err != nil {
		t.Fatalf("relax the §10.6 no-environment policy for tenant %q: %v", mixedAuthTenant, err)
	}

	sess, err := dev.CreateAndStart(ctx, mixedAuthTenant, sessiondriver.EchoRuntimeSidecar)
	if errors.Is(err, sessiondriver.ErrPoolNotReady) {
		// The §4.6 warm pool never settled an idle pod. This test is
		// about credential resolution, not pool warm-up; skip on the
		// same precondition the sibling live-session tests skip on.
		t.Skipf("precondition not met: warm pool not ready, so there is no live session to present mixed credentials against: %v", err)
	}
	if err != nil {
		t.Fatalf("create-and-start the shared session as the dev-header client: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = dev.Terminate(ctx, mixedAuthTenant, sess.ID)
	})
	t.Logf("dev-header client created the shared session %s in state %q", sess.ID, sess.State)

	// A registered second tenant for the foreign-credential probe. A
	// freshly bootstrapped tenant (rather than the built-in "default")
	// keeps the probe's 404 attributable to the resource-tenancy
	// boundary alone: an unregistered tenant claim would be rejected
	// earlier in the chain with §10.2 TENANT_NOT_FOUND and prove nothing
	// about resource scoping.
	foreignTenant, err := dev.BootstrapFreshTenant(ctx, "mixed-auth-foreign")
	if err != nil {
		t.Fatalf("bootstrap the foreign tenant for the cross-tenant probe: %v", err)
	}

	// Credential 2: a scope-narrowed Bearer. tools:me:read covers
	// GET /v1/admin/me and does not cover GET /v1/admin/tenants, whose
	// x-lenny-scope is tools:tenant:read.
	narrowed := mintMixedAuthBearer(t, signer, "sa-mixed-auth-narrowed", mixedAuthTenant,
		[]auth.Role{auth.RolePlatformAdmin}, "tools:me:read")
	// Credential 3: the same role and tenant with no scope claim at all,
	// so §25.1's role ceiling applies unmodified.
	unscoped := mintMixedAuthBearer(t, signer, "sa-mixed-auth-unscoped", mixedAuthTenant,
		[]auth.Role{auth.RolePlatformAdmin}, "")
	// Credential 4: a different tenant, deliberately carrying the whole
	// session domain (`tools:sessions:*`, the wildcard action form §25.1
	// defines and the domain §25.1's playground_allowed_scope names
	// first), to prove scope breadth over exactly the resource family
	// under probe still buys no cross-tenant read.
	foreign := mintMixedAuthBearer(t, signer, "sa-mixed-auth-foreign", foreignTenant,
		[]auth.Role{auth.RoleTenantAdmin}, "tools:sessions:*")

	probes := []mixedAuthProbe{
		{
			name:       "narrowed bearer admitted on its own scope",
			method:     http.MethodGet,
			path:       "/v1/admin/me",
			bearer:     narrowed,
			wantStatus: http.StatusOK,
		},
		{
			name:       "narrowed bearer refused off its scope",
			method:     http.MethodGet,
			path:       "/v1/admin/tenants",
			bearer:     narrowed,
			wantStatus: http.StatusForbidden,
			wantCode:   "SCOPE_FORBIDDEN",
		},
		{
			// The session REST surface declares no x-lenny-scope
			// (pkg/gateway/externalapi/openapi/openapi.json), and §25.1
			// enumerates scope enforcement as applying to the admin API
			// middleware, MCP tool invocation, and
			// /v1/admin/me/authorized-tools. A narrowing therefore leaves
			// the shared session readable at the role ceiling.
			name:       "narrowed bearer still reads the shared session",
			method:     http.MethodGet,
			path:       "/v1/sessions/" + sess.ID,
			bearer:     narrowed,
			wantStatus: http.StatusOK,
		},
		{
			name:       "unscoped bearer admitted where the narrowed one is refused",
			method:     http.MethodGet,
			path:       "/v1/admin/tenants",
			bearer:     unscoped,
			wantStatus: http.StatusOK,
		},
		{
			name:       "foreign-tenant bearer cannot read the shared session",
			method:     http.MethodGet,
			path:       "/v1/sessions/" + sess.ID,
			bearer:     foreign,
			wantStatus: http.StatusNotFound,
			wantCode:   "RESOURCE_NOT_FOUND",
		},
		{
			name:       "unverifiable bearer fails closed",
			method:     http.MethodGet,
			path:       "/v1/sessions/" + sess.ID,
			bearer:     "not-a-real-jwt",
			wantStatus: http.StatusUnauthorized,
			wantCode:   "TOKEN_INVALID",
		},
	}

	// Attach the §7.1 step 16 stream before the prompt so the pod's
	// echoed output is observed live rather than only in the synchronous
	// response.
	events, stopEvents, err := dev.StreamEvents(ctx, mixedAuthTenant, sess.ID, 0)
	if err != nil {
		t.Fatalf("attach the shared session's event stream as the dev-header client: %v", err)
	}
	defer stopEvents()

	stop := make(chan struct{})
	failures := make([]string, len(probes))
	hc := &http.Client{Timeout: 30 * time.Second}
	var wg sync.WaitGroup
	for i, p := range probes {
		wg.Add(1)
		go func(i int, p mixedAuthProbe) {
			defer wg.Done()
			failures[i] = runMixedAuthProbe(ctx, hc, dev.BaseURL(), p, stop)
		}(i, p)
	}

	// §7.1 step 18 under the dev-header credential, with every bearer
	// probe above hammering the same gateway concurrently. The echo
	// runtime prefixes each text part it emits, so matching that prefix
	// pins the assertion to the pod's own output travelling back across
	// the Gateway ↔ Pod link rather than to the gateway's reflection of
	// the client's input.
	const prompt = "mixed-auth ping"
	msgResp, err := dev.SendMessage(ctx, mixedAuthTenant, sess.ID, prompt)
	if err != nil {
		close(stop)
		wg.Wait()
		t.Fatalf("dev-header client send message %q while the bearer credentials are live: %v", prompt, err)
	}
	if msgResp.DeliveryReceipt.Status != "delivered" {
		close(stop)
		wg.Wait()
		t.Fatalf("delivery receipt status = %q, want delivered (output: %s)",
			msgResp.DeliveryReceipt.Status, msgResp.Output)
	}
	assertOutputEchoes(t, "POST /messages response under mixed credentials", msgResp.Output, prompt)

	echoed := waitForEchoedOutput(ctx, events, prompt, 60*time.Second)
	close(stop)
	wg.Wait()

	if echoed != "" {
		t.Errorf("the §7.1 prompt round-trip did not complete while the client-facing bearers were live: %s", echoed)
	}
	for _, f := range failures {
		if f != "" {
			t.Error(f)
		}
	}
	if !t.Failed() {
		t.Logf("all %d credential/endpoint pairings held their verdict for the whole prompt round-trip on tenant %q, session %s",
			len(probes), mixedAuthTenant, sess.ID)
	}
}

// waitForEchoedOutput drains the §7.1 step 17 event stream until a frame
// carries the echo runtime's output for prompt, and returns a
// description of what went wrong instead (empty string on success). It
// returns rather than fails so the caller can shut the concurrent probes
// down before reporting.
func waitForEchoedOutput(ctx context.Context, events <-chan sessiondriver.Event, prompt string, timeout time.Duration) string {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				return fmt.Sprintf("the event stream closed before any frame carried the runtime's echoed output for prompt %q", prompt)
			}
			data := string(ev.Data)
			if strings.Contains(data, echoOutputPrefix) && strings.Contains(data, prompt) {
				return ""
			}
		case <-deadline.C:
			return fmt.Sprintf("timed out after %s waiting for an event-stream frame carrying the runtime's echoed output for prompt %q", timeout, prompt)
		case <-ctx.Done():
			return fmt.Sprintf("context ended while waiting for the runtime's echoed output for prompt %q: %v", prompt, ctx.Err())
		}
	}
}
