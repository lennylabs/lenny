//go:build component

// SPDX-License-Identifier: MIT

// Component tests for the §10.2 KMS signing failure mode on every mint
// path. The Token Service server and the gateway admin router are each
// built around the §10.2 key set, the remote ES256 signer, and the
// JWTSigner circuit breaker from one jwt.LoadSigning call over the
// testinfra KMS stub's provider, with a fake breaker clock. The stub
// injects provider throttling and hard Sign failures; the tests assert
// the 503 KMS_SIGNING_UNAVAILABLE response, its Retry-After, the breaker
// state, and that a token issued before the fault keeps verifying.
package tokensigning_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/audit"
	pkgauth "github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/impersonation"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/userstore"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admintoken"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/kms"
	"github.com/lennylabs/lenny/pkg/tokenservice"
	kmsstub "github.com/lennylabs/lenny/tests/testinfra/stubs/kms"
)

// signingFixture is one signer process: the key set, the breaker-wrapped
// remote signer with a movable clock, the stub that injects faults, and
// a token minted before any fault.
type signingFixture struct {
	stub     *kmsstub.Stub
	keySet   *jwt.KeySet
	signer   *jwt.BreakerSigner
	mu       sync.Mutex
	now      time.Time
	preFault string
}

func newSigningFixture(t *testing.T) *signingFixture {
	t.Helper()
	stub := kmsstub.New(t)
	ks, signer, err := jwt.LoadSigning(context.Background(), stub.AsProvider(), map[string]string{kms.AliasJWTSigning: "key-1"}, nil)
	if err != nil {
		t.Fatalf("LoadSigning: %v", err)
	}
	f := &signingFixture{stub: stub, keySet: ks, signer: signer, now: time.Unix(7_000_000, 0)}
	signer.Now = f.clock
	f.preFault = f.mintCaller(t)
	return f
}

func (f *signingFixture) clock() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

func (f *signingFixture) advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = f.now.Add(d)
}

// mintCaller signs a platform-admin user bearer under the shared key.
func (f *signingFixture) mintCaller(t *testing.T) string {
	t.Helper()
	now := time.Now()
	tok, err := f.signer.Sign(context.Background(), jwt.Claims{
		Subject:  "admin@acme.com",
		TenantID: "acme",
		Typ:      pkgauth.TokenUserBearer,
		Scope:    "sessions:read sessions:write",
		Audience: []string{"lenny-gateway"},
		IssuedAt: now.Unix(),
		Expiry:   now.Add(time.Hour).Unix(),
		JWTID:    fmt.Sprintf("jti-pre-%d", now.UnixNano()),
	})
	if err != nil {
		t.Fatalf("mint pre-fault token: %v", err)
	}
	return tok
}

// assertPreFaultVerifies checks that the token issued before the fault
// still verifies against the key set.
func (f *signingFixture) assertPreFaultVerifies(t *testing.T) {
	t.Helper()
	if _, err := f.keySet.Verify(f.preFault); err != nil {
		t.Errorf("token issued before the fault no longer verifies: %v", err)
	}
}

// mintResult is the part of a mint response the tests assert on.
type mintResult struct {
	status     int
	retryAfter string
	code       string
}

// mintPath drives one mint route of a signer process.
type mintPath struct {
	name  string
	build func(t *testing.T, f *signingFixture) func(t *testing.T) mintResult
}

var mintPaths = []mintPath{
	{"impersonation", buildImpersonationMint},
	{"rotate-token", buildRotateTokenMint},
	{"oauth-token", buildOAuthTokenMint},
}

// okAppender is an impersonation.AuditAppender whose writes succeed.
type okAppender struct{}

func (okAppender) Append(context.Context, string, string, json.RawMessage, time.Time) (audit.Row, error) {
	return audit.Row{ID: "row"}, nil
}

// memSecrets is an in-memory admintoken.SecretStore.
type memSecrets struct {
	mu   sync.Mutex
	data map[string]map[string][]byte
}

func (m *memSecrets) Get(_ context.Context, ns, name string) (map[string][]byte, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.data[ns+"/"+name]
	return d, ok, nil
}

func (m *memSecrets) Create(_ context.Context, ns, name string, _ map[string]string, data map[string][]byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.data == nil {
		m.data = map[string]map[string][]byte{}
	}
	m.data[ns+"/"+name] = data
	return nil
}

func (m *memSecrets) Update(ctx context.Context, ns, name string, data map[string][]byte) error {
	return m.Create(ctx, ns, name, nil, data)
}

// newGatewayAdminRouter builds the gateway admin router with the real
// impersonation issuer and admin-token provisioner over the fixture's
// signer, as cmd/lenny-gateway wires them.
func newGatewayAdminRouter(t *testing.T, f *signingFixture) http.Handler {
	t.Helper()
	ctx := context.Background()
	tenants := tenantstore.NewMemory()
	if err := tenants.Create(ctx, tenantstore.Tenant{ID: "acme"}); err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	users := userstore.NewMemory()
	if err := users.Create(ctx, userstore.User{Subject: "alice@acme.com", TenantID: "acme", Roles: []pkgauth.Role{pkgauth.RoleUser}}); err != nil {
		t.Fatalf("create user: %v", err)
	}
	var ids atomic.Int64
	imp := impersonation.New(impersonation.NewMemStore(), okAppender{}, f.signer, impersonation.Config{
		PlatformTenantID: "platform",
		MaxDuration:      time.Hour,
		NewID:            func() string { return fmt.Sprintf("imp-%d", ids.Add(1)) },
	})
	prov, err := admintoken.New(admintoken.Config{Namespace: "lenny-system", AdminTenant: "platform"},
		f.signer, users, &memSecrets{}, nil, nil)
	if err != nil {
		t.Fatalf("admintoken.New: %v", err)
	}
	return admin.NewRouter(tenants, admin.Options{}).
		WithUsers(users).
		WithImpersonation(imp).
		WithAdminTokenProvisioner(prov).
		Handler()
}

// asPlatformAdmin attaches the authenticated platform-admin principal
// the auth middleware would set.
func asPlatformAdmin(req *http.Request) *http.Request {
	return req.WithContext(authmw.WithPrincipal(req.Context(), authmw.Principal{
		Subject:  "admin@acme.com",
		TenantID: "platform",
		Roles:    []pkgauth.Role{pkgauth.RolePlatformAdmin},
	}))
}

func serve(h http.Handler, req *http.Request) mintResult {
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return decodeMintResult(rr.Code, rr.Header().Get("Retry-After"), rr.Body)
}

func decodeMintResult(status int, retryAfter string, body io.Reader) mintResult {
	var env struct {
		Error json.RawMessage `json:"error"`
	}
	raw, _ := io.ReadAll(body)
	_ = json.Unmarshal(raw, &env)
	var lenny struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(env.Error, &lenny)
	return mintResult{status: status, retryAfter: retryAfter, code: lenny.Code}
}

func buildImpersonationMint(t *testing.T, f *signingFixture) func(t *testing.T) mintResult {
	h := newGatewayAdminRouter(t, f)
	return func(t *testing.T) mintResult {
		b, _ := json.Marshal(admin.StartImpersonationRequest{
			TargetTenantID: "acme", TargetUserID: "alice@acme.com",
			Reason: "support", TicketRef: "SUP-1", DurationSeconds: 600,
		})
		return serve(h, asPlatformAdmin(httptest.NewRequest(http.MethodPost, "/v1/admin/impersonation", bytes.NewReader(b))))
	}
}

func buildRotateTokenMint(t *testing.T, f *signingFixture) func(t *testing.T) mintResult {
	h := newGatewayAdminRouter(t, f)
	return func(t *testing.T) mintResult {
		return serve(h, asPlatformAdmin(httptest.NewRequest(http.MethodPost, "/v1/admin/users/"+admintoken.DefaultUsername+"/rotate-token", nil)))
	}
}

func buildOAuthTokenMint(t *testing.T, f *signingFixture) func(t *testing.T) mintResult {
	srv, err := tokenservice.NewServer(tokenservice.Options{
		CapabilityTokenTTL: 15 * time.Minute,
		Signer:             f.signer,
		Verifier:           f.keySet,
		Issuer:             "https://lenny.dev.test/token",
		PerDialectCap:      map[string]time.Duration{"lenny-gateway": 24 * time.Hour},
	})
	if err != nil {
		t.Fatalf("tokenservice.NewServer: %v", err)
	}
	h := srv.Handler()
	caller := f.preFault
	return func(t *testing.T) mintResult {
		b, _ := json.Marshal(tokenservice.Request{
			GrantType:        "urn:ietf:params:oauth:grant-type:token-exchange",
			SubjectToken:     caller,
			SubjectTokenType: "urn:ietf:params:oauth:token-type:jwt",
			Scope:            "sessions:read",
			Audience:         "lenny-gateway",
		})
		req := httptest.NewRequest(http.MethodPost, "/v1/oauth/token", bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+caller)
		return serve(h, req)
	}
}

func assertUnavailable(t *testing.T, got mintResult, wantRetryAfter string) {
	t.Helper()
	if got.status != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (code %q)", got.status, got.code)
	}
	if got.code != "KMS_SIGNING_UNAVAILABLE" {
		t.Errorf("error code = %q, want KMS_SIGNING_UNAVAILABLE", got.code)
	}
	if got.retryAfter != wantRetryAfter {
		t.Errorf("Retry-After = %q, want %q", got.retryAfter, wantRetryAfter)
	}
}

// spec: 10.2 (Authentication) "KMS signing failure mode", 15.1 (REST API)
// diagnosis: a mint the KMS throttled did not return 503
// KMS_SIGNING_UNAVAILABLE with Retry-After: 1, or throttling counted
// toward the breaker trip so that the next unthrottled mint was refused.
// Throttling is backpressure: it never opens the breaker.
func TestThrottledMintReturns503AndBreakerStaysClosed_spec_10_2(t *testing.T) {
	for _, p := range mintPaths {
		t.Run(p.name, func(t *testing.T) {
			f := newSigningFixture(t)
			mint := p.build(t, f)
			f.stub.SetSignThrottled(true)
			for i := 0; i < 6; i++ {
				assertUnavailable(t, mint(t), "1")
			}
			f.assertPreFaultVerifies(t)
			f.stub.SetSignThrottled(false)
			if got := mint(t); got.status != http.StatusOK {
				t.Fatalf("mint after throttling cleared: status = %d (code %q), want 200; the breaker must stay closed", got.status, got.code)
			}
		})
	}
}

// spec: 10.2 (Authentication) "KMS signing failure mode", 15.1 (REST API)
// diagnosis: one hard Sign failure with the breaker closed did not return
// 503 KMS_SIGNING_UNAVAILABLE with Retry-After: 1. Before every mint path
// mapped a signing error, each answered it with 500.
func TestHardSigningFailureReturns503RetryAfter1_spec_10_2(t *testing.T) {
	for _, p := range mintPaths {
		t.Run(p.name, func(t *testing.T) {
			f := newSigningFixture(t)
			mint := p.build(t, f)
			f.stub.SetSignError(errors.New("kms: internal failure"))
			assertUnavailable(t, mint(t), "1")
			f.assertPreFaultVerifies(t)
		})
	}
}

// spec: 10.2 (Authentication) "KMS signing failure mode", 15.1 (REST API)
// diagnosis: after four hard failures inside 30 s opened the breaker, the
// next mint either reached the KMS or did not carry Retry-After equal to
// the remaining cooldown. The first three failures leave the breaker
// closed, so each answers 1. The fourth trips it (more than three
// consecutive failures), and the breaker is open when that response is
// written, so it answers the full 30 s cooldown. The clock then moves 10 s
// into the cooldown, so the next mint answers 20.
func TestOpenBreakerMintRetryAfterIsRemainingCooldown_spec_10_2(t *testing.T) {
	for _, p := range mintPaths {
		t.Run(p.name, func(t *testing.T) {
			f := newSigningFixture(t)
			mint := p.build(t, f)
			f.stub.SetSignError(errors.New("kms: internal failure"))
			for i := 0; i < 3; i++ {
				assertUnavailable(t, mint(t), "1")
			}
			assertUnavailable(t, mint(t), "30")
			f.advance(10 * time.Second)
			calls := f.stub.SignCalls()
			assertUnavailable(t, mint(t), "20")
			if got := f.stub.SignCalls(); got != calls {
				t.Errorf("open breaker forwarded %d Sign call(s) to the KMS, want 0", got-calls)
			}
			f.assertPreFaultVerifies(t)
		})
	}
}
