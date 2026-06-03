// SPDX-License-Identifier: MIT

package eventsubscription_test

import (
	"context"
	"testing"
	"time"

	es "github.com/lennylabs/lenny/pkg/ops/eventsubscription"
)

// TestServiceFiresChangeAndSecretHooks is the §25.5 lines 2747-2756
// contract: Create and RotateSecret hand the plaintext secret to the
// reveal cache and signal a cache change; Delete signals a removal and a
// change.
func TestServiceFiresChangeAndSecretHooks_spec_25_5_2751(t *testing.T) {
	svc, _ := newService()

	var changes []string
	var secrets []string
	var removed []string
	svc.OnChange = func(_ context.Context, id string) { changes = append(changes, id) }
	svc.OnSecret = func(id, secret string, _ int64) { secrets = append(secrets, id+"="+secret) }
	svc.OnRemove = func(id string) { removed = append(removed, id) }

	reveal, err := svc.Create(context.Background(), es.CreateRequest{CallbackURL: "https://hook.acme.com"}, platformAdmin)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	id := reveal.ID
	if len(secrets) != 1 || secrets[0] != id+"="+reveal.Secret {
		t.Fatalf("OnSecret = %v, want one create secret for %s", secrets, id)
	}
	if len(changes) != 1 || changes[0] != id {
		t.Fatalf("OnChange after create = %v, want [%s]", changes, id)
	}

	rot, err := svc.RotateSecret(context.Background(), id, platformAdmin)
	if err != nil {
		t.Fatalf("RotateSecret: %v", err)
	}
	if len(secrets) != 2 || secrets[1] != id+"="+rot.Secret {
		t.Fatalf("OnSecret after rotate = %v, want the new secret", secrets)
	}
	if len(changes) != 2 {
		t.Fatalf("OnChange after rotate = %v, want 2", changes)
	}

	if err := svc.Delete(context.Background(), id, platformAdmin); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if len(removed) != 1 || removed[0] != id {
		t.Fatalf("OnRemove = %v, want [%s]", removed, id)
	}
	if len(changes) != 3 {
		t.Fatalf("OnChange after delete = %v, want 3", changes)
	}
}

// TestMemoryStoreDeleteExpired is the §25.5 lines 2649-2664 retention
// contract: DeleteExpired removes only rows whose expires_at is at or
// before the cutoff, bounded by limit.
func TestMemoryStoreDeleteExpired_spec_25_5_2661(t *testing.T) {
	store := es.NewMemoryStore()
	now := time.Now().UTC()
	add := func(id string, expires time.Time) {
		if _, err := store.RecordDelivery(context.Background(), es.Delivery{
			SubscriptionID: "sub-1", EventID: id, Status: es.DeliveryDelivered, ExpiresAt: expires,
		}); err != nil {
			t.Fatalf("RecordDelivery: %v", err)
		}
	}
	add("old-1", now.Add(-2*time.Hour))
	add("old-2", now.Add(-1*time.Hour))
	add("fresh", now.Add(24*time.Hour))

	// A limit of 1 removes only the first expired row.
	n, err := store.DeleteExpired(context.Background(), now, 1)
	if err != nil || n != 1 {
		t.Fatalf("DeleteExpired(limit 1) = %d,%v, want 1,nil", n, err)
	}
	// The rest of the expired rows go in the next sweep; the fresh row
	// survives.
	n, err = store.DeleteExpired(context.Background(), now, 100)
	if err != nil || n != 1 {
		t.Fatalf("DeleteExpired(limit 100) = %d,%v, want 1,nil", n, err)
	}
	left, _ := store.ListDeliveries(context.Background(), "sub-1", 100)
	if len(left) != 1 || left[0].EventID != "fresh" {
		t.Fatalf("remaining = %+v, want only the fresh row", left)
	}
}
