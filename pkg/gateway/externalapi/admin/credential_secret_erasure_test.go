// SPDX-License-Identifier: MIT

package admin_test

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credentialpoolstore"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
	"github.com/lennylabs/lenny/tests/testinfra/credsecretfake"
)

const erasureNS = "lenny-credentials"

// faultPools wraps a credential pool store with injectable DeleteByTenant
// and List faults.
type faultPools struct {
	credentialpoolstore.Store
	failDelete error
	failList   error
	deletes    int
}

func (f *faultPools) DeleteByTenant(ctx context.Context, tenant string) (int, error) {
	f.deletes++
	if f.failDelete != nil {
		return 0, f.failDelete
	}
	return f.Store.DeleteByTenant(ctx, tenant)
}

func (f *faultPools) List(ctx context.Context, tenant string, filter credentialpoolstore.ListFilter) ([]credentialpoolstore.CredentialPool, error) {
	if f.failList != nil {
		return nil, f.failList
	}
	return f.Store.List(ctx, tenant, filter)
}

func writtenName(tenant, pool, id string) string {
	return credential.ManagedSecretName(tenant, pool, id)
}

func writtenRef(tenant, pool, id string) string {
	return credential.CanonicalSecretRef(erasureNS, writtenName(tenant, pool, id))
}

// seedErasurePool creates pool name for tenant with the given refs, one
// credential each, and soft-deletes it when deleted is set.
func seedErasurePool(t *testing.T, store credentialpoolstore.Store, tenant, name string, deleted bool, refs ...string) {
	t.Helper()
	ctx := context.Background()
	p := credentialpoolstore.CredentialPool{TenantID: tenant, Name: name, Provider: "anthropic_direct"}
	for i, ref := range refs {
		p.Credentials = append(p.Credentials, credentialpoolstore.Credential{ID: "key-" + string(rune('1'+i)), SecretRef: ref})
	}
	if err := store.Create(ctx, p); err != nil {
		t.Fatalf("seed pool %s/%s: %v", tenant, name, err)
	}
	if deleted {
		if err := store.SoftDelete(ctx, tenant, name, time.Now()); err != nil {
			t.Fatalf("soft-delete %s/%s: %v", tenant, name, err)
		}
	}
}

func tenantRows(t *testing.T, store credentialpoolstore.Store, tenant string) int {
	t.Helper()
	rows, err := store.List(context.Background(), tenant, credentialpoolstore.ListFilter{IncludeDeleted: true})
	if err != nil {
		t.Fatalf("List %s: %v", tenant, err)
	}
	return len(rows)
}

// TestTenantErasureDeletesWrittenSecretsAfterPoolRows_spec_4_9 pins the
// tenant-deletion row of the Secret operation table: the delete-collection
// runs after the CredentialPoolStore deletion has returned, removes every
// Secret carrying the tenant's label, including an orphan no stored
// reference names, and leaves another tenant's Secrets and an unlabelled
// operator Secret in place.
//
// spec: §4.9 (Credential Storage Topology) — Credential values submitted
// through the admin API rules 3 and 5; §12.8 (Compliance Interfaces)
func TestTenantErasureDeletesWrittenSecretsAfterPoolRows_spec_4_9(t *testing.T) {
	ctx := context.Background()
	store := credentialpoolstore.NewMemory()
	seedErasurePool(t, store, "acme", "p1", false, writtenRef("acme", "p1", "key-1"), erasureNS+"/operator-made")
	seedErasurePool(t, store, "acme", "p2", true, writtenRef("acme", "p2", "key-1"))
	seedErasurePool(t, store, "globex", "p1", false, writtenRef("globex", "p1", "key-1"))

	ns := credsecretfake.New()
	ns.Seed(writtenName("acme", "p1", "key-1"), "acme")
	ns.Seed(writtenName("acme", "p2", "key-1"), "acme")
	orphan := writtenName("acme", "gone", "key-9")
	ns.Seed(orphan, "acme")
	ns.Seed(writtenName("globex", "p1", "key-1"), "globex")
	ns.Seed("operator-made", "")

	rowsAtDeleteTenant := -1
	ns.BeforeDeleteTenant(func(context.Context, string) error {
		rowsAtDeleteTenant = tenantRows(t, store, "acme")
		return nil
	})

	n, err := admin.TenantCredentialPoolEraser(store, ns, erasureNS)(ctx, "acme")
	if err != nil {
		t.Fatalf("erase: %v", err)
	}
	if n != 2 {
		t.Fatalf("deleted rows = %d, want 2 (active and soft-deleted pool)", n)
	}
	if rowsAtDeleteTenant != 0 {
		t.Fatalf("pool rows when the delete-collection ran = %d, want 0: the delete-collection must follow the row deletion", rowsAtDeleteTenant)
	}
	if got, want := ns.Calls(), []string{"deleteTenant acme"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("writer calls = %v, want %v (one delete-collection and no per-reference Delete)", got, want)
	}
	if ns.Has(orphan) {
		t.Fatal("the orphan no stored reference names survived; the erasure must delete by label, not by reference")
	}
	want := []string{writtenName("globex", "p1", "key-1"), "operator-made"}
	sort.Strings(want)
	if got := ns.Names(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Secrets left = %v, want only %v", got, want)
	}
	if tenantRows(t, store, "globex") != 1 {
		t.Fatal("globex pool row did not survive acme's erasure")
	}
}

// TestTenantErasureSecretFailuresAbortForRetry_spec_12_8 pins the rule 5
// failure outcomes: a failed row deletion issues no delete-collection, and a
// failed delete-collection returns an error so Phase 4 stays and retries,
// and the retry deletes the Secrets.
//
// spec: §12.8 (Compliance Interfaces); §4.9 (Credential Storage Topology) —
// Credential values submitted through the admin API rule 5
func TestTenantErasureSecretFailuresAbortForRetry_spec_12_8(t *testing.T) {
	ctx := context.Background()
	t.Run("row deletion fails", func(t *testing.T) {
		boom := errors.New("pg down")
		pools := &faultPools{Store: credentialpoolstore.NewMemory(), failDelete: boom}
		ns := credsecretfake.New()
		_, err := admin.TenantCredentialPoolEraser(pools, ns, erasureNS)(ctx, "acme")
		if !errors.Is(err, boom) {
			t.Fatalf("err = %v, want the row deletion error", err)
		}
		if calls := ns.Calls(); len(calls) != 0 {
			t.Fatalf("writer calls = %v, want none after a failed row deletion", calls)
		}
	})
	t.Run("delete-collection fails then retries", func(t *testing.T) {
		store := credentialpoolstore.NewMemory()
		seedErasurePool(t, store, "acme", "p1", false, writtenRef("acme", "p1", "key-1"))
		ns := credsecretfake.New()
		ns.Seed(writtenName("acme", "p1", "key-1"), "acme")
		apiDown := errors.New("kube-apiserver unavailable")
		ns.BeforeDeleteTenant(func(context.Context, string) error { return apiDown })
		erase := admin.TenantCredentialPoolEraser(store, ns, erasureNS)
		if _, err := erase(ctx, "acme"); !errors.Is(err, apiDown) || !strings.Contains(err.Error(), "Lenny-written credential Secrets") {
			t.Fatalf("err = %v, want the wrapped delete-collection error", err)
		}
		if !ns.Has(writtenName("acme", "p1", "key-1")) {
			t.Fatal("the failed delete-collection deleted a Secret")
		}
		ns.BeforeDeleteTenant(nil)
		n, err := erase(ctx, "acme")
		if err != nil || n != 0 {
			t.Fatalf("retry = (%d, %v), want (0, nil)", n, err)
		}
		if ns.Has(writtenName("acme", "p1", "key-1")) {
			t.Fatal("the retried delete-collection left the tenant's Secret")
		}
	})
}

// TestTenantErasureWithoutWriter_spec_4_9 pins rule 6 for tenant deletion:
// with no writer, Phase 4 fails while any pool of the tenant, a
// soft-deleted one included, carries a Lenny-written reference, and
// otherwise deletes the rows with no delete-collection.
//
// spec: §4.9 (Credential Storage Topology) — Credential values submitted
// through the admin API rule 6; §12.8 (Compliance Interfaces)
func TestTenantErasureWithoutWriter_spec_4_9(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name    string
		seed    func(t *testing.T, s credentialpoolstore.Store)
		credNS  string
		refused bool
	}{
		{
			name: "only written reference is on a soft-deleted pool",
			seed: func(t *testing.T, s credentialpoolstore.Store) {
				seedErasurePool(t, s, "acme", "live", false, erasureNS+"/operator-made")
				seedErasurePool(t, s, "acme", "dead", true, writtenRef("acme", "dead", "key-1"))
			},
			credNS:  erasureNS,
			refused: true,
		},
		{
			name: "written reference on an active pool",
			seed: func(t *testing.T, s credentialpoolstore.Store) {
				seedErasurePool(t, s, "acme", "live", false, writtenRef("acme", "live", "key-1"))
			},
			credNS:  erasureNS,
			refused: true,
		},
		{
			name: "empty namespace resolves to the default and still refuses",
			seed: func(t *testing.T, s credentialpoolstore.Store) {
				seedErasurePool(t, s, "acme", "live", false, writtenRef("acme", "live", "key-1"))
			},
			credNS:  "",
			refused: true,
		},
		{
			name: "operator references only",
			seed: func(t *testing.T, s credentialpoolstore.Store) {
				seedErasurePool(t, s, "acme", "live", false, erasureNS+"/operator-made", "other-operator")
				seedErasurePool(t, s, "acme", "dead", true, erasureNS+"/old-operator")
			},
			credNS: erasureNS,
		},
		{
			name:   "no pools",
			seed:   func(*testing.T, credentialpoolstore.Store) {},
			credNS: erasureNS,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := credentialpoolstore.NewMemory()
			tc.seed(t, store)
			before := tenantRows(t, store, "acme")
			n, err := admin.TenantCredentialPoolEraser(store, nil, tc.credNS)(ctx, "acme")
			if tc.refused {
				if !errors.Is(err, admin.ErrLennyWrittenRefWithoutWriter) {
					t.Fatalf("err = %v, want ErrLennyWrittenRefWithoutWriter", err)
				}
				if got := tenantRows(t, store, "acme"); got != before {
					t.Fatalf("rows after refusal = %d, want %d unchanged", got, before)
				}
				return
			}
			if err != nil || n != before {
				t.Fatalf("erase = (%d, %v), want (%d, nil)", n, err, before)
			}
			if got := tenantRows(t, store, "acme"); got != 0 {
				t.Fatalf("rows left = %d, want 0", got)
			}
		})
	}
	t.Run("list fails", func(t *testing.T) {
		boom := errors.New("pg down")
		pools := &faultPools{Store: credentialpoolstore.NewMemory(), failList: boom}
		if _, err := admin.TenantCredentialPoolEraser(pools, nil, erasureNS)(ctx, "acme"); !errors.Is(err, boom) {
			t.Fatalf("err = %v, want the list error", err)
		}
		if pools.deletes != 0 {
			t.Fatal("rows deleted after the rule 6 read failed")
		}
	})
	t.Run("row deletion fails", func(t *testing.T) {
		boom := errors.New("pg down")
		pools := &faultPools{Store: credentialpoolstore.NewMemory(), failDelete: boom}
		if _, err := admin.TenantCredentialPoolEraser(pools, nil, erasureNS)(ctx, "acme"); !errors.Is(err, boom) {
			t.Fatalf("err = %v, want the row deletion error", err)
		}
	})
}
