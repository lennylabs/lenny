// SPDX-License-Identifier: MIT

//go:build load_local

// Tier-7a load_local coverage for the Lenny-written credential Secret path
// of the admin credential-pool writes. Randomized interleavings of
// concurrent credential additions of one id, credential updates carrying
// secretData, credential removals and re-additions, and pool deletions run
// against the production admin Router with the race detector, and an
// invariant checker reads the committed rows and the Secrets the writer
// holds after every round. With no injected fault, no committed entry of an
// active pool references a missing Secret, no two committed entries share a
// Lenny-written reference, and a deleted pool leaves no Lenny-written
// Secret behind.
//
// spec: §4.9 (Credential Storage Topology: Credential values submitted
// through the admin API rules 2, 3, and 4)
package tier7a_load_local_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	pkgauth "github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credentialpoolstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
)

const interleaveNS = "lenny-credentials"

// interleaveWriter holds the Secrets the admin writes put, by name.
type interleaveWriter struct {
	mu      sync.Mutex
	secrets map[string]admin.SecretOwner
}

func (w *interleaveWriter) Put(_ context.Context, name string, meta admin.SecretOwner, _ map[string]string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.secrets[name] = meta
	return nil
}

func (w *interleaveWriter) Delete(_ context.Context, name string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	delete(w.secrets, name)
	return nil
}

func (w *interleaveWriter) DeleteTenant(context.Context, string) error { return nil }

func (w *interleaveWriter) snapshot() map[string]admin.SecretOwner {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make(map[string]admin.SecretOwner, len(w.secrets))
	for k, v := range w.secrets {
		out[k] = v
	}
	return out
}

func interleaveSend(h http.Handler, method, path string, body any) int {
	var buf []byte
	if body != nil {
		buf, _ = json.Marshal(body)
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(buf))
	req = req.WithContext(authmw.WithPrincipal(req.Context(), authmw.Principal{
		TenantID: "acme", Subject: "alice@acme.com", Roles: []pkgauth.Role{pkgauth.RoleTenantAdmin},
	}))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr.Code
}

// interleaveOp issues one random write against pool for credential id.
func interleaveOp(h http.Handler, rng *rand.Rand, pool string) int {
	id := fmt.Sprintf("key-%d", rng.Intn(3))
	base := "/v1/admin/credential-pools/" + pool
	value := map[string]string{"apiKey": fmt.Sprintf("v%d", rng.Int())}
	switch n := rng.Intn(20); {
	case n < 8:
		return interleaveSend(h, http.MethodPost, base+"/credentials?tenantId=acme", map[string]any{"id": id, "secretData": value})
	case n < 12:
		return interleaveSend(h, http.MethodPut, base+"/credentials/"+id+"?tenantId=acme", map[string]any{"secretData": value})
	case n < 15:
		return interleaveSend(h, http.MethodDelete, base+"/credentials/"+id+"?tenantId=acme", nil)
	case n < 17:
		return interleaveSend(h, http.MethodPut, base+"/credentials/"+id+"?tenantId=acme", map[string]any{"secretRef": "operator-made"})
	case n < 19:
		return interleaveSend(h, http.MethodPost, base+"/credentials/"+id+"/revoke?tenantId=acme", nil)
	default:
		return interleaveSend(h, http.MethodDelete, base+"?tenantId=acme", nil)
	}
}

// checkSecretInvariants reads every pool of acme and the writer's Secrets
// and reports each violated invariant.
func checkSecretInvariants(t *testing.T, store credentialpoolstore.Store, w *interleaveWriter) {
	t.Helper()
	pools, err := store.List(context.Background(), "acme", credentialpoolstore.ListFilter{IncludeDeleted: true})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	secrets := w.snapshot()
	owners := map[string]string{}
	for _, p := range pools {
		for _, c := range p.Credentials {
			name := c.SecretRef[strings.LastIndexByte(c.SecretRef, '/')+1:]
			if !credential.IsManaged(name) {
				continue
			}
			if prev, dup := owners[c.SecretRef]; dup {
				t.Errorf("entries %s and %s/%s share the Lenny-written ref %s", prev, p.Name, c.ID, c.SecretRef)
			}
			owners[c.SecretRef] = p.Name + "/" + c.ID
			if want := credential.ManagedSecretName("acme", p.Name, c.ID); name != want {
				t.Errorf("%s/%s references %s, want its own Secret %s", p.Name, c.ID, name, want)
			}
			if _, ok := secrets[name]; p.IsActive() && !ok {
				t.Errorf("active pool %s entry %s references the missing Secret %s", p.Name, c.ID, name)
			}
		}
		if p.IsActive() {
			continue
		}
		for name, owner := range secrets {
			if owner.Pool == p.Name {
				t.Errorf("deleted pool %s left the Lenny-written Secret %s", p.Name, name)
			}
		}
	}
}

// TestCredentialPoolSecretWriteInterleavings_spec_4_9 runs randomized
// concurrent rounds and checks the invariants after each.
//
// diagnosis: a failure means concurrent admin credential-pool writes left a
// committed entry on a missing Lenny-written Secret, two entries on one
// Secret, an entry on another entry's Secret, or a deleted pool's Secret in
// place, so a Secret operation ran outside the pool row lock or against a
// row other than the one the write committed.
// spec: §4.9 (Credential Storage Topology: Credential values submitted
// through the admin API rules 2, 3, and 4)
func TestCredentialPoolSecretWriteInterleavings_spec_4_9(t *testing.T) {
	const (
		rounds  = 40
		workers = 8
		opsEach = 25
	)
	for round := 0; round < rounds; round++ {
		seed := int64(round*7919 + 17)
		store := credentialpoolstore.NewMemory()
		writer := &interleaveWriter{secrets: map[string]admin.SecretOwner{}}
		h := admin.NewRouter(tenantstore.NewMemory(), admin.Options{CredentialsNamespace: interleaveNS}).
			WithCredentialPools(store).WithCredentialSecretWriter(writer).Handler()
		pools := []string{"p1", "p2"}
		for _, name := range pools {
			if err := store.Create(context.Background(), credentialpoolstore.CredentialPool{
				TenantID: "acme", Name: name, Provider: "anthropic_direct",
			}); err != nil {
				t.Fatalf("seed %s: %v", name, err)
			}
		}
		var wg sync.WaitGroup
		for w := 0; w < workers; w++ {
			wg.Add(1)
			go func(w int) {
				defer wg.Done()
				rng := rand.New(rand.NewSource(seed + int64(w)))
				for i := 0; i < opsEach; i++ {
					if code := interleaveOp(h, rng, pools[rng.Intn(len(pools))]); code >= 500 {
						t.Errorf("round %d seed %d: status %d with no injected fault", round, seed, code)
					}
				}
			}(w)
		}
		wg.Wait()
		checkSecretInvariants(t, store, writer)
		if t.Failed() {
			t.Fatalf("invariant violated in round %d (seed %d)", round, seed)
		}
	}
}
