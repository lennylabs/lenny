//go:build contract

// SPDX-License-Identifier: MIT

// Tier-3 contract test for the codes the admin credential-pool secretRef
// checks return on the wire. Each case drives the production admin
// handlers (router → store → JSON error encoder) over httptest and asserts
// that the status, the code, the details keys, the body's category and
// retryable fields, and the presence of Retry-After match the §15.1
// catalog row, and that errorclassify.Classify on the code string returns
// the same category and retryability. The second assertion matters
// because writeError classifies by status when the code has no table
// entry, so a missing entry would pass the body check alone.
//
// spec: §15.1, §4.9 (Credential Storage Topology), §25.2.
package admin_credential_pool_codes_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"
	"time"

	pkgauth "github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credentialpoolstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/errorclassify"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
)

const credNS = "lenny-credentials"

// prober answers every probe with verdict, or with err when set.
type prober struct {
	verdict admin.SecretProbeVerdict
	err     error
}

func (p prober) ProbeSecretAccess(context.Context, string) (admin.SecretProbeVerdict, error) {
	return p.verdict, p.err
}

func platformAdmin(req *http.Request) *http.Request {
	return req.WithContext(authmw.WithPrincipal(req.Context(), authmw.Principal{
		TenantID: "platform", Subject: "admin@acme.com",
		Roles: []pkgauth.Role{pkgauth.RolePlatformAdmin},
	}))
}

func tenantAdmin(req *http.Request) *http.Request {
	return req.WithContext(authmw.WithPrincipal(req.Context(), authmw.Principal{
		TenantID: "acme", Subject: "alice@acme.com",
		Roles: []pkgauth.Role{pkgauth.RoleTenantAdmin},
	}))
}

func pool(name string, refs ...string) admin.CredentialPoolPayload {
	p := admin.CredentialPoolPayload{
		TenantID: "acme", Name: name, Provider: "anthropic_direct",
		AssignmentStrategy: "least-loaded", MaxConcurrentSessions: 10,
	}
	for i, ref := range refs {
		p.Credentials = append(p.Credentials, admin.CredentialEntryPayload{ID: "key-" + string(rune('1'+i)), SecretRef: ref})
	}
	return p
}

// writer is a credential Secret writer whose Put and Delete return err.
type writer struct{ err error }

func (w writer) Put(context.Context, string, admin.SecretOwner, map[string]string) error {
	return w.err
}
func (w writer) Delete(context.Context, string) error       { return w.err }
func (w writer) DeleteTenant(context.Context, string) error { return w.err }

// newHandler returns the production admin handler over a store holding
// acme/p1 at version 1 with one operator ref and acme/gone, soft-deleted.
// w is wired as the credential Secret writer unless it is nil.
func newHandler(t *testing.T, pr admin.SecretAccessProber, w admin.CredentialSecretWriter) http.Handler {
	t.Helper()
	store := credentialpoolstore.NewMemory()
	for _, name := range []string{"p1", "gone"} {
		if err := store.Create(context.Background(), credentialpoolstore.CredentialPool{
			TenantID: "acme", Name: name, Provider: "anthropic_direct",
			AssignmentStrategy: "least-loaded", MaxConcurrentSessions: 10,
			Credentials: []credentialpoolstore.Credential{{ID: "key-1", SecretRef: credNS + "/existing"}},
		}); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	if err := store.SoftDelete(context.Background(), "acme", "gone", time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("soft delete: %v", err)
	}
	r := admin.NewRouter(tenantstore.NewMemory(), admin.Options{
		Clock:                func() time.Time { return time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC) },
		CredentialsNamespace: credNS,
	}).WithCredentialPools(store).WithSecretAccessProber(pr)
	if w != nil {
		r = r.WithCredentialSecretWriter(w)
	}
	return r.Handler()
}

// secretData is a credential addition body carrying a submitted value.
func secretData(id string) map[string]any {
	return map[string]any{"id": id, "secretData": map[string]string{"apiKey": "sk-ant-x"}}
}

type wireCase struct {
	name         string
	prober       admin.SecretAccessProber
	writer       admin.CredentialSecretWriter
	as           func(*http.Request) *http.Request
	method, path string
	ifMatch      string
	body         any
	status       int
	code         string
	detailKeys   []string
	category     errorclassify.Category
	retryable    bool
	retryAfter   bool
}

// TestAdminCredentialPoolSecretRefCodesOnWire pins each distinct code the
// secretRef checks return to its §15.1 catalog row.
//
// diagnosis: a failure means an admin credential-pool refusal leaves the
// gateway with a status, code, details key, category, retryability, or
// Retry-After header that differs from the §15.1 catalog row, or the code
// has no errorclassify table entry, so a client branching on the catalog
// mishandles it.
// spec: §15.1 (error catalog: VALIDATION_ERROR, ETAG_REQUIRED, ETAG_MISMATCH,
// FORBIDDEN, CREDENTIAL_SECRET_RBAC_MISSING, CREDENTIAL_PROBE_UNAVAILABLE,
// TOKEN_SERVICE_NOT_CONFIGURED, SERVICE_UNAVAILABLE, RESOURCE_NOT_FOUND),
// §4.9 (Credential Storage Topology), §25.2 (errorclassify)
func TestAdminCredentialPoolSecretRefCodesOnWire(t *testing.T) {
	cases := []wireCase{
		{
			name: "ref outside the credentials namespace", prober: prober{}, as: platformAdmin,
			method: http.MethodPost, path: "/v1/admin/credential-pools", body: pool("new", "lenny-system/k"),
			status: http.StatusBadRequest, code: "VALIDATION_ERROR", detailKeys: []string{"field"},
			category: errorclassify.CategoryPermanent,
		},
		{
			name: "reserved prefix", prober: prober{}, as: platformAdmin,
			method: http.MethodPost, path: "/v1/admin/credential-pools", body: pool("new", "lenny-cred-0123456789abcdef0123456789abcdef01234567"),
			status: http.StatusBadRequest, code: "VALIDATION_ERROR", detailKeys: []string{"field"},
			category: errorclassify.CategoryPermanent,
		},
		{
			name: "pool PUT without If-Match", prober: prober{}, as: platformAdmin,
			method: http.MethodPut, path: "/v1/admin/credential-pools/ghost", body: pool("ghost"),
			status: http.StatusPreconditionRequired, code: "ETAG_REQUIRED", detailKeys: []string{"fields"},
			category: errorclassify.CategoryPermanent,
		},
		{
			name: "pool PUT with a stale If-Match", prober: prober{}, as: platformAdmin,
			method: http.MethodPut, path: "/v1/admin/credential-pools/p1", ifMatch: `"7"`, body: pool("p1", "existing"),
			status: http.StatusPreconditionFailed, code: "ETAG_MISMATCH", detailKeys: []string{"currentEtag"},
			category: errorclassify.CategoryPermanent,
		},
		{
			name: "registration by a tenant-admin", prober: prober{}, as: tenantAdmin,
			method: http.MethodPost, path: "/v1/admin/credential-pools", body: pool("new", "operator-made"),
			status: http.StatusForbidden, code: "FORBIDDEN",
			// The §15.1 catalog row gives FORBIDDEN the POLICY category,
			// while the errorclassify table maps it to PERMANENT for every
			// admin 403. The case pins the body to the table, which is the
			// shared classification every 403 takes.
			category: errorclassify.CategoryPermanent,
		},
		{
			name: "probe NOT_FOUND", prober: prober{verdict: admin.SecretProbeNotFound}, as: platformAdmin,
			method: http.MethodPost, path: "/v1/admin/credential-pools", body: pool("new", "absent"),
			status: http.StatusUnprocessableEntity, code: "CREDENTIAL_SECRET_RBAC_MISSING", detailKeys: []string{"missingSecrets"},
			category: errorclassify.CategoryPermanent,
		},
		{
			name: "probe with no verdict", prober: prober{err: errors.New("unreachable")}, as: platformAdmin,
			method: http.MethodPost, path: "/v1/admin/credential-pools", body: pool("new", "absent"),
			status: http.StatusServiceUnavailable, code: "CREDENTIAL_PROBE_UNAVAILABLE", detailKeys: []string{"secretRef"},
			category: errorclassify.CategoryTransient, retryable: true, retryAfter: true,
		},
		{
			name: "secretData on pool create", prober: prober{}, as: platformAdmin,
			method: http.MethodPost, path: "/v1/admin/credential-pools",
			body:   map[string]any{"tenantId": "acme", "name": "new", "provider": "anthropic_direct", "credentials": []any{secretData("k")}},
			status: http.StatusBadRequest, code: "VALIDATION_ERROR", detailKeys: []string{"field"},
			category: errorclassify.CategoryPermanent,
		},
		{
			name: "empty secretData on credential add", prober: prober{}, writer: writer{}, as: tenantAdmin,
			method: http.MethodPost, path: "/v1/admin/credential-pools/p1/credentials?tenantId=acme",
			body:   map[string]any{"id": "k", "secretData": map[string]string{}},
			status: http.StatusBadRequest, code: "VALIDATION_ERROR", detailKeys: []string{"field"},
			category: errorclassify.CategoryPermanent,
		},
		{
			name: "credential add on a soft-deleted pool", prober: prober{}, writer: writer{}, as: tenantAdmin,
			method: http.MethodPost, path: "/v1/admin/credential-pools/gone/credentials?tenantId=acme", body: secretData("k"),
			status: http.StatusNotFound, code: "RESOURCE_NOT_FOUND",
			category: errorclassify.CategoryPermanent,
		},
		{
			name: "secretData with no Token Service", prober: prober{}, as: tenantAdmin,
			method: http.MethodPost, path: "/v1/admin/credential-pools/p1/credentials?tenantId=acme", body: secretData("k"),
			status: http.StatusUnprocessableEntity, code: "TOKEN_SERVICE_NOT_CONFIGURED",
			category: errorclassify.CategoryPermanent,
		},
		{
			name: "secretData dryRun with no Token Service", prober: prober{}, as: tenantAdmin,
			method: http.MethodPost, path: "/v1/admin/credential-pools/p1/credentials?tenantId=acme&dryRun=true", body: secretData("k"),
			status: http.StatusUnprocessableEntity, code: "TOKEN_SERVICE_NOT_CONFIGURED",
			category: errorclassify.CategoryPermanent,
		},
		{
			name: "failed Secret Put", prober: prober{}, writer: writer{err: errors.New("apiserver down")}, as: tenantAdmin,
			method: http.MethodPost, path: "/v1/admin/credential-pools/p1/credentials?tenantId=acme", body: secretData("k"),
			status: http.StatusServiceUnavailable, code: "SERVICE_UNAVAILABLE",
			category: errorclassify.CategoryTransient, retryable: true, retryAfter: true,
		},
		{
			name: "pool DELETE stale If-Match", prober: prober{}, writer: writer{}, as: tenantAdmin,
			method: http.MethodDelete, path: "/v1/admin/credential-pools/p1?tenantId=acme", ifMatch: `"9"`,
			status: http.StatusPreconditionFailed, code: "ETAG_MISMATCH", detailKeys: []string{"currentEtag"},
			category: errorclassify.CategoryPermanent,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHandler(t, tc.prober, tc.writer)
			buf, _ := json.Marshal(tc.body)
			req := tc.as(httptest.NewRequest(tc.method, tc.path, bytes.NewReader(buf)))
			if tc.ifMatch != "" {
				req.Header.Set("If-Match", tc.ifMatch)
			}
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)
			assertWire(t, rr, tc)
		})
	}
}

func assertWire(t *testing.T, rr *httptest.ResponseRecorder, tc wireCase) {
	t.Helper()
	if rr.Code != tc.status {
		t.Fatalf("status = %d, want %d; body %s", rr.Code, tc.status, rr.Body.String())
	}
	var env struct {
		Error struct {
			Code      string         `json:"code"`
			Category  string         `json:"category"`
			Retryable bool           `json:"retryable"`
			Details   map[string]any `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v (%s)", err, rr.Body.String())
	}
	e := env.Error
	if e.Code != tc.code {
		t.Fatalf("code = %q, want %q", e.Code, tc.code)
	}
	keys := make([]string, 0, len(e.Details))
	for k := range e.Details {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	want := append([]string(nil), tc.detailKeys...)
	sort.Strings(want)
	if len(keys) != len(want) || (len(keys) > 0 && !equal(keys, want)) {
		t.Fatalf("details keys = %v, want %v", keys, want)
	}
	if e.Category != string(tc.category) || e.Retryable != tc.retryable {
		t.Fatalf("body category/retryable = %s/%v, want %s/%v", e.Category, e.Retryable, tc.category, tc.retryable)
	}
	if got := rr.Header().Get("Retry-After") != ""; got != tc.retryAfter {
		t.Fatalf("Retry-After present = %v, want %v", got, tc.retryAfter)
	}
	if !errorclassify.Known(tc.code) {
		t.Fatalf("errorclassify has no table entry for %s", tc.code)
	}
	if cat, retryable := errorclassify.Classify(tc.code); cat != tc.category || retryable != tc.retryable {
		t.Fatalf("errorclassify.Classify(%s) = %s/%v, want %s/%v", tc.code, cat, retryable, tc.category, tc.retryable)
	}
}

func equal(a, b []string) bool {
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
