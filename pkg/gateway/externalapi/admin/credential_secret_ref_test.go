// SPDX-License-Identifier: MIT

package admin_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	pkgauth "github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credentialpoolstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/customrolestore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
)

// These tests pin the secretRef checks every admin credential-pool write
// and the bootstrap seed run, in the §4.9 order: the ref parse and
// canonical form, the If-Match compare, the platform-admin registration
// rule, the reserved lenny-cred- prefix, and the admin-time Secret probe.
// Each column runs its variants with every earlier column passing, and a
// pair case asserts that a request failing two consecutive columns gets
// the earlier column's code with no later probe call or commit.

const refTestNS = "lenny-credentials"

// interleavingStore wraps credentialpoolstore.Memory with two hooks on
// the locking call. beforeUpdate commits a concurrent write before the
// wrapped Update takes the row, once. failCommit returns an error after
// the mutate closure returns nil and leaves the row unwritten.
type interleavingStore struct {
	*credentialpoolstore.Memory
	beforeUpdate func(ctx context.Context, m *credentialpoolstore.Memory)
	failCommit   error
}

func (s *interleavingStore) Update(ctx context.Context, tenantID, name string, mutate func(*credentialpoolstore.CredentialPool) error) (credentialpoolstore.CredentialPool, error) {
	if hook := s.beforeUpdate; hook != nil {
		s.beforeUpdate = nil
		hook(ctx, s.Memory)
	}
	if s.failCommit == nil {
		return s.Memory.Update(ctx, tenantID, name, mutate)
	}
	commitErr := s.failCommit
	_, err := s.Memory.Update(ctx, tenantID, name, func(p *credentialpoolstore.CredentialPool) error {
		if err := mutate(p); err != nil {
			return err
		}
		return commitErr
	})
	return credentialpoolstore.CredentialPool{}, err
}

// refHarness is an admin Router over an interleavingStore, a fake prober,
// and a custom-role registry holding cred-admin, a custom role with
// manage_credential_pools.
type refHarness struct {
	h      http.Handler
	store  *interleavingStore
	prober *fakeSecretProber
}

func newRefHarness(t *testing.T) *refHarness {
	t.Helper()
	roles := customrolestore.NewMemory()
	if err := roles.Create(context.Background(), customrolestore.CustomRole{
		TenantID: "acme", Name: "cred-admin",
		Permissions: []pkgauth.Permission{pkgauth.PermManageCredentialPools},
	}); err != nil {
		t.Fatalf("seed custom role: %v", err)
	}
	store := &interleavingStore{Memory: credentialpoolstore.NewMemory()}
	prober := &fakeSecretProber{verdicts: map[string]admin.SecretProbeVerdict{}}
	router := admin.NewRouter(tenantstore.NewMemory(), admin.Options{
		Clock:                func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		CredentialsNamespace: refTestNS,
	}).WithCredentialPools(store).WithSecretAccessProber(prober).WithCustomRoles(roles)
	return &refHarness{h: router.Handler(), store: store, prober: prober}
}

// asCredAdmin authenticates as acme's cred-admin custom role.
func asCredAdmin(req *http.Request) *http.Request { return withRolesFor(req, "acme", "cred-admin") }

// managedRef returns the canonical ref of the Secret the gateway writes
// for (acme, pool, credID).
func managedRef(pool, credID string) string {
	return credential.CanonicalSecretRef(refTestNS, credential.ManagedSecretName("acme", pool, credID))
}

// seedRefPool creates acme/p1 with key-1 on an operator Secret and key-2
// on its Lenny-written Secret, and returns the stored row.
func seedRefPool(t *testing.T, hs *refHarness) credentialpoolstore.CredentialPool {
	t.Helper()
	if err := hs.store.Create(context.Background(), credentialpoolstore.CredentialPool{
		TenantID: "acme", Name: "p1", Provider: "anthropic_direct",
		AssignmentStrategy: "least-loaded", MaxConcurrentSessions: 10,
		Credentials: []credentialpoolstore.Credential{
			{ID: "key-1", SecretRef: refTestNS + "/existing-1"},
			{ID: "key-2", SecretRef: managedRef("p1", "key-2")},
		},
	}); err != nil {
		t.Fatalf("seed pool: %v", err)
	}
	return storedRow(t, hs, "p1")
}

func storedRow(t *testing.T, hs *refHarness, name string) credentialpoolstore.CredentialPool {
	t.Helper()
	row, err := hs.store.Get(context.Background(), "acme", name)
	if err != nil {
		t.Fatalf("Get %s: %v", name, err)
	}
	return row
}

// sendRef serves one request with an explicit If-Match (none when empty).
func sendRef(t *testing.T, hs *refHarness, method, path string, body any, as func(*http.Request) *http.Request, ifMatch string) *httptest.ResponseRecorder {
	t.Helper()
	buf, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req := as(httptest.NewRequest(method, path, bytes.NewReader(buf)))
	if ifMatch != "" {
		req.Header.Set("If-Match", ifMatch)
	}
	rr := httptest.NewRecorder()
	hs.h.ServeHTTP(rr, req)
	return rr
}

type refErrBody struct {
	Code    string         `json:"code"`
	Details map[string]any `json:"details"`
}

func decodeRefErr(t *testing.T, rr *httptest.ResponseRecorder) refErrBody {
	t.Helper()
	var env struct {
		Error refErrBody `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode error body: %v (%s)", err, rr.Body.String())
	}
	return env.Error
}

// wantRefusal asserts a status, a code, and (when field is set) the
// details.field value.
func wantRefusal(t *testing.T, rr *httptest.ResponseRecorder, status int, code, field string) refErrBody {
	t.Helper()
	if rr.Code != status {
		t.Fatalf("status = %d, want %d; body %s", rr.Code, status, rr.Body.String())
	}
	got := decodeRefErr(t, rr)
	if got.Code != code {
		t.Fatalf("code = %q, want %q; body %s", got.Code, code, rr.Body.String())
	}
	if field != "" && got.Details["field"] != field {
		t.Fatalf("details.field = %v, want %q", got.Details["field"], field)
	}
	return got
}

func etagOf(v int64) string { return `"` + strconv.FormatInt(v, 10) + `"` }

// refPath is one admin write path. submit sends entries (the credential
// paths send the first entry) and returns the response.
type refPath struct {
	name     string
	dryRun   bool
	needPool bool
	submit   func(t *testing.T, hs *refHarness, as func(*http.Request) *http.Request, entries []admin.CredentialEntryPayload) *httptest.ResponseRecorder
}

func dryRunSuffix(dry bool, sep string) string {
	if dry {
		return sep + "dryRun=true"
	}
	return ""
}

func poolBody(name string, entries []admin.CredentialEntryPayload) admin.CredentialPoolPayload {
	return admin.CredentialPoolPayload{
		TenantID: "acme", Name: name, Provider: "anthropic_direct",
		AssignmentStrategy: "least-loaded", MaxConcurrentSessions: 10,
		Credentials: entries,
	}
}

func refPaths() []refPath {
	create := func(dry bool) refPath {
		return refPath{
			name: "pool create" + dryRunSuffix(dry, " "), dryRun: dry,
			submit: func(t *testing.T, hs *refHarness, as func(*http.Request) *http.Request, e []admin.CredentialEntryPayload) *httptest.ResponseRecorder {
				return sendRef(t, hs, http.MethodPost, "/v1/admin/credential-pools"+dryRunSuffix(dry, "?"), poolBody("new", e), as, "")
			},
		}
	}
	put := func(dry bool) refPath {
		return refPath{
			name: "pool PUT" + dryRunSuffix(dry, " "), dryRun: dry, needPool: true,
			submit: func(t *testing.T, hs *refHarness, as func(*http.Request) *http.Request, e []admin.CredentialEntryPayload) *httptest.ResponseRecorder {
				etag := etagOf(storedRow(t, hs, "p1").Version)
				return sendRef(t, hs, http.MethodPut, "/v1/admin/credential-pools/p1"+dryRunSuffix(dry, "?"), poolBody("p1", e), as, etag)
			},
		}
	}
	add := func(dry bool) refPath {
		return refPath{
			name: "credential add" + dryRunSuffix(dry, " "), dryRun: dry, needPool: true,
			submit: func(t *testing.T, hs *refHarness, as func(*http.Request) *http.Request, e []admin.CredentialEntryPayload) *httptest.ResponseRecorder {
				return sendRef(t, hs, http.MethodPost, "/v1/admin/credential-pools/p1/credentials?tenantId=acme"+dryRunSuffix(dry, "&"), e[0], as, "")
			},
		}
	}
	update := func(dry bool) refPath {
		return refPath{
			name: "credential update" + dryRunSuffix(dry, " "), dryRun: dry, needPool: true,
			submit: func(t *testing.T, hs *refHarness, as func(*http.Request) *http.Request, e []admin.CredentialEntryPayload) *httptest.ResponseRecorder {
				return sendRef(t, hs, http.MethodPut, "/v1/admin/credential-pools/p1/credentials/key-1?tenantId=acme"+dryRunSuffix(dry, "&"), e[0], as, "")
			},
		}
	}
	return []refPath{create(false), create(true), put(false), put(true), add(false), add(true), update(false), update(true)}
}

// entriesFor returns the submitted entries for a path: the pool paths
// carry the seeded pool's entries plus the probe entry, and the credential
// paths carry the probe entry alone (its id is key-3 on an add and key-1
// on an update).
func entriesFor(p refPath, ref string) []admin.CredentialEntryPayload {
	switch {
	case strings.HasPrefix(p.name, "pool") && p.needPool:
		return []admin.CredentialEntryPayload{
			{ID: "key-1", SecretRef: "existing-1"},
			{ID: "key-2", SecretRef: managedRef("p1", "key-2")},
			{ID: "key-3", SecretRef: ref},
		}
	case strings.HasPrefix(p.name, "credential update"):
		return []admin.CredentialEntryPayload{{ID: "key-1", SecretRef: ref}}
	default:
		return []admin.CredentialEntryPayload{{ID: "key-3", SecretRef: ref}}
	}
}

// assertUnchanged asserts the write committed nothing: a created pool is
// absent and a seeded pool keeps its version and credentials.
func assertUnchanged(t *testing.T, hs *refHarness, p refPath, before credentialpoolstore.CredentialPool) {
	t.Helper()
	if !p.needPool {
		if _, err := hs.store.Get(context.Background(), "acme", "new"); err == nil {
			t.Fatal("a refused pool create persisted the pool")
		}
		return
	}
	after := storedRow(t, hs, "p1")
	if after.Version != before.Version || !reflect.DeepEqual(after.Credentials, before.Credentials) {
		t.Fatalf("a refused write changed the row: before %+v after %+v", before, after)
	}
}

// spec: §4.9 (Credential Storage Topology) — Secret references rule 1: a
// foreign-namespace ref, a three-segment ref, and an invalid name are each
// refused with 400 VALIDATION_ERROR and details.field secretRef on every
// write path, with no probe call and no commit. The release-namespace
// form the tree accepted before the credentials namespace existed is one
// of the refused refs.
func TestSecretRefParseRefusals_spec_4_9(t *testing.T) {
	bad := []string{"lenny-system/anthropic-key-1", refTestNS + "/a/b", "Not_A_Name", "other-ns/x"}
	for _, p := range refPaths() {
		for _, ref := range bad {
			t.Run(p.name+"/"+ref, func(t *testing.T) {
				hs := newRefHarness(t)
				before := seedRefPool(t, hs)
				rr := p.submit(t, hs, withAdminPrincipal, entriesFor(p, ref))
				wantRefusal(t, rr, http.StatusBadRequest, "VALIDATION_ERROR", "secretRef")
				if len(hs.prober.calls) != 0 {
					t.Fatalf("probe calls = %v, want none", hs.prober.calls)
				}
				assertUnchanged(t, hs, p, before)
			})
		}
	}
}

// spec: §4.9 (Credential Storage Topology) — Secret references rule 2: a
// bare ref and a qualified ref are stored in the canonical form, and the
// probe sees the canonical form.
func TestSecretRefStoredCanonical_spec_4_9(t *testing.T) {
	for _, submitted := range []string{"k-new", refTestNS + "/k-new"} {
		for _, p := range refPaths() {
			if p.dryRun {
				continue
			}
			t.Run(p.name+"/"+submitted, func(t *testing.T) {
				hs := newRefHarness(t)
				seedRefPool(t, hs)
				rr := p.submit(t, hs, withAdminPrincipal, entriesFor(p, submitted))
				if rr.Code != http.StatusOK && rr.Code != http.StatusCreated {
					t.Fatalf("status = %d; body %s", rr.Code, rr.Body.String())
				}
				want := refTestNS + "/k-new"
				if len(hs.prober.calls) != 1 || hs.prober.calls[0] != want {
					t.Fatalf("probe calls = %v, want [%s]", hs.prober.calls, want)
				}
				name, id := "p1", "key-3"
				if !p.needPool {
					name = "new"
				}
				if p.name == "credential update" {
					id = "key-1"
				}
				found := false
				for _, c := range storedRow(t, hs, name).Credentials {
					if c.ID == id {
						found = true
						if c.SecretRef != want {
							t.Fatalf("stored ref = %q, want %q", c.SecretRef, want)
						}
					}
					if c.SecretRef == "existing-1" {
						t.Fatalf("a bare ref was stored: %+v", c)
					}
				}
				if !found {
					t.Fatalf("credential %s not stored", id)
				}
			})
		}
	}
}

// spec: §4.9 (Credential Storage Topology) — a ref the row stores,
// resubmitted in bare form, is the same canonical ref and so no
// introduction: a tenant-admin pool PUT and credential update pass the
// registration rule with no probe call.
func TestSecretRefBareResubmitIsNoChange_spec_4_9(t *testing.T) {
	hs := newRefHarness(t)
	row := seedRefPool(t, hs)
	body := poolBody("p1", []admin.CredentialEntryPayload{
		{ID: "key-1", SecretRef: "existing-1"},
		{ID: "key-2", SecretRef: credential.ManagedSecretName("acme", "p1", "key-2")},
	})
	rr := sendRef(t, hs, http.MethodPut, "/v1/admin/credential-pools/p1", body, withTenantAdminPrincipal, etagOf(row.Version))
	if rr.Code != http.StatusOK {
		t.Fatalf("tenant-admin PUT resubmitting stored refs: status %d; body %s", rr.Code, rr.Body.String())
	}
	rr = sendRef(t, hs, http.MethodPut, "/v1/admin/credential-pools/p1/credentials/key-1",
		admin.CredentialEntryPayload{SecretRef: "existing-1"}, withTenantAdminPrincipal, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("tenant-admin credential update resubmitting its ref: status %d; body %s", rr.Code, rr.Body.String())
	}
	if len(hs.prober.calls) != 0 {
		t.Fatalf("probe calls = %v, want none", hs.prober.calls)
	}
	if got := storedRow(t, hs, "p1").Credentials; got[0].SecretRef != refTestNS+"/existing-1" || got[1].SecretRef != managedRef("p1", "key-2") {
		t.Fatalf("stored refs = %+v, want the canonical forms", got)
	}
}

// spec: §15.1 — the pool PUT applies the If-Match presence and format
// checks before any store read: a missing If-Match against a missing pool
// is 428 rather than 404, and a malformed header is 400.
func TestCredentialPoolPutIfMatchBeforeRead_spec_15_1(t *testing.T) {
	hs := newRefHarness(t)
	rr := sendRef(t, hs, http.MethodPut, "/v1/admin/credential-pools/ghost", poolBody("ghost", nil), withAdminPrincipal, "")
	wantRefusal(t, rr, http.StatusPreconditionRequired, "ETAG_REQUIRED", "")
	seedRefPool(t, hs)
	rr = sendRef(t, hs, http.MethodPut, "/v1/admin/credential-pools/p1", poolBody("p1", nil), withAdminPrincipal, "W/\"1\"")
	wantRefusal(t, rr, http.StatusBadRequest, "VALIDATION_ERROR", "")
	// An invalid ref and a missing If-Match on one request: the ref parse
	// runs first.
	rr = sendRef(t, hs, http.MethodPut, "/v1/admin/credential-pools/p1",
		poolBody("p1", []admin.CredentialEntryPayload{{ID: "key-1", SecretRef: "lenny-system/x"}}), withAdminPrincipal, "")
	wantRefusal(t, rr, http.StatusBadRequest, "VALIDATION_ERROR", "secretRef")
}

// spec: §4.9 (Credential Storage Topology) — Registering a Secret by
// reference: a tenant-admin and a custom role holding
// manage_credential_pools each get 403 FORBIDDEN for an introduced ref,
// whether or not the Secret exists, with no probe call and no commit. A
// platform-admin introduction passes.
func TestSecretRefRegistrationRequiresPlatformAdmin_spec_4_9(t *testing.T) {
	callers := map[string]func(*http.Request) *http.Request{
		"tenant-admin": withTenantAdminPrincipal,
		"custom role":  asCredAdmin,
	}
	for _, p := range refPaths() {
		for callerName, as := range callers {
			for _, verdict := range []admin.SecretProbeVerdict{admin.SecretProbeAllowed, admin.SecretProbeNotFound} {
				t.Run(p.name+"/"+callerName+"/"+strconv.Itoa(int(verdict)), func(t *testing.T) {
					hs := newRefHarness(t)
					before := seedRefPool(t, hs)
					hs.prober.verdicts[refTestNS+"/introduced"] = verdict
					rr := p.submit(t, hs, as, entriesFor(p, "introduced"))
					wantRefusal(t, rr, http.StatusForbidden, "FORBIDDEN", "")
					if len(hs.prober.calls) != 0 {
						t.Fatalf("probe calls = %v, want none for a refused caller", hs.prober.calls)
					}
					assertUnchanged(t, hs, p, before)
				})
			}
		}
		t.Run(p.name+"/platform-admin", func(t *testing.T) {
			hs := newRefHarness(t)
			seedRefPool(t, hs)
			rr := p.submit(t, hs, withAdminPrincipal, entriesFor(p, "introduced"))
			if rr.Code != http.StatusOK && rr.Code != http.StatusCreated {
				t.Fatalf("platform-admin introduction: status %d; body %s", rr.Code, rr.Body.String())
			}
		})
	}
}

// spec: §4.9 (Credential Storage Topology) — a ref another entry of the
// pool carries is not an introduction, so a tenant-admin may name it on
// another credential id with no probe call.
func TestSecretRefCarriedByAnotherEntryIsNotIntroduced_spec_4_9(t *testing.T) {
	for _, p := range refPaths() {
		if !p.needPool {
			continue
		}
		t.Run(p.name, func(t *testing.T) {
			hs := newRefHarness(t)
			seedRefPool(t, hs)
			if _, err := hs.store.Update(context.Background(), "acme", "p1", func(row *credentialpoolstore.CredentialPool) error {
				row.Credentials = append(row.Credentials, credentialpoolstore.Credential{ID: "key-4", SecretRef: refTestNS + "/shared"})
				return nil
			}); err != nil {
				t.Fatalf("add key-4: %v", err)
			}
			rr := p.submit(t, hs, withTenantAdminPrincipal, entriesFor(p, "shared"))
			if rr.Code != http.StatusOK && rr.Code != http.StatusCreated {
				t.Fatalf("status = %d; body %s", rr.Code, rr.Body.String())
			}
			if len(hs.prober.calls) != 0 {
				t.Fatalf("probe calls = %v, want none", hs.prober.calls)
			}
		})
	}
}

// spec: §4.9 (Credential Storage Topology) — Secret references rule 3: a
// lenny-cred- ref the same credential id does not carry is refused with
// 400 VALIDATION_ERROR and details.field secretRef, with no probe call,
// even from a platform-admin.
func TestReservedPrefixRefusedPerCredentialID_spec_4_9(t *testing.T) {
	foreign := credential.ManagedSecretName("globex", "other", "key-9")
	for _, p := range refPaths() {
		t.Run(p.name, func(t *testing.T) {
			hs := newRefHarness(t)
			before := seedRefPool(t, hs)
			hs.prober.verdicts[refTestNS+"/"+foreign] = admin.SecretProbeNotFound
			rr := p.submit(t, hs, withAdminPrincipal, entriesFor(p, foreign))
			wantRefusal(t, rr, http.StatusBadRequest, "VALIDATION_ERROR", "secretRef")
			if len(hs.prober.calls) != 0 {
				t.Fatalf("probe calls = %v, want none for a reserved ref", hs.prober.calls)
			}
			assertUnchanged(t, hs, p, before)
		})
	}
}

// spec: §4.9 (Credential Storage Topology) — Secret references rule 3 is
// per credential id: a pool PUT that moves entry key-2's Lenny-written ref
// onto key-1 is refused, although the pool-wide set of refs is unchanged.
func TestReservedPrefixMoveBetweenEntriesRefused_spec_4_9(t *testing.T) {
	for _, dry := range []bool{false, true} {
		hs := newRefHarness(t)
		row := seedRefPool(t, hs)
		body := poolBody("p1", []admin.CredentialEntryPayload{
			{ID: "key-1", SecretRef: managedRef("p1", "key-2")},
			{ID: "key-2", SecretRef: "existing-1"},
		})
		rr := sendRef(t, hs, http.MethodPut, "/v1/admin/credential-pools/p1"+dryRunSuffix(dry, "?"), body, withAdminPrincipal, etagOf(row.Version))
		wantRefusal(t, rr, http.StatusBadRequest, "VALIDATION_ERROR", "secretRef")
		if after := storedRow(t, hs, "p1"); after.Version != row.Version {
			t.Fatalf("dryRun=%v: the refused move committed", dry)
		}
	}
	// The credential update path: key-1 asks for key-2's written ref.
	hs := newRefHarness(t)
	seedRefPool(t, hs)
	rr := sendRef(t, hs, http.MethodPut, "/v1/admin/credential-pools/p1/credentials/key-1?tenantId=acme",
		admin.CredentialEntryPayload{SecretRef: managedRef("p1", "key-2")}, withAdminPrincipal, "")
	wantRefusal(t, rr, http.StatusBadRequest, "VALIDATION_ERROR", "secretRef")
}

// spec: §4.9 (Credential Storage Topology) — an entry's own Lenny-written
// ref resubmitted in bare form passes rule 3.
func TestReservedPrefixOwnRefResubmittedPasses_spec_4_9(t *testing.T) {
	hs := newRefHarness(t)
	row := seedRefPool(t, hs)
	body := poolBody("p1", []admin.CredentialEntryPayload{
		{ID: "key-1", SecretRef: "existing-1"},
		{ID: "key-2", SecretRef: credential.ManagedSecretName("acme", "p1", "key-2")},
	})
	rr := sendRef(t, hs, http.MethodPut, "/v1/admin/credential-pools/p1", body, withTenantAdminPrincipal, etagOf(row.Version))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d; body %s", rr.Code, rr.Body.String())
	}
	rr = sendRef(t, hs, http.MethodPut, "/v1/admin/credential-pools/p1/credentials/key-2",
		admin.CredentialEntryPayload{SecretRef: credential.ManagedSecretName("acme", "p1", "key-2")}, withTenantAdminPrincipal, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("credential update: status = %d; body %s", rr.Code, rr.Body.String())
	}
}

// spec: §4.9 (Credential Storage Topology) — Admin-time Secret probe
// (required); §15.1 error catalog. A platform-admin introduction answered
// NOT_FOUND or DENIED gets 422 CREDENTIAL_SECRET_RBAC_MISSING with only
// details.missingSecrets, and one with no verdict gets 503
// CREDENTIAL_PROBE_UNAVAILABLE with only details.secretRef. Each makes one
// probe call per introduced ref, and a dryRun request makes none.
func TestSecretProbeOnIntroducedRefs_spec_4_9(t *testing.T) {
	type outcome struct {
		name    string
		verdict admin.SecretProbeVerdict
		err     error
		status  int
		code    string
		key     string
	}
	outcomes := []outcome{
		{"not found", admin.SecretProbeNotFound, nil, http.StatusUnprocessableEntity, "CREDENTIAL_SECRET_RBAC_MISSING", "missingSecrets"},
		{"denied", admin.SecretProbeDenied, nil, http.StatusUnprocessableEntity, "CREDENTIAL_SECRET_RBAC_MISSING", "missingSecrets"},
		{"no verdict", 0, errors.New("token service unreachable"), http.StatusServiceUnavailable, "CREDENTIAL_PROBE_UNAVAILABLE", "secretRef"},
	}
	for _, p := range refPaths() {
		if p.dryRun && strings.HasPrefix(p.name, "credential") {
			// The credential add and update dryRun previews land with
			// the secretData write path.
			continue
		}
		for _, o := range outcomes {
			t.Run(p.name+"/"+o.name, func(t *testing.T) {
				hs := newRefHarness(t)
				before := seedRefPool(t, hs)
				hs.prober.verdicts[refTestNS+"/introduced"] = o.verdict
				hs.prober.err = o.err
				rr := p.submit(t, hs, withAdminPrincipal, entriesFor(p, "introduced"))
				if p.dryRun {
					if len(hs.prober.calls) != 0 {
						t.Fatalf("dryRun probe calls = %v, want none", hs.prober.calls)
					}
					return
				}
				got := wantRefusal(t, rr, o.status, o.code, "")
				if len(got.Details) != 1 || got.Details[o.key] == nil {
					t.Fatalf("details = %v, want only %s", got.Details, o.key)
				}
				if _, ok := got.Details["remediation"]; ok {
					t.Fatal("details carry a remediation key")
				}
				if len(hs.prober.calls) != 1 || hs.prober.calls[0] != refTestNS+"/introduced" {
					t.Fatalf("probe calls = %v, want one for the introduced ref", hs.prober.calls)
				}
				assertUnchanged(t, hs, p, before)
			})
		}
	}
}

// spec: §4.9 (Credential Storage Topology) — the probe runs once per
// distinct introduced ref and not for refs the pool already carries.
func TestSecretProbeOncePerIntroducedRef_spec_4_9(t *testing.T) {
	hs := newRefHarness(t)
	row := seedRefPool(t, hs)
	body := poolBody("p1", []admin.CredentialEntryPayload{
		{ID: "key-1", SecretRef: "existing-1"},
		{ID: "key-2", SecretRef: managedRef("p1", "key-2")},
		{ID: "key-3", SecretRef: "a"},
		{ID: "key-4", SecretRef: refTestNS + "/a"},
		{ID: "key-5", SecretRef: "b"},
	})
	rr := sendRef(t, hs, http.MethodPut, "/v1/admin/credential-pools/p1", body, withAdminPrincipal, etagOf(row.Version))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d; body %s", rr.Code, rr.Body.String())
	}
	want := []string{refTestNS + "/a", refTestNS + "/b"}
	if !reflect.DeepEqual(hs.prober.calls, want) {
		t.Fatalf("probe calls = %v, want %v", hs.prober.calls, want)
	}
}

// spec: §4.9 (Credential Storage Topology) — a request that fails two
// consecutive checks receives the earlier check's code, and no later check
// makes a probe call or a commit.
func TestSecretRefCheckOrder_spec_4_9(t *testing.T) {
	for _, p := range refPaths() {
		// Parse before registration: a tenant-admin sends an invalid ref.
		t.Run(p.name+"/parse before registration", func(t *testing.T) {
			hs := newRefHarness(t)
			before := seedRefPool(t, hs)
			rr := p.submit(t, hs, withTenantAdminPrincipal, entriesFor(p, "lenny-system/x"))
			wantRefusal(t, rr, http.StatusBadRequest, "VALIDATION_ERROR", "secretRef")
			assertUnchanged(t, hs, p, before)
		})
		// Registration before the reserved prefix: a tenant-admin sends a
		// reserved ref the pool does not carry.
		t.Run(p.name+"/registration before prefix", func(t *testing.T) {
			hs := newRefHarness(t)
			before := seedRefPool(t, hs)
			rr := p.submit(t, hs, withTenantAdminPrincipal, entriesFor(p, credential.ManagedSecretName("acme", "p1", "key-9")))
			wantRefusal(t, rr, http.StatusForbidden, "FORBIDDEN", "")
			if len(hs.prober.calls) != 0 {
				t.Fatalf("probe calls = %v", hs.prober.calls)
			}
			assertUnchanged(t, hs, p, before)
		})
		// The reserved prefix before the probe is covered by
		// TestReservedPrefixRefusedPerCredentialID_spec_4_9, whose prober
		// answers NOT_FOUND for the reserved ref.
	}
	// If-Match before registration on a pool PUT: a stale ETag and a
	// tenant-admin introduction get 412.
	hs := newRefHarness(t)
	row := seedRefPool(t, hs)
	rr := sendRef(t, hs, http.MethodPut, "/v1/admin/credential-pools/p1",
		poolBody("p1", []admin.CredentialEntryPayload{{ID: "key-3", SecretRef: "introduced"}}),
		withTenantAdminPrincipal, etagOf(row.Version+5))
	wantRefusal(t, rr, http.StatusPreconditionFailed, "ETAG_MISMATCH", "")
	if len(hs.prober.calls) != 0 {
		t.Fatalf("probe calls = %v", hs.prober.calls)
	}
}

// spec: §4.9 (Credential Storage Topology), §10.2 — the introduced set is
// decided against the row the write holds locked. A concurrent write
// removes key-1's ref between the client's read and the PUT. A tenant-admin
// resubmitting the pool's refs, with the ETag of the version the concurrent
// write produced, gets 403, and a platform-admin doing the same makes one
// probe call for that ref and gets 422 on NOT_FOUND with the row
// unchanged.
func TestLockedRegistrationSeesConcurrentRemoval_spec_4_9(t *testing.T) {
	cases := []struct {
		name   string
		as     func(*http.Request) *http.Request
		status int
		probes int
	}{
		{"tenant-admin", withTenantAdminPrincipal, http.StatusForbidden, 0},
		{"platform-admin", withAdminPrincipal, http.StatusUnprocessableEntity, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hs := newRefHarness(t)
			row := seedRefPool(t, hs)
			hs.prober.verdicts[refTestNS+"/existing-1"] = admin.SecretProbeNotFound
			var concurrent credentialpoolstore.CredentialPool
			hs.store.beforeUpdate = func(ctx context.Context, m *credentialpoolstore.Memory) {
				var err error
				concurrent, err = m.Update(ctx, "acme", "p1", func(p *credentialpoolstore.CredentialPool) error {
					p.Credentials = p.Credentials[1:]
					return nil
				})
				if err != nil {
					t.Errorf("concurrent write: %v", err)
				}
			}
			body := poolBody("p1", []admin.CredentialEntryPayload{
				{ID: "key-1", SecretRef: "existing-1"},
				{ID: "key-2", SecretRef: managedRef("p1", "key-2")},
			})
			rr := sendRef(t, hs, http.MethodPut, "/v1/admin/credential-pools/p1", body, tc.as, etagOf(row.Version+1))
			if rr.Code != tc.status {
				t.Fatalf("status = %d, want %d; body %s", rr.Code, tc.status, rr.Body.String())
			}
			if len(hs.prober.calls) != tc.probes {
				t.Fatalf("probe calls = %v, want %d", hs.prober.calls, tc.probes)
			}
			after := storedRow(t, hs, "p1")
			if after.Version != concurrent.Version || !reflect.DeepEqual(after.Credentials, concurrent.Credentials) {
				t.Fatalf("row = %+v, want the concurrent write's row %+v", after, concurrent)
			}
		})
	}
}

// spec: §15.1, §4.9 (Credential Storage Topology) — the pool PUT compares
// If-Match against the locked row. The request carries the row's current
// ETag, a concurrent write bumps the version before the locked Update, and
// the request gets 412 ETAG_MISMATCH naming the new ETag.
func TestLockedIfMatchSeesConcurrentWrite_spec_15_1(t *testing.T) {
	hs := newRefHarness(t)
	row := seedRefPool(t, hs)
	hs.store.beforeUpdate = func(ctx context.Context, m *credentialpoolstore.Memory) {
		if _, err := m.Update(ctx, "acme", "p1", func(p *credentialpoolstore.CredentialPool) error {
			p.MaxConcurrentSessions = 11
			return nil
		}); err != nil {
			t.Errorf("concurrent write: %v", err)
		}
	}
	body := poolBody("p1", []admin.CredentialEntryPayload{{ID: "key-1", SecretRef: "existing-1"}})
	body.MaxConcurrentSessions = 25
	rr := sendRef(t, hs, http.MethodPut, "/v1/admin/credential-pools/p1", body, withAdminPrincipal, etagOf(row.Version))
	got := wantRefusal(t, rr, http.StatusPreconditionFailed, "ETAG_MISMATCH", "")
	if got.Details["currentEtag"] != etagOf(row.Version+1) {
		t.Fatalf("currentEtag = %v, want %s", got.Details["currentEtag"], etagOf(row.Version+1))
	}
	if after := storedRow(t, hs, "p1"); after.MaxConcurrentSessions != 11 || len(after.Credentials) != 2 {
		t.Fatalf("the stale PUT committed: %+v", after)
	}
}

// spec: §4.9 (Credential Storage Topology) — a commit failure after the
// checks pass leaves the row unwritten and is not reported as success.
func TestCredentialAddCommitFailureLeavesRow_spec_4_9(t *testing.T) {
	hs := newRefHarness(t)
	before := seedRefPool(t, hs)
	hs.store.failCommit = errors.New("commit failed")
	rr := sendRef(t, hs, http.MethodPost, "/v1/admin/credential-pools/p1/credentials?tenantId=acme",
		admin.CredentialEntryPayload{ID: "key-3", SecretRef: "introduced"}, withAdminPrincipal, "")
	if rr.Code < 400 {
		t.Fatalf("status = %d, want a refusal", rr.Code)
	}
	assertUnchanged(t, hs, refPath{needPool: true}, before)
}

// seedBody returns the bootstrap request for acme/p1 with the given
// entries and the seeded pool's other fields.
func seedBody(entries []admin.CredentialEntryPayload, maxSessions int) admin.BootstrapRequest {
	p := poolBody("p1", entries)
	p.MaxConcurrentSessions = maxSessions
	return admin.BootstrapRequest{CredentialPools: []admin.CredentialPoolPayload{p, poolBody("other", nil)}}
}

func runSeed(t *testing.T, hs *refHarness, query string, body admin.BootstrapRequest) admin.BootstrapSection {
	t.Helper()
	rr := sendRef(t, hs, http.MethodPost, "/v1/admin/bootstrap"+query, body, withAdminPrincipal, "")
	var resp admin.BootstrapResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode bootstrap response: %v (%s)", err, rr.Body.String())
	}
	return resp.CredentialPools
}

// seedEntryCode returns the error code the seed recorded for entry name,
// or "" when it recorded none.
func seedEntryCode(s admin.BootstrapSection, name string) string {
	for _, e := range s.Errors {
		if e.ID == name {
			return e.Code
		}
	}
	return ""
}

// spec: §4.9 (Credential Storage Topology), §17.6 — the bootstrap seed
// canonicalizes refs, reports a ref rule 1 or rule 3 refuses as
// SEED_VALIDATION on that pool entry with the seed's other entries
// applied, is exempt from the registration rule, and makes no probe call.
func TestBootstrapSeedSecretRefRules_spec_4_9(t *testing.T) {
	foreign := credential.ManagedSecretName("globex", "x", "y")
	type seedCase struct {
		name     string
		present  bool
		query    string
		entries  []admin.CredentialEntryPayload
		maxSess  int
		wantCode string
		check    func(t *testing.T, hs *refHarness, s admin.BootstrapSection)
	}
	cases := []seedCase{
		{name: "absent pool, invalid ref", entries: []admin.CredentialEntryPayload{{ID: "key-1", SecretRef: "lenny-system/x"}}, wantCode: "SEED_VALIDATION"},
		{name: "absent pool, reserved ref", entries: []admin.CredentialEntryPayload{{ID: "key-1", SecretRef: foreign}}, wantCode: "SEED_VALIDATION"},
		{
			name: "absent pool, introduced ref stored canonical", entries: []admin.CredentialEntryPayload{{ID: "key-1", SecretRef: "operator-made"}},
			check: func(t *testing.T, hs *refHarness, s admin.BootstrapSection) {
				if got := storedRow(t, hs, "p1").Credentials[0].SecretRef; got != refTestNS+"/operator-made" {
					t.Fatalf("stored ref = %q", got)
				}
			},
		},
		{name: "present pool, not written, invalid ref", present: true, entries: []admin.CredentialEntryPayload{{ID: "key-1", SecretRef: "a/b/c"}}, maxSess: 10, wantCode: "SEED_VALIDATION"},
		{name: "present pool, not written, reserved ref", present: true, entries: []admin.CredentialEntryPayload{{ID: "key-1", SecretRef: foreign}}, maxSess: 10, wantCode: "SEED_VALIDATION"},
		{
			name: "present pool, bare resubmit is no conflict", present: true, maxSess: 10,
			entries: []admin.CredentialEntryPayload{{ID: "key-1", SecretRef: "existing-1"}, {ID: "key-2", SecretRef: credential.ManagedSecretName("acme", "p1", "key-2")}},
			check: func(t *testing.T, hs *refHarness, s admin.BootstrapSection) {
				for _, sk := range s.Skipped {
					if sk.ID == "p1" && len(sk.ConflictingFields) > 0 {
						t.Fatalf("bare resubmit reported conflicts %v", sk.ConflictingFields)
					}
				}
			},
		},
		{
			name: "force-update dryRun, reserved ref", present: true, query: "?forceUpdate=true&dryRun=true", maxSess: 20,
			entries: []admin.CredentialEntryPayload{{ID: "key-1", SecretRef: foreign}}, wantCode: "SEED_VALIDATION",
		},
		{
			name: "force-update, reserved ref", present: true, query: "?forceUpdate=true", maxSess: 20,
			entries: []admin.CredentialEntryPayload{{ID: "key-1", SecretRef: foreign}}, wantCode: "SEED_VALIDATION",
		},
		{
			name: "force-update, moved written ref", present: true, query: "?forceUpdate=true", maxSess: 20,
			entries: []admin.CredentialEntryPayload{{ID: "key-1", SecretRef: managedRef("p1", "key-2")}}, wantCode: "SEED_VALIDATION",
		},
		{
			name: "force-update, introduced ref needs no platform check", present: true, query: "?forceUpdate=true", maxSess: 20,
			entries: []admin.CredentialEntryPayload{{ID: "key-1", SecretRef: "new-operator"}, {ID: "key-2", SecretRef: managedRef("p1", "key-2")}},
			check: func(t *testing.T, hs *refHarness, s admin.BootstrapSection) {
				row := storedRow(t, hs, "p1")
				if row.Credentials[0].SecretRef != refTestNS+"/new-operator" || row.MaxConcurrentSessions != 20 {
					t.Fatalf("forced update not applied: %+v", row)
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hs := newRefHarness(t)
			var before credentialpoolstore.CredentialPool
			if tc.present {
				before = seedRefPool(t, hs)
			}
			s := runSeed(t, hs, tc.query, seedBody(tc.entries, tc.maxSess))
			if got := seedEntryCode(s, "p1"); got != tc.wantCode {
				t.Fatalf("p1 entry code = %q, want %q; section %+v", got, tc.wantCode, s)
			}
			if seedEntryCode(s, "other") != "" {
				t.Fatalf("a refusal on p1 refused the other entry: %+v", s)
			}
			if len(hs.prober.calls) != 0 {
				t.Fatalf("the seed made probe calls %v", hs.prober.calls)
			}
			if tc.wantCode != "" {
				if tc.present {
					assertUnchanged(t, hs, refPath{needPool: true}, before)
				} else if _, err := hs.store.Get(context.Background(), "acme", "p1"); err == nil {
					t.Fatal("a refused seed entry created its pool")
				}
			}
			if tc.check != nil {
				tc.check(t, hs, s)
			}
		})
	}
}

// spec: §4.9 (Credential Storage Topology) — on a forced seed the reserved
// prefix runs against the locked row. A concurrent write removes key-2's
// written ref after the seed's read, and the forced update that resubmits
// it is refused with SEED_VALIDATION rather than the store-error code a
// closure error would otherwise take.
func TestBootstrapForcedSeedReservedPrefixLocked_spec_4_9(t *testing.T) {
	hs := newRefHarness(t)
	seedRefPool(t, hs)
	hs.store.beforeUpdate = func(ctx context.Context, m *credentialpoolstore.Memory) {
		if _, err := m.Update(ctx, "acme", "p1", func(p *credentialpoolstore.CredentialPool) error {
			p.Credentials = p.Credentials[:1]
			return nil
		}); err != nil {
			t.Errorf("concurrent write: %v", err)
		}
	}
	s := runSeed(t, hs, "?forceUpdate=true", seedBody([]admin.CredentialEntryPayload{
		{ID: "key-1", SecretRef: "existing-1"},
		{ID: "key-2", SecretRef: managedRef("p1", "key-2")},
	}, 20))
	if got := seedEntryCode(s, "p1"); got != "SEED_VALIDATION" {
		t.Fatalf("p1 entry code = %q, want SEED_VALIDATION; section %+v", got, s)
	}
	if row := storedRow(t, hs, "p1"); len(row.Credentials) != 1 || row.MaxConcurrentSessions != 10 {
		t.Fatalf("the refused forced update committed: %+v", row)
	}
}
