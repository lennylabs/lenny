// SPDX-License-Identifier: MIT

package admin_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/credentials/credentialpoolstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
)

// fakeSecretProber maps a canonical secretRef to a verdict. An unmapped ref
// defaults to SecretProbeAllowed (the zero value). A non-nil err makes
// every probe indeterminate.
type fakeSecretProber struct {
	verdicts map[string]admin.SecretProbeVerdict
	err      error
	calls    []string
}

func (f *fakeSecretProber) ProbeSecretAccess(_ context.Context, ref string) (admin.SecretProbeVerdict, error) {
	f.calls = append(f.calls, ref)
	if f.err != nil {
		return 0, f.err
	}
	return f.verdicts[ref], nil
}

func probeRouter(prober admin.SecretAccessProber) (*admin.Router, *credentialpoolstore.Memory) {
	store := credentialpoolstore.NewMemory()
	r := admin.NewRouter(tenantstore.NewMemory(), admin.Options{
		Clock: func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
	}).WithCredentialPools(store).WithSecretAccessProber(prober)
	return r, store
}

func poolWithRefs(tenant, name string, refs ...string) admin.CredentialPoolPayload {
	p := admin.CredentialPoolPayload{
		TenantID: tenant, Name: name, Provider: "anthropic_direct",
		AssignmentStrategy: "least-loaded", MaxConcurrentSessions: 10,
	}
	for i, ref := range refs {
		p.Credentials = append(p.Credentials, admin.CredentialEntryPayload{
			ID:        "key-" + string(rune('1'+i)),
			SecretRef: ref,
		})
	}
	return p
}

// spec: §4.9 — pool creation probes every secretRef; an
// allowed probe persists the pool.
func TestCreatePool_ProbeAllowedPersists(t *testing.T) {
	prober := &fakeSecretProber{}
	router, store := probeRouter(prober)
	rr := doAdminReq(t, router.Handler(), http.MethodPost, "/v1/admin/credential-pools",
		poolWithRefs("acme", "p1", "anthropic-key-1"), withAdminPrincipal)
	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rr.Code, rr.Body.String())
	}
	if len(prober.calls) != 1 || prober.calls[0] != "lenny-credentials/anthropic-key-1" {
		t.Fatalf("probe calls = %v, want [lenny-credentials/anthropic-key-1]", prober.calls)
	}
	if _, err := store.Get(context.Background(), "acme", "p1"); err != nil {
		t.Fatalf("pool not persisted: %v", err)
	}
}

// spec: §4.9 (Credential Storage Topology), §15.1 — a DENIED verdict
// rejects creation with 422 CREDENTIAL_SECRET_RBAC_MISSING whose
// details.missingSecrets names every failing Secret by its canonical ref
// and carries no remediation key; the pool is not persisted.
func TestCreatePool_ProbeDeniedRejects(t *testing.T) {
	prober := &fakeSecretProber{verdicts: map[string]admin.SecretProbeVerdict{
		"lenny-credentials/a": admin.SecretProbeDenied,
		"lenny-credentials/b": admin.SecretProbeDenied,
	}}
	router, store := probeRouter(prober)
	rr := doAdminReq(t, router.Handler(), http.MethodPost, "/v1/admin/credential-pools",
		poolWithRefs("acme", "p1", "a", "b"), withAdminPrincipal)
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body=%s", rr.Code, rr.Body.String())
	}
	var body struct {
		Error struct {
			Code    string         `json:"code"`
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Error.Code != "CREDENTIAL_SECRET_RBAC_MISSING" {
		t.Fatalf("code = %q, want CREDENTIAL_SECRET_RBAC_MISSING", body.Error.Code)
	}
	missing, _ := body.Error.Details["missingSecrets"].([]any)
	if len(missing) != 2 || missing[0] != "lenny-credentials/a" || missing[1] != "lenny-credentials/b" {
		t.Fatalf("missingSecrets = %v, want both failing secrets in canonical form", body.Error.Details["missingSecrets"])
	}
	if _, ok := body.Error.Details["remediation"]; ok {
		t.Fatalf("details carry a remediation key: %v", body.Error.Details)
	}
	if len(body.Error.Details) != 1 {
		t.Fatalf("details = %v, want only missingSecrets", body.Error.Details)
	}
	if _, err := store.Get(context.Background(), "acme", "p1"); err == nil {
		t.Fatal("pool persisted despite denied probe")
	}
}

// spec: §4.9 — a NOT_FOUND verdict also rejects with 422.
func TestCreatePool_ProbeNotFoundRejects(t *testing.T) {
	prober := &fakeSecretProber{verdicts: map[string]admin.SecretProbeVerdict{
		"lenny-credentials/absent": admin.SecretProbeNotFound,
	}}
	router, _ := probeRouter(prober)
	rr := doAdminReq(t, router.Handler(), http.MethodPost, "/v1/admin/credential-pools",
		poolWithRefs("acme", "p1", "absent"), withAdminPrincipal)
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body=%s", rr.Code, rr.Body.String())
	}
}

// spec: §4.9 — an indeterminate probe (transport failure)
// rejects with 503 CREDENTIAL_PROBE_UNAVAILABLE; the write does not fail
// open.
func TestCreatePool_ProbeUnavailableRejects(t *testing.T) {
	prober := &fakeSecretProber{err: errors.New("token service unreachable")}
	router, store := probeRouter(prober)
	rr := doAdminReq(t, router.Handler(), http.MethodPost, "/v1/admin/credential-pools",
		poolWithRefs("acme", "p1", "anthropic-key-1"), withAdminPrincipal)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body=%s", rr.Code, rr.Body.String())
	}
	var body struct {
		Error struct {
			Code    string         `json:"code"`
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &body)
	if body.Error.Code != "CREDENTIAL_PROBE_UNAVAILABLE" {
		t.Fatalf("code = %q, want CREDENTIAL_PROBE_UNAVAILABLE", body.Error.Code)
	}
	if got := body.Error.Details["secretRef"]; got != "lenny-credentials/anthropic-key-1" {
		t.Fatalf("details.secretRef = %v, want the canonical ref", got)
	}
	if _, err := store.Get(context.Background(), "acme", "p1"); err == nil {
		t.Fatal("pool persisted despite unevaluated probe")
	}
}

// Without a prober wired the probe is skipped (dev-mode posture); the
// pool is created.
func TestCreatePool_NoProberSkips(t *testing.T) {
	store := credentialpoolstore.NewMemory()
	router := admin.NewRouter(tenantstore.NewMemory(), admin.Options{
		Clock: func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
	}).WithCredentialPools(store)
	rr := doAdminReq(t, router.Handler(), http.MethodPost, "/v1/admin/credential-pools",
		poolWithRefs("acme", "p1", "anthropic-key-1"), withAdminPrincipal)
	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rr.Code, rr.Body.String())
	}
}

// spec: §4.9 — a PUT that introduces a new secretRef probes
// only the new ref; a DENIED new ref rejects the update.
func TestUpdatePool_ProbesNewSecretRefOnly(t *testing.T) {
	prober := &fakeSecretProber{verdicts: map[string]admin.SecretProbeVerdict{
		"lenny-credentials/old": admin.SecretProbeDenied, // would fail if re-probed
		"lenny-credentials/new": admin.SecretProbeDenied,
	}}
	router, store := probeRouter(prober)
	if err := store.Create(context.Background(), credentialpoolstore.CredentialPool{
		TenantID: "acme", Name: "p1", Provider: "anthropic_direct",
		AssignmentStrategy: "least-loaded", MaxConcurrentSessions: 10,
		Credentials: []credentialpoolstore.Credential{{ID: "key-1", SecretRef: "lenny-credentials/old"}},
		CreatedAt:   time.Now(),
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	body := poolWithRefs("acme", "p1", "old", "new")
	rr := doAdminReq(t, router.Handler(), http.MethodPut, "/v1/admin/credential-pools/p1", body, withAdminPrincipal)
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body=%s", rr.Code, rr.Body.String())
	}
	// Only the new ref is probed; the unchanged "old" ref is not.
	if len(prober.calls) != 1 || prober.calls[0] != "lenny-credentials/new" {
		t.Fatalf("probe calls = %v, want [lenny-credentials/new]", prober.calls)
	}
}

// spec: §4.9 — a PUT that changes nothing about the secretRefs
// re-probes none of them and succeeds even when the existing refs would
// be denied.
func TestUpdatePool_UnchangedSecretRefNotReprobed(t *testing.T) {
	prober := &fakeSecretProber{verdicts: map[string]admin.SecretProbeVerdict{
		"lenny-credentials/old": admin.SecretProbeDenied,
	}}
	router, store := probeRouter(prober)
	if err := store.Create(context.Background(), credentialpoolstore.CredentialPool{
		TenantID: "acme", Name: "p1", Provider: "anthropic_direct",
		AssignmentStrategy: "least-loaded", MaxConcurrentSessions: 10,
		Credentials: []credentialpoolstore.Credential{{ID: "key-1", SecretRef: "lenny-credentials/old"}},
		CreatedAt:   time.Now(),
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	body := poolWithRefs("acme", "p1", "old")
	body.MaxConcurrentSessions = 25 // a non-secret change
	rr := doAdminReq(t, router.Handler(), http.MethodPut, "/v1/admin/credential-pools/p1", body, withAdminPrincipal)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if len(prober.calls) != 0 {
		t.Fatalf("probe calls = %v, want none (no new secretRef)", prober.calls)
	}
}
