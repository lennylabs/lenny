// SPDX-License-Identifier: MIT

package admin_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"

	pkgauth "github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credentialpoolstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/customrolestore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
)

// These tests pin the Lenny-written credential Secret path of the admin
// credential-pool writes and the bootstrap seed: the secretData check, the
// soft-deleted row guard, the explicit Validate ahead of any Secret
// operation, the no-writer refusal, and the Put and Delete each write
// performs under the row lock as its last step. Each case asserts the
// response, the writer calls, and the committed row.

// writerCall is one call the fake writer received.
type writerCall struct {
	op    string
	name  string
	owner admin.SecretOwner
	data  map[string]string
}

// fakeCredentialSecretWriter records every call, keeps the Secrets it
// holds, and fails Put or Delete when the matching fault is set.
type fakeCredentialSecretWriter struct {
	mu         sync.Mutex
	calls      []writerCall
	secrets    map[string]map[string]string
	failPut    error
	failDelete error
}

func newFakeWriter() *fakeCredentialSecretWriter {
	return &fakeCredentialSecretWriter{secrets: map[string]map[string]string{}}
}

func (f *fakeCredentialSecretWriter) Put(_ context.Context, name string, meta admin.SecretOwner, data map[string]string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, writerCall{op: "put", name: name, owner: meta, data: data})
	if f.failPut != nil {
		return f.failPut
	}
	f.secrets[name] = data
	return nil
}

func (f *fakeCredentialSecretWriter) Delete(_ context.Context, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, writerCall{op: "delete", name: name})
	if f.failDelete != nil {
		return f.failDelete
	}
	delete(f.secrets, name)
	return nil
}

func (f *fakeCredentialSecretWriter) DeleteTenant(_ context.Context, tenant string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, writerCall{op: "deleteTenant", name: tenant})
	return nil
}

func (f *fakeCredentialSecretWriter) snapshot() []writerCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]writerCall(nil), f.calls...)
}

// asPoolRequest converts a pool payload to its request form with no
// secretData on any entry.
func asPoolRequest(p admin.CredentialPoolPayload) admin.CredentialPoolRequest {
	out := admin.CredentialPoolRequest{CredentialPoolPayload: p}
	for _, e := range p.Credentials {
		out.Credentials = append(out.Credentials, admin.CredentialEntryRequest{CredentialEntryPayload: e})
	}
	return out
}

// dataHarness is a refHarness whose Router carries writer, or no writer
// when writer is nil, and records audit events.
type dataHarness struct {
	*refHarness
	writer *fakeCredentialSecretWriter
	audit  *recordingAudit
}

func newDataHarness(t *testing.T, withWriter bool) *dataHarness {
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
	audit := &recordingAudit{}
	router := admin.NewRouter(tenantstore.NewMemory(), admin.Options{
		Clock:                func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		CredentialsNamespace: refTestNS,
		Audit:                audit,
	}).WithCredentialPools(store).WithSecretAccessProber(prober).WithCustomRoles(roles)
	dh := &dataHarness{audit: audit}
	if withWriter {
		dh.writer = newFakeWriter()
		router = router.WithCredentialSecretWriter(dh.writer)
	}
	dh.refHarness = &refHarness{h: router.Handler(), store: store, prober: prober}
	return dh
}

func (dh *dataHarness) calls() []writerCall {
	if dh.writer == nil {
		return nil
	}
	return dh.writer.snapshot()
}

// managedName is the Secret name the gateway writes for (acme, pool, id).
func managedName(pool, id string) string { return credential.ManagedSecretName("acme", pool, id) }

// dataEntry is a credential addition or update body carrying secretData.
func dataEntry(id string, data map[string]string) admin.CredentialEntryRequest {
	return admin.CredentialEntryRequest{CredentialEntryPayload: admin.CredentialEntryPayload{ID: id}, SecretData: data}
}

const (
	addPath    = "/v1/admin/credential-pools/p1/credentials?tenantId=acme"
	updatePath = "/v1/admin/credential-pools/p1/credentials/%s?tenantId=acme"
)

func updatePathFor(id string) string { return strings.Replace(updatePath, "%s", id, 1) }

func softDelete(t *testing.T, dh *dataHarness) {
	t.Helper()
	if err := dh.store.SoftDelete(context.Background(), "acme", "p1", time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("soft delete: %v", err)
	}
}

func wantNoWriterCalls(t *testing.T, dh *dataHarness) {
	t.Helper()
	if c := dh.calls(); len(c) != 0 {
		t.Fatalf("writer calls = %+v, want none", c)
	}
}

func wantCalls(t *testing.T, dh *dataHarness, want ...writerCall) {
	t.Helper()
	got := dh.calls()
	if len(got) != len(want) {
		t.Fatalf("writer calls = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i].op != want[i].op || got[i].name != want[i].name {
			t.Fatalf("writer call %d = %s %s, want %s %s", i, got[i].op, got[i].name, want[i].op, want[i].name)
		}
		if want[i].op == "put" && (got[i].owner != want[i].owner || !reflect.DeepEqual(got[i].data, want[i].data)) {
			t.Fatalf("put %d owner/data = %+v %v, want %+v %v", i, got[i].owner, got[i].data, want[i].owner, want[i].data)
		}
	}
}

func putCall(id string, data map[string]string) writerCall {
	return writerCall{op: "put", name: managedName("p1", id), owner: admin.SecretOwner{TenantID: "acme", Pool: "p1", CredentialID: id}, data: data}
}

func deleteCall(id string) writerCall { return writerCall{op: "delete", name: managedName("p1", id)} }

// wantRowUnchanged asserts acme/p1 kept its version and credentials.
func wantRowUnchanged(t *testing.T, dh *dataHarness, before credentialpoolstore.CredentialPool) {
	t.Helper()
	assertUnchanged(t, dh.refHarness, refPath{needPool: true}, before)
}

// spec: §4.9 (Credential Storage Topology) — Credential values submitted
// through the admin API rule 1: pool creation and pool update refuse
// secretData with 400 VALIDATION_ERROR and details.field secretData, with
// and without dryRun, and change nothing.
func TestSecretDataRefusedOnPoolWrites_spec_4_9(t *testing.T) {
	entries := []admin.CredentialEntryRequest{dataEntry("key-3", map[string]string{"apiKey": "sk-ant-x"})}
	for _, dry := range []bool{false, true} {
		t.Run("pool create"+dryRunSuffix(dry, " "), func(t *testing.T) {
			dh := newDataHarness(t, true)
			body := asPoolRequest(poolBody("new", nil))
			body.Credentials = entries
			rr := sendRef(t, dh.refHarness, http.MethodPost, "/v1/admin/credential-pools"+dryRunSuffix(dry, "?"), body, withAdminPrincipal, "")
			wantRefusal(t, rr, http.StatusBadRequest, "VALIDATION_ERROR", "secretData")
			assertUnchanged(t, dh.refHarness, refPath{}, credentialpoolstore.CredentialPool{})
			wantNoWriterCalls(t, dh)
		})
		t.Run("pool PUT"+dryRunSuffix(dry, " "), func(t *testing.T) {
			dh := newDataHarness(t, true)
			before := seedRefPool(t, dh.refHarness)
			body := asPoolRequest(poolBody("p1", nil))
			body.Credentials = entries
			rr := sendRef(t, dh.refHarness, http.MethodPut, "/v1/admin/credential-pools/p1"+dryRunSuffix(dry, "?"), body, withAdminPrincipal, etagOf(before.Version))
			wantRefusal(t, rr, http.StatusBadRequest, "VALIDATION_ERROR", "secretData")
			wantRowUnchanged(t, dh, before)
			wantNoWriterCalls(t, dh)
		})
	}
}

// spec: §4.9 (Credential Storage Topology) — Credential values submitted
// through the admin API rule 1 on credential addition and update, with and
// without dryRun: more than one of secretRef, secretData, and roleArn is
// 400; an empty secretData, an invalid key, and values one byte over
// corev1.MaxSecretSize are 400 with details.field secretData; values
// totalling exactly the limit pass.
func TestSecretDataBodyChecks_spec_4_9(t *testing.T) {
	big := strings.Repeat("a", corev1.MaxSecretSize)
	refusals := []struct {
		name  string
		body  admin.CredentialEntryRequest
		field string
	}{
		{"secretRef and secretData", admin.CredentialEntryRequest{CredentialEntryPayload: admin.CredentialEntryPayload{SecretRef: "existing-1"}, SecretData: map[string]string{"apiKey": "v"}}, ""},
		{"roleArn and secretData", admin.CredentialEntryRequest{CredentialEntryPayload: admin.CredentialEntryPayload{RoleArn: "arn:aws:iam::1:role/r"}, SecretData: map[string]string{"apiKey": "v"}}, ""},
		{"secretRef and roleArn", admin.CredentialEntryRequest{CredentialEntryPayload: admin.CredentialEntryPayload{SecretRef: "existing-1", RoleArn: "arn:aws:iam::1:role/r"}}, ""},
		{"empty secretData", dataEntry("", map[string]string{}), "secretData"},
		{"invalid key", dataEntry("", map[string]string{"bad key/": "v"}), "secretData"},
		{"one byte over the limit", dataEntry("", map[string]string{"apiKey": big, "extra": "b"}), "secretData"},
	}
	type path struct {
		name, method, url, id string
	}
	paths := []path{
		{"credential add", http.MethodPost, addPath, "key-3"},
		{"credential add dryRun", http.MethodPost, addPath + "&dryRun=true", "key-3"},
		{"credential update", http.MethodPut, updatePathFor("key-1"), "key-1"},
		{"credential update dryRun", http.MethodPut, updatePathFor("key-1") + "&dryRun=true", "key-1"},
	}
	for _, p := range paths {
		for _, rc := range refusals {
			t.Run(p.name+"/"+rc.name, func(t *testing.T) {
				dh := newDataHarness(t, true)
				before := seedRefPool(t, dh.refHarness)
				body := rc.body
				body.ID = p.id
				var payload any = body
				if body.SecretData != nil && len(body.SecretData) == 0 {
					// omitempty drops an empty map, so the empty object is
					// sent as raw JSON.
					payload = map[string]any{"id": p.id, "secretData": map[string]string{}}
				}
				rr := sendRef(t, dh.refHarness, p.method, p.url, payload, withAdminPrincipal, "")
				got := wantRefusal(t, rr, http.StatusBadRequest, "VALIDATION_ERROR", rc.field)
				if rc.field == "" && got.Details["field"] != nil {
					t.Fatalf("details.field = %v, want none", got.Details["field"])
				}
				if strings.Contains(rr.Body.String(), "sk-ant") || strings.Contains(rr.Body.String(), big[:64]) {
					t.Fatalf("the refusal carries a submitted value: %s", rr.Body.String())
				}
				wantRowUnchanged(t, dh, before)
				wantNoWriterCalls(t, dh)
				if len(dh.prober.calls) != 0 {
					t.Fatalf("probe calls = %v, want none", dh.prober.calls)
				}
			})
		}
		t.Run(p.name+"/exactly the limit", func(t *testing.T) {
			dh := newDataHarness(t, true)
			seedRefPool(t, dh.refHarness)
			rr := sendRef(t, dh.refHarness, p.method, p.url, dataEntry(p.id, map[string]string{"apiKey": big}), withAdminPrincipal, "")
			if rr.Code >= 300 {
				t.Fatalf("status = %d, want success; body %.200s", rr.Code, rr.Body.String())
			}
		})
	}
}

// spec: §4.9 (Credential Storage Topology) — Credential values submitted
// through the admin API rules 2, 3, and 4: a credential addition carrying
// secretData Puts the Secret rule 2 names with the owner and the submitted
// data, and stores the canonical reference to it; a tenant-admin may do so,
// because a written Secret is not an introduction. The response and the
// audit event carry no value.
func TestSecretDataAddPutsManagedSecret_spec_4_9(t *testing.T) {
	data := map[string]string{"apiKey": "sk-ant-secret-value"}
	for _, as := range []struct {
		name string
		fn   func(*http.Request) *http.Request
	}{{"platform-admin", withAdminPrincipal}, {"tenant-admin", withTenantAdminPrincipal}} {
		t.Run(as.name, func(t *testing.T) {
			dh := newDataHarness(t, true)
			seedRefPool(t, dh.refHarness)
			rr := sendRef(t, dh.refHarness, http.MethodPost, addPath, dataEntry("key-3", data), as.fn, "")
			if rr.Code != http.StatusCreated {
				t.Fatalf("status = %d, want 201; body %s", rr.Code, rr.Body.String())
			}
			wantCalls(t, dh, putCall("key-3", data))
			row := storedRow(t, dh.refHarness, "p1")
			if got := row.Credentials[len(row.Credentials)-1]; got.ID != "key-3" || got.SecretRef != managedRef("p1", "key-3") {
				t.Fatalf("stored entry = %+v, want key-3 on %s", got, managedRef("p1", "key-3"))
			}
			if strings.Contains(rr.Body.String(), "sk-ant-secret-value") {
				t.Fatalf("the response carries the submitted value: %s", rr.Body.String())
			}
			for _, ev := range dh.audit.snapshot() {
				if strings.Contains(stringify(ev), "sk-ant-secret-value") {
					t.Fatalf("audit event carries the submitted value: %+v", ev)
				}
			}
			if len(dh.prober.calls) != 0 {
				t.Fatalf("probe calls = %v, want none for a written Secret", dh.prober.calls)
			}
		})
	}
}

// spec: §4.9 (Credential Storage Topology) — Credential values submitted
// through the admin API rule 3 on credential update: secretData on an entry
// that carries an operator ref switches it to its written Secret with a
// Put; secretData on an entry that already carries its written Secret
// overwrites it in place; an update that moves an entry off its written
// Secret Deletes it; an update without secretData that keeps the written
// ref performs nothing.
func TestSecretDataUpdateOperations_spec_4_9(t *testing.T) {
	data := map[string]string{"apiKey": "v2"}
	cases := []struct {
		name    string
		id      string
		body    admin.CredentialEntryRequest
		as      func(*http.Request) *http.Request
		calls   []writerCall
		wantRef string
	}{
		{"operator ref to secretData", "key-1", dataEntry("", data), withTenantAdminPrincipal, []writerCall{putCall("key-1", data)}, managedRef("p1", "key-1")},
		{"written Secret overwritten", "key-2", dataEntry("", data), withTenantAdminPrincipal, []writerCall{putCall("key-2", data)}, managedRef("p1", "key-2")},
		{"written Secret to operator ref", "key-2", admin.CredentialEntryRequest{CredentialEntryPayload: admin.CredentialEntryPayload{SecretRef: "existing-1"}}, withTenantAdminPrincipal, []writerCall{deleteCall("key-2")}, refTestNS + "/existing-1"},
		{"written Secret to roleArn", "key-2", admin.CredentialEntryRequest{CredentialEntryPayload: admin.CredentialEntryPayload{RoleArn: "arn:aws:iam::1:role/r"}}, withTenantAdminPrincipal, []writerCall{deleteCall("key-2")}, ""},
		{"written ref kept in bare form", "key-2", admin.CredentialEntryRequest{CredentialEntryPayload: admin.CredentialEntryPayload{SecretRef: managedName("p1", "key-2"), Region: "us-east-1"}}, withTenantAdminPrincipal, nil, managedRef("p1", "key-2")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dh := newDataHarness(t, true)
			seedRefPool(t, dh.refHarness)
			rr := sendRef(t, dh.refHarness, http.MethodPut, updatePathFor(tc.id), tc.body, tc.as, "")
			if rr.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body %s", rr.Code, rr.Body.String())
			}
			wantCalls(t, dh, tc.calls...)
			row := storedRow(t, dh.refHarness, "p1")
			for _, c := range row.Credentials {
				if c.ID == tc.id && c.SecretRef != tc.wantRef {
					t.Fatalf("%s secretRef = %q, want %q", tc.id, c.SecretRef, tc.wantRef)
				}
			}
		})
	}
}

// spec: §4.9 (Credential Storage Topology) — Credential values submitted
// through the admin API rule 3: credential removal, pool update, and pool
// deletion Delete each dropped Lenny-written ref and touch no operator
// Secret; revocation, pool revocation, and re-enable perform no Secret
// operation.
func TestSecretDeletesOnDroppedRefs_spec_4_9(t *testing.T) {
	type tc struct {
		name   string
		send   func(t *testing.T, dh *dataHarness, row credentialpoolstore.CredentialPool) *httptest.ResponseRecorder
		status int
		calls  []writerCall
	}
	cases := []tc{
		{"remove written entry", func(t *testing.T, dh *dataHarness, _ credentialpoolstore.CredentialPool) *httptest.ResponseRecorder {
			return sendRef(t, dh.refHarness, http.MethodDelete, updatePathFor("key-2"), nil, withTenantAdminPrincipal, "")
		}, http.StatusOK, []writerCall{deleteCall("key-2")}},
		{"remove operator entry", func(t *testing.T, dh *dataHarness, _ credentialpoolstore.CredentialPool) *httptest.ResponseRecorder {
			return sendRef(t, dh.refHarness, http.MethodDelete, updatePathFor("key-1"), nil, withTenantAdminPrincipal, "")
		}, http.StatusOK, nil},
		{"pool PUT dropping the written entry", func(t *testing.T, dh *dataHarness, row credentialpoolstore.CredentialPool) *httptest.ResponseRecorder {
			return sendRef(t, dh.refHarness, http.MethodPut, "/v1/admin/credential-pools/p1",
				poolBody("p1", []admin.CredentialEntryPayload{{ID: "key-1", SecretRef: "existing-1"}}), withTenantAdminPrincipal, etagOf(row.Version))
		}, http.StatusOK, []writerCall{deleteCall("key-2")}},
		{"pool PUT keeping the written entry", func(t *testing.T, dh *dataHarness, row credentialpoolstore.CredentialPool) *httptest.ResponseRecorder {
			return sendRef(t, dh.refHarness, http.MethodPut, "/v1/admin/credential-pools/p1",
				poolBody("p1", []admin.CredentialEntryPayload{{ID: "key-2", SecretRef: managedName("p1", "key-2")}}), withTenantAdminPrincipal, etagOf(row.Version))
		}, http.StatusOK, nil},
		{"pool DELETE", func(t *testing.T, dh *dataHarness, _ credentialpoolstore.CredentialPool) *httptest.ResponseRecorder {
			return sendRef(t, dh.refHarness, http.MethodDelete, "/v1/admin/credential-pools/p1?tenantId=acme", nil, withTenantAdminPrincipal, "")
		}, http.StatusNoContent, []writerCall{deleteCall("key-2")}},
		{"credential revoke", func(t *testing.T, dh *dataHarness, _ credentialpoolstore.CredentialPool) *httptest.ResponseRecorder {
			return sendRef(t, dh.refHarness, http.MethodPost, "/v1/admin/credential-pools/p1/credentials/key-2/revoke?tenantId=acme", nil, withTenantAdminPrincipal, "")
		}, http.StatusOK, nil},
		{"pool revoke", func(t *testing.T, dh *dataHarness, _ credentialpoolstore.CredentialPool) *httptest.ResponseRecorder {
			return sendRef(t, dh.refHarness, http.MethodPost, "/v1/admin/credential-pools/p1/revoke?tenantId=acme", nil, withTenantAdminPrincipal, "")
		}, http.StatusOK, nil},
		{"re-enable", func(t *testing.T, dh *dataHarness, _ credentialpoolstore.CredentialPool) *httptest.ResponseRecorder {
			return sendRef(t, dh.refHarness, http.MethodPost, "/v1/admin/credential-pools/p1/credentials/key-2/re-enable?tenantId=acme", nil, withTenantAdminPrincipal, "")
		}, http.StatusOK, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dh := newDataHarness(t, true)
			row := seedRefPool(t, dh.refHarness)
			rr := c.send(t, dh, row)
			if rr.Code != c.status {
				t.Fatalf("status = %d, want %d; body %s", rr.Code, c.status, rr.Body.String())
			}
			wantCalls(t, dh, c.calls...)
		})
	}
}

// spec: §4.9 (Credential Storage Topology) — Credential values submitted
// through the admin API rule 5: a failed Put or Delete refuses the write
// with 503 SERVICE_UNAVAILABLE and Retry-After, and the row does not
// change. No compensating operation follows.
func TestSecretOperationFailureIs503_spec_4_9(t *testing.T) {
	fault := errors.New("apiserver unavailable")
	cases := []struct {
		name   string
		fail   func(w *fakeCredentialSecretWriter)
		method string
		path   string
		body   any
		etag   bool
		calls  []writerCall
	}{
		{"add Put", func(w *fakeCredentialSecretWriter) { w.failPut = fault }, http.MethodPost, addPath, dataEntry("key-3", map[string]string{"apiKey": "v"}), false, []writerCall{putCall("key-3", nil)}},
		{"update Put", func(w *fakeCredentialSecretWriter) { w.failPut = fault }, http.MethodPut, updatePathFor("key-1"), dataEntry("", map[string]string{"apiKey": "v"}), false, []writerCall{putCall("key-1", nil)}},
		{"update Delete", func(w *fakeCredentialSecretWriter) { w.failDelete = fault }, http.MethodPut, updatePathFor("key-2"), admin.CredentialEntryPayload{SecretRef: "existing-1"}, false, []writerCall{deleteCall("key-2")}},
		{"remove Delete", func(w *fakeCredentialSecretWriter) { w.failDelete = fault }, http.MethodDelete, updatePathFor("key-2"), nil, false, []writerCall{deleteCall("key-2")}},
		{"pool PUT Delete", func(w *fakeCredentialSecretWriter) { w.failDelete = fault }, http.MethodPut, "/v1/admin/credential-pools/p1", poolBody("p1", []admin.CredentialEntryPayload{{ID: "key-1", SecretRef: "existing-1"}}), true, []writerCall{deleteCall("key-2")}},
		{"pool DELETE Delete", func(w *fakeCredentialSecretWriter) { w.failDelete = fault }, http.MethodDelete, "/v1/admin/credential-pools/p1?tenantId=acme", nil, false, []writerCall{deleteCall("key-2")}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dh := newDataHarness(t, true)
			before := seedRefPool(t, dh.refHarness)
			tc.fail(dh.writer)
			etag := ""
			if tc.etag {
				etag = etagOf(before.Version)
			}
			rr := sendRef(t, dh.refHarness, tc.method, tc.path, tc.body, withAdminPrincipal, etag)
			wantRefusal(t, rr, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "")
			if rr.Header().Get("Retry-After") == "" {
				t.Fatal("503 SERVICE_UNAVAILABLE carries no Retry-After")
			}
			if strings.Contains(rr.Body.String(), fault.Error()) {
				t.Fatalf("the refusal carries the writer error text: %s", rr.Body.String())
			}
			wantRowUnchanged(t, dh, before)
			if after := storedRow(t, dh.refHarness, "p1"); !after.IsActive() {
				t.Fatal("a failed pool DELETE soft-deleted the pool")
			}
			got := dh.calls()
			if len(got) != len(tc.calls) || got[0].op != tc.calls[0].op || got[0].name != tc.calls[0].name {
				t.Fatalf("writer calls = %+v, want %+v and no compensation", got, tc.calls)
			}
		})
	}
}

// spec: §4.9 (Credential Storage Topology) — Credential values submitted
// through the admin API rule 6: with no writer, a write for which rule 3
// gives a Put or Delete is refused with 422 TOKEN_SERVICE_NOT_CONFIGURED
// and changes nothing, under dryRun too; a write for which rule 3 gives
// none succeeds, and revocation, pool revocation, and re-enable refuse
// nothing.
func TestNoWriterRefusesSecretOperations_spec_4_9(t *testing.T) {
	data := map[string]string{"apiKey": "v"}
	cases := []struct {
		name     string
		method   string
		path     string
		body     any
		etag     bool
		refused  bool
		okStatus int
	}{
		{"add secretData", http.MethodPost, addPath, dataEntry("key-3", data), false, true, 0},
		{"add secretData dryRun", http.MethodPost, addPath + "&dryRun=true", dataEntry("key-3", data), false, true, 0},
		{"add secretRef", http.MethodPost, addPath, admin.CredentialEntryPayload{ID: "key-3", SecretRef: "existing-1"}, false, false, http.StatusCreated},
		{"update secretData", http.MethodPut, updatePathFor("key-1"), dataEntry("", data), false, true, 0},
		{"update secretData dryRun", http.MethodPut, updatePathFor("key-1") + "&dryRun=true", dataEntry("", data), false, true, 0},
		{"update dropping the written ref", http.MethodPut, updatePathFor("key-2"), admin.CredentialEntryPayload{SecretRef: "existing-1"}, false, true, 0},
		{"update keeping operator ref", http.MethodPut, updatePathFor("key-1"), admin.CredentialEntryPayload{SecretRef: "existing-1", Region: "eu"}, false, false, http.StatusOK},
		{"remove written entry", http.MethodDelete, updatePathFor("key-2"), nil, false, true, 0},
		{"remove operator entry", http.MethodDelete, updatePathFor("key-1"), nil, false, false, http.StatusOK},
		{"pool PUT dropping the written ref", http.MethodPut, "/v1/admin/credential-pools/p1", poolBody("p1", []admin.CredentialEntryPayload{{ID: "key-1", SecretRef: "existing-1"}}), true, true, 0},
		{"pool PUT dryRun dropping the written ref", http.MethodPut, "/v1/admin/credential-pools/p1?dryRun=true", poolBody("p1", []admin.CredentialEntryPayload{{ID: "key-1", SecretRef: "existing-1"}}), true, true, 0},
		{"pool PUT keeping the written ref", http.MethodPut, "/v1/admin/credential-pools/p1", poolBody("p1", []admin.CredentialEntryPayload{{ID: "key-1", SecretRef: "existing-1"}, {ID: "key-2", SecretRef: managedName("p1", "key-2")}}), true, false, http.StatusOK},
		{"pool DELETE with a written ref", http.MethodDelete, "/v1/admin/credential-pools/p1?tenantId=acme", nil, false, true, 0},
		{"credential revoke", http.MethodPost, "/v1/admin/credential-pools/p1/credentials/key-2/revoke?tenantId=acme", nil, false, false, http.StatusOK},
		{"pool revoke", http.MethodPost, "/v1/admin/credential-pools/p1/revoke?tenantId=acme", nil, false, false, http.StatusOK},
		{"re-enable", http.MethodPost, "/v1/admin/credential-pools/p1/credentials/key-2/re-enable?tenantId=acme", nil, false, false, http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dh := newDataHarness(t, false)
			before := seedRefPool(t, dh.refHarness)
			etag := ""
			if tc.etag {
				etag = etagOf(before.Version)
			}
			rr := sendRef(t, dh.refHarness, tc.method, tc.path, tc.body, withAdminPrincipal, etag)
			if !tc.refused {
				if rr.Code != tc.okStatus {
					t.Fatalf("status = %d, want %d; body %s", rr.Code, tc.okStatus, rr.Body.String())
				}
				return
			}
			wantRefusal(t, rr, http.StatusUnprocessableEntity, "TOKEN_SERVICE_NOT_CONFIGURED", "")
			if rr.Header().Get("Retry-After") != "" {
				t.Fatal("the non-retryable 422 carries Retry-After")
			}
			wantRowUnchanged(t, dh, before)
		})
	}
	t.Run("pool DELETE with no written ref", func(t *testing.T) {
		dh := newDataHarness(t, false)
		if err := dh.store.Create(context.Background(), credentialpoolstore.CredentialPool{
			TenantID: "acme", Name: "p1", Provider: "anthropic_direct",
			Credentials: []credentialpoolstore.Credential{{ID: "key-1", SecretRef: refTestNS + "/existing-1"}},
		}); err != nil {
			t.Fatalf("seed: %v", err)
		}
		rr := sendRef(t, dh.refHarness, http.MethodDelete, "/v1/admin/credential-pools/p1?tenantId=acme", nil, withAdminPrincipal, "")
		if rr.Code != http.StatusNoContent || storedRow(t, dh.refHarness, "p1").IsActive() {
			t.Fatalf("status = %d, want 204 and a soft-deleted pool", rr.Code)
		}
	})
}

// spec: §4.9 (Credential Storage Topology) — Credential values submitted
// through the admin API rule 4: credential addition and update against a
// soft-deleted pool return 404 RESOURCE_NOT_FOUND with no Secret operation,
// with and without dryRun; a pool DELETE of a soft-deleted pool is 204
// with the row unchanged and no Secret operation; and a stale If-Match on
// a soft-deleted pool is 412, because the precondition runs first.
func TestSoftDeletedPoolGuards_spec_4_9(t *testing.T) {
	data := map[string]string{"apiKey": "v"}
	for _, tc := range []struct {
		name, method, path string
		body               any
	}{
		{"add", http.MethodPost, addPath, dataEntry("key-3", data)},
		{"add dryRun", http.MethodPost, addPath + "&dryRun=true", dataEntry("key-3", data)},
		{"update", http.MethodPut, updatePathFor("key-1"), dataEntry("", data)},
		{"update dryRun", http.MethodPut, updatePathFor("key-1") + "&dryRun=true", dataEntry("", data)},
		// The registration rule would refuse this tenant-admin
		// introduction with 403; the row guard runs first.
		{"add introducing a ref", http.MethodPost, addPath, admin.CredentialEntryPayload{ID: "key-3", SecretRef: "introduced"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dh := newDataHarness(t, true)
			seedRefPool(t, dh.refHarness)
			softDelete(t, dh)
			before := storedRow(t, dh.refHarness, "p1")
			rr := sendRef(t, dh.refHarness, tc.method, tc.path, tc.body, withTenantAdminPrincipal, "")
			wantRefusal(t, rr, http.StatusNotFound, "RESOURCE_NOT_FOUND", "")
			wantRowUnchanged(t, dh, before)
			wantNoWriterCalls(t, dh)
		})
	}
	t.Run("pool DELETE repeat", func(t *testing.T) {
		dh := newDataHarness(t, true)
		seedRefPool(t, dh.refHarness)
		softDelete(t, dh)
		before := storedRow(t, dh.refHarness, "p1")
		rr := sendRef(t, dh.refHarness, http.MethodDelete, "/v1/admin/credential-pools/p1?tenantId=acme", nil, withAdminPrincipal, "")
		if rr.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want 204", rr.Code)
		}
		after := storedRow(t, dh.refHarness, "p1")
		if after.Version != before.Version || !after.DeletedAt.Equal(before.DeletedAt) {
			t.Fatalf("a repeat DELETE changed the row: %+v -> %+v", before, after)
		}
		wantNoWriterCalls(t, dh)
	})
	t.Run("pool DELETE repeat with no writer", func(t *testing.T) {
		dh := newDataHarness(t, false)
		seedRefPool(t, dh.refHarness)
		softDelete(t, dh)
		rr := sendRef(t, dh.refHarness, http.MethodDelete, "/v1/admin/credential-pools/p1?tenantId=acme", nil, withAdminPrincipal, "")
		if rr.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want 204", rr.Code)
		}
	})
	t.Run("pool DELETE stale If-Match on a soft-deleted pool", func(t *testing.T) {
		dh := newDataHarness(t, true)
		seedRefPool(t, dh.refHarness)
		softDelete(t, dh)
		rr := sendRef(t, dh.refHarness, http.MethodDelete, "/v1/admin/credential-pools/p1?tenantId=acme", nil, withAdminPrincipal, `"1"`)
		wantRefusal(t, rr, http.StatusPreconditionFailed, "ETAG_MISMATCH", "")
		wantNoWriterCalls(t, dh)
	})
	t.Run("pool DELETE malformed If-Match", func(t *testing.T) {
		dh := newDataHarness(t, true)
		seedRefPool(t, dh.refHarness)
		rr := sendRef(t, dh.refHarness, http.MethodDelete, "/v1/admin/credential-pools/p1?tenantId=acme", nil, withAdminPrincipal, "not-an-etag")
		wantRefusal(t, rr, http.StatusBadRequest, "VALIDATION_ERROR", "")
		wantNoWriterCalls(t, dh)
	})
	t.Run("pool DELETE missing pool", func(t *testing.T) {
		dh := newDataHarness(t, true)
		rr := sendRef(t, dh.refHarness, http.MethodDelete, "/v1/admin/credential-pools/ghost?tenantId=acme", nil, withAdminPrincipal, "")
		wantRefusal(t, rr, http.StatusNotFound, "RESOURCE_NOT_FOUND", "")
	})
}

// spec: §4.9 (Credential Storage Topology) — Credential values submitted
// through the admin API rule 4: the pool row is validated before any
// Secret operation, so a pool PUT that drops a Lenny-written ref and sets
// a field the store refuses is 400 with no Delete, and so is the same
// request with no writer, because the validation precedes the writer
// check.
func TestValidateRunsBeforeSecretOperation_spec_4_9(t *testing.T) {
	for _, withWriter := range []bool{true, false} {
		dh := newDataHarness(t, withWriter)
		before := seedRefPool(t, dh.refHarness)
		body := poolBody("p1", []admin.CredentialEntryPayload{{ID: "key-1", SecretRef: "existing-1"}})
		body.DeliveryMode = "carrier-pigeon"
		rr := sendRef(t, dh.refHarness, http.MethodPut, "/v1/admin/credential-pools/p1", body, withAdminPrincipal, etagOf(before.Version))
		wantRefusal(t, rr, http.StatusBadRequest, "VALIDATION_ERROR", "")
		wantRowUnchanged(t, dh, before)
		wantNoWriterCalls(t, dh)
	}
}

// spec: §4.9 (Credential Storage Topology), §15.1 (dryRun) — a dryRun
// credential addition or update runs the registration rule and the
// reserved prefix against an unlocked read, makes no probe call, performs
// no Secret operation, and commits nothing.
func TestCredentialDryRunRunsChecksWithoutEffects_spec_4_9(t *testing.T) {
	cases := []struct {
		name   string
		method string
		path   string
		body   any
		as     func(*http.Request) *http.Request
		status int
		code   string
	}{
		{"add introduction by tenant-admin", http.MethodPost, addPath + "&dryRun=true", admin.CredentialEntryPayload{ID: "key-3", SecretRef: "introduced"}, withTenantAdminPrincipal, http.StatusForbidden, "FORBIDDEN"},
		{"update introduction by custom role", http.MethodPut, updatePathFor("key-1") + "&dryRun=true", admin.CredentialEntryPayload{SecretRef: "introduced"}, asCredAdmin, http.StatusForbidden, "FORBIDDEN"},
		{"add reserved prefix", http.MethodPost, addPath + "&dryRun=true", admin.CredentialEntryPayload{ID: "key-3", SecretRef: managedName("p1", "key-2")}, withAdminPrincipal, http.StatusBadRequest, "VALIDATION_ERROR"},
		{"update reserved prefix", http.MethodPut, updatePathFor("key-1") + "&dryRun=true", admin.CredentialEntryPayload{SecretRef: managedName("p1", "key-2")}, withAdminPrincipal, http.StatusBadRequest, "VALIDATION_ERROR"},
		{"add duplicate id", http.MethodPost, addPath + "&dryRun=true", admin.CredentialEntryPayload{ID: "key-1", SecretRef: "existing-1"}, withAdminPrincipal, http.StatusConflict, "RESOURCE_ALREADY_EXISTS"},
		{"update missing id", http.MethodPut, updatePathFor("key-9") + "&dryRun=true", admin.CredentialEntryPayload{SecretRef: "existing-1"}, withAdminPrincipal, http.StatusNotFound, "RESOURCE_NOT_FOUND"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dh := newDataHarness(t, true)
			before := seedRefPool(t, dh.refHarness)
			rr := sendRef(t, dh.refHarness, tc.method, tc.path, tc.body, tc.as, "")
			wantRefusal(t, rr, tc.status, tc.code, "")
			wantRowUnchanged(t, dh, before)
			wantNoWriterCalls(t, dh)
			if len(dh.prober.calls) != 0 {
				t.Fatalf("probe calls = %v, want none", dh.prober.calls)
			}
		})
	}
	for _, tc := range []struct {
		name, method, path string
		status             int
		body               any
	}{
		{"add platform-admin introduction", http.MethodPost, addPath + "&dryRun=true", http.StatusCreated, admin.CredentialEntryPayload{ID: "key-3", SecretRef: "introduced"}},
		{"add secretData", http.MethodPost, addPath + "&dryRun=true", http.StatusCreated, dataEntry("key-3", map[string]string{"apiKey": "v"})},
		{"update secretData", http.MethodPut, updatePathFor("key-1") + "&dryRun=true", http.StatusOK, dataEntry("", map[string]string{"apiKey": "v"})},
		{"pool PUT dropping the written ref", http.MethodPut, "/v1/admin/credential-pools/p1?dryRun=true", http.StatusOK, poolBody("p1", []admin.CredentialEntryPayload{{ID: "key-1", SecretRef: "existing-1"}})},
		{"pool create", http.MethodPost, "/v1/admin/credential-pools?dryRun=true", http.StatusCreated, poolBody("new", []admin.CredentialEntryPayload{{ID: "key-1", SecretRef: "introduced"}})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dh := newDataHarness(t, true)
			before := seedRefPool(t, dh.refHarness)
			rr := sendRef(t, dh.refHarness, tc.method, tc.path, tc.body, withAdminPrincipal, etagOf(before.Version))
			if rr.Code != tc.status || rr.Header().Get("X-Dry-Run") != "true" {
				t.Fatalf("status = %d dry-run header %q, want %d true; body %s", rr.Code, rr.Header().Get("X-Dry-Run"), tc.status, rr.Body.String())
			}
			wantRowUnchanged(t, dh, before)
			wantNoWriterCalls(t, dh)
			if len(dh.prober.calls) != 0 {
				t.Fatalf("probe calls = %v, want none under dryRun", dh.prober.calls)
			}
		})
	}
}

// spec: §4.9 (Credential Storage Topology) — the request checks run in
// order and a request failing two of them gets the earlier refusal with no
// later probe call, Secret operation, or commit: the secretData check runs
// before the ref parse, the row guard before the registration rule, and
// the validation before the writer check.
func TestSecretDataCheckOrderPairs_spec_4_9(t *testing.T) {
	t.Run("secretData check before ref parse", func(t *testing.T) {
		dh := newDataHarness(t, true)
		before := seedRefPool(t, dh.refHarness)
		body := admin.CredentialEntryRequest{
			CredentialEntryPayload: admin.CredentialEntryPayload{ID: "key-3", SecretRef: "lenny-system/x"},
			SecretData:             map[string]string{"apiKey": "v"},
		}
		got := wantRefusal(t, sendRef(t, dh.refHarness, http.MethodPost, addPath, body, withAdminPrincipal, ""), http.StatusBadRequest, "VALIDATION_ERROR", "")
		if got.Details["field"] == "secretRef" {
			t.Fatal("the ref parse ran before the secretData check")
		}
		wantRowUnchanged(t, dh, before)
		wantNoWriterCalls(t, dh)
	})
	t.Run("pool secretData before ref parse", func(t *testing.T) {
		dh := newDataHarness(t, true)
		body := asPoolRequest(poolBody("new", nil))
		body.Credentials = []admin.CredentialEntryRequest{
			{CredentialEntryPayload: admin.CredentialEntryPayload{ID: "a", SecretRef: "lenny-system/x"}},
			dataEntry("b", map[string]string{"apiKey": "v"}),
		}
		wantRefusal(t, sendRef(t, dh.refHarness, http.MethodPost, "/v1/admin/credential-pools", body, withAdminPrincipal, ""), http.StatusBadRequest, "VALIDATION_ERROR", "secretData")
	})
	t.Run("validation before writer check", func(t *testing.T) {
		dh := newDataHarness(t, false)
		before := seedRefPool(t, dh.refHarness)
		body := poolBody("p1", nil)
		body.MaxConcurrentSessions = -1
		wantRefusal(t, sendRef(t, dh.refHarness, http.MethodPut, "/v1/admin/credential-pools/p1", body, withAdminPrincipal, etagOf(before.Version)), http.StatusBadRequest, "VALIDATION_ERROR", "")
		wantRowUnchanged(t, dh, before)
	})
}

// spec: §4.9 (Credential Storage Topology) — no compensation: a commit
// that fails after a credential addition's Put returns 503 and the Put
// stands with no Delete issued.
func TestCommitFailureAfterPutIsNotCompensated_spec_4_9(t *testing.T) {
	dh := newDataHarness(t, true)
	before := seedRefPool(t, dh.refHarness)
	dh.store.failCommit = errors.New("commit failed")
	data := map[string]string{"apiKey": "v"}
	rr := sendRef(t, dh.refHarness, http.MethodPost, addPath, dataEntry("key-3", data), withAdminPrincipal, "")
	wantRefusal(t, rr, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "")
	wantCalls(t, dh, putCall("key-3", data))
	if _, ok := dh.writer.secrets[managedName("p1", "key-3")]; !ok {
		t.Fatal("the Put did not stand")
	}
	dh.store.failCommit = nil
	wantRowUnchanged(t, dh, before)
}

// spec: §15.1, §4.9 (Credential Storage Topology) — the pool DELETE
// compares If-Match against the row it holds locked. A concurrent write
// bumps the version after the client's read, and the DELETE carrying the
// earlier ETag gets 412 with no Secret operation and the pool active.
func TestPoolDeleteLockedIfMatch_spec_15_1(t *testing.T) {
	dh := newDataHarness(t, true)
	row := seedRefPool(t, dh.refHarness)
	dh.store.beforeUpdate = func(ctx context.Context, m *credentialpoolstore.Memory) {
		if _, err := m.Update(ctx, "acme", "p1", func(p *credentialpoolstore.CredentialPool) error {
			p.MaxConcurrentSessions = 12
			return nil
		}); err != nil {
			t.Errorf("concurrent write: %v", err)
		}
	}
	rr := sendRef(t, dh.refHarness, http.MethodDelete, "/v1/admin/credential-pools/p1?tenantId=acme", nil, withAdminPrincipal, etagOf(row.Version))
	wantRefusal(t, rr, http.StatusPreconditionFailed, "ETAG_MISMATCH", "")
	wantNoWriterCalls(t, dh)
	if !storedRow(t, dh.refHarness, "p1").IsActive() {
		t.Fatal("the stale DELETE soft-deleted the pool")
	}
}

// spec: §4.9 (Credential Storage Topology) — Credential values submitted
// through the admin API rule 4: the pool DELETE decides its Deletes from
// the row it holds locked. A concurrent write adds an entry on a
// Lenny-written Secret after the client's read, and the DELETE with no
// If-Match deletes that Secret as well as the pool's earlier one.
func TestPoolDeleteLockedSnapshot_spec_4_9(t *testing.T) {
	dh := newDataHarness(t, true)
	seedRefPool(t, dh.refHarness)
	dh.store.beforeUpdate = func(ctx context.Context, m *credentialpoolstore.Memory) {
		if _, err := m.Update(ctx, "acme", "p1", func(p *credentialpoolstore.CredentialPool) error {
			p.Credentials = append(p.Credentials, credentialpoolstore.Credential{ID: "key-9", SecretRef: managedRef("p1", "key-9")})
			return nil
		}); err != nil {
			t.Errorf("concurrent write: %v", err)
		}
	}
	rr := sendRef(t, dh.refHarness, http.MethodDelete, "/v1/admin/credential-pools/p1?tenantId=acme", nil, withAdminPrincipal, "")
	if rr.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body %s", rr.Code, rr.Body.String())
	}
	wantCalls(t, dh, deleteCall("key-2"), deleteCall("key-9"))
	if storedRow(t, dh.refHarness, "p1").IsActive() {
		t.Fatal("the pool is still active")
	}
}

// spec: §4.9 (Credential Storage Topology) — Credential values submitted
// through the admin API rules 1, 3, 5, and 6 on the bootstrap seed:
// secretData on an entry is an error on that entry with the other entries
// applied; a forced update that drops a Lenny-written ref Deletes it, and
// is an entry error when the writer fails or is absent, under dryRun too;
// a forced update with an invalid field Deletes nothing; a forced update
// of a soft-deleted pool revives it.
func TestBootstrapSeedSecretOperations_spec_4_9(t *testing.T) {
	dropBody := func(maxSessions int) admin.BootstrapRequest {
		return seedBody([]admin.CredentialEntryPayload{{ID: "key-1", SecretRef: "existing-1"}}, maxSessions)
	}
	t.Run("secretData on an entry", func(t *testing.T) {
		dh := newDataHarness(t, true)
		body := seedBody(nil, 10)
		body.CredentialPools[0].Credentials = []admin.CredentialEntryRequest{dataEntry("key-1", map[string]string{"apiKey": "v"})}
		section := runSeed(t, dh.refHarness, "", body)
		if code := seedEntryCode(section, "p1"); code != "SEED_VALIDATION" {
			t.Fatalf("p1 code = %q, want SEED_VALIDATION; %+v", code, section)
		}
		if _, err := dh.store.Get(context.Background(), "acme", "other"); err != nil {
			t.Fatalf("the other entry was not applied: %v", err)
		}
		wantNoWriterCalls(t, dh)
	})
	t.Run("forced update dropping a written ref", func(t *testing.T) {
		dh := newDataHarness(t, true)
		seedRefPool(t, dh.refHarness)
		section := runSeed(t, dh.refHarness, "?forceUpdate=true", dropBody(10))
		if code := seedEntryCode(section, "p1"); code != "" {
			t.Fatalf("p1 code = %q, want none; %+v", code, section)
		}
		wantCalls(t, dh, deleteCall("key-2"))
	})
	t.Run("forced update writer failure", func(t *testing.T) {
		dh := newDataHarness(t, true)
		before := seedRefPool(t, dh.refHarness)
		dh.writer.failDelete = errors.New("apiserver unavailable")
		section := runSeed(t, dh.refHarness, "?forceUpdate=true", dropBody(10))
		if code := seedEntryCode(section, "p1"); code != "SEED_STORE_ERROR" {
			t.Fatalf("p1 code = %q, want SEED_STORE_ERROR", code)
		}
		wantRowUnchanged(t, dh, before)
	})
	for _, query := range []string{"?forceUpdate=true", "?forceUpdate=true&dryRun=true"} {
		t.Run("no writer "+query, func(t *testing.T) {
			dh := newDataHarness(t, false)
			before := seedRefPool(t, dh.refHarness)
			section := runSeed(t, dh.refHarness, query, dropBody(10))
			if code := seedEntryCode(section, "p1"); code != "SEED_STORE_ERROR" {
				t.Fatalf("p1 code = %q, want SEED_STORE_ERROR; %+v", code, section)
			}
			wantRowUnchanged(t, dh, before)
		})
	}
	t.Run("no writer and no drop", func(t *testing.T) {
		dh := newDataHarness(t, false)
		seedRefPool(t, dh.refHarness)
		section := runSeed(t, dh.refHarness, "?forceUpdate=true", seedBody([]admin.CredentialEntryPayload{
			{ID: "key-1", SecretRef: "existing-1"}, {ID: "key-2", SecretRef: managedName("p1", "key-2")},
		}, 20))
		if code := seedEntryCode(section, "p1"); code != "" {
			t.Fatalf("p1 code = %q, want none; %+v", code, section)
		}
		if got := storedRow(t, dh.refHarness, "p1").MaxConcurrentSessions; got != 20 {
			t.Fatalf("maxConcurrentSessions = %d, want 20", got)
		}
	})
	t.Run("dryRun forced update performs no Delete", func(t *testing.T) {
		dh := newDataHarness(t, true)
		before := seedRefPool(t, dh.refHarness)
		section := runSeed(t, dh.refHarness, "?forceUpdate=true&dryRun=true", dropBody(10))
		if code := seedEntryCode(section, "p1"); code != "" {
			t.Fatalf("p1 code = %q, want none; %+v", code, section)
		}
		wantRowUnchanged(t, dh, before)
		wantNoWriterCalls(t, dh)
	})
	t.Run("forced update with an invalid field", func(t *testing.T) {
		dh := newDataHarness(t, true)
		before := seedRefPool(t, dh.refHarness)
		body := dropBody(10)
		body.CredentialPools[0].DeliveryMode = "carrier-pigeon"
		section := runSeed(t, dh.refHarness, "?forceUpdate=true", body)
		if code := seedEntryCode(section, "p1"); code != "SEED_STORE_ERROR" {
			t.Fatalf("p1 code = %q, want SEED_STORE_ERROR; %+v", code, section)
		}
		wantRowUnchanged(t, dh, before)
		wantNoWriterCalls(t, dh)
	})
	t.Run("forced update revives a soft-deleted pool", func(t *testing.T) {
		dh := newDataHarness(t, true)
		seedRefPool(t, dh.refHarness)
		softDelete(t, dh)
		runSeed(t, dh.refHarness, "?forceUpdate=true", seedBody([]admin.CredentialEntryPayload{
			{ID: "key-1", SecretRef: "existing-1"}, {ID: "key-2", SecretRef: managedName("p1", "key-2")},
		}, 30))
		if !storedRow(t, dh.refHarness, "p1").IsActive() {
			t.Fatal("the forced seed did not revive the pool")
		}
		wantNoWriterCalls(t, dh)
	})
}

// stringify renders an audit event, its detail values included, for a
// substring search.
func stringify(ev admin.AuditEvent) string { return fmt.Sprintf("%+v", ev) }

// diagnosis: a failure means a Token Service gateway without a cluster client
// answers a Secret write with something other than the rule 5 503, or the
// writer's cause reached the response.
// spec: §4.9 (Credential Storage Topology) — Credential values submitted
// through the admin API rules 5 and 6
func TestUnavailableCredentialSecretWriterFailsEveryOperation_spec_4_9(t *testing.T) {
	cause := errors.New("no kubeconfig")
	w := admin.NewUnavailableCredentialSecretWriter(cause)
	ctx := context.Background()
	for op, err := range map[string]error{
		"Put":          w.Put(ctx, "s", admin.SecretOwner{}, nil),
		"Delete":       w.Delete(ctx, "s"),
		"DeleteTenant": w.DeleteTenant(ctx, "acme"),
	} {
		if !errors.Is(err, cause) {
			t.Errorf("%s err = %v, want one wrapping the cause", op, err)
		}
	}
}
