//go:build component

// SPDX-License-Identifier: MIT

package stores_test

import (
	"bytes"
	"context"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/signingkeys"
	signingkeyspg "github.com/lennylabs/lenny/pkg/signingkeys/pgstore"
)

// spec: §7.1 (Normal Flow), §12.3 (Postgres HA Requirements)
// diagnosis: the Postgres session store dropped the uploadToken consumed
// mark that create-and-start writes into the row it creates. A session
// that POST /v1/sessions/start created would then admit uploads with its
// token, which §7.1 consumes at creation.
func TestSessionStoreCreatesARowWithItsTokenConsumed(t *testing.T) {
	t.Parallel()
	store, pg := startStore(t)
	ctx := context.Background()
	tenant := freshTenant(t, ctx, pg)
	id := newUUID(t)
	created := time.Now().UTC().Truncate(time.Microsecond)
	if err := store.Create(ctx, sessionstore.Session{
		ID: id, TenantID: tenant, State: session.StateRunning, RuntimeRef: "echo",
		CreatedAt: created, UpdatedAt: created, UploadTokenDigest: "d1", UploadTokenConsumedAt: created,
	}); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get(ctx, tenant, id)
	if err != nil || !got.UploadTokenConsumedAt.Equal(created) || got.UploadTokenDigest != "d1" {
		t.Fatalf("created row: consumed %v digest %q, %v; want consumed %v digest d1", got.UploadTokenConsumedAt, got.UploadTokenDigest, err, created)
	}
}

// spec: §7.1 (Normal Flow), §12.3 (Postgres HA Requirements)
// diagnosis: a key written at a slot whose row was deleted moved the
// previous key's retire_after or delete_after later in Postgres. Deleting
// the newest upload-token key row by hand would then let replicas sign
// with the replaced key again, or keep it verifying longer than its
// verification time.
func TestSigningKeyStoreNeverExtendsTheReplacedKey(t *testing.T) {
	t.Parallel()
	_, pg := startStore(t)
	ctx := context.Background()
	store, err := signingkeyspg.New(pg.Pool)
	if err != nil {
		t.Fatal(err)
	}
	t0 := time.Now().UTC().Truncate(time.Microsecond)
	rec := func(id string, slot int64, active time.Time) signingkeys.Record {
		return signingkeys.Record{
			Purpose: signingkeys.PurposeUploadToken, KeyID: id, Algorithm: signingkeys.AlgorithmHMACSHA256,
			KEKAlias: signingkeys.AliasUploadTokenSigning, WrappedSecret: []byte(id), RotationSlot: slot, ActiveFrom: active,
		}
	}
	const verifyFor = 10 * time.Minute
	for _, r := range []signingkeys.Record{rec("k1", 1, t0), rec("k2", 2, t0.Add(time.Hour))} {
		if ok, err := store.Insert(ctx, r, verifyFor); !ok || err != nil {
			t.Fatalf("insert %s = %v, %v", r.KeyID, ok, err)
		}
	}
	if _, err := pg.Pool.Exec(ctx, `DELETE FROM platform_signing_keys WHERE key_id = 'k2'`); err != nil {
		t.Fatal(err)
	}
	if ok, err := store.Insert(ctx, rec("k3", 2, t0.Add(2*time.Hour)), verifyFor); !ok || err != nil {
		t.Fatalf("insert at the freed slot = %v, %v", ok, err)
	}
	recs, err := store.List(ctx, signingkeys.PurposeUploadToken, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	wantRetire := t0.Add(time.Hour)
	if len(recs) != 2 || !recs[0].RetireAfter.Equal(wantRetire) || !recs[0].DeleteAfter.Equal(wantRetire.Add(verifyFor)) {
		t.Fatalf("k1 after the rewrite = %+v, want retire %v delete %v", recs[0], wantRetire, wantRetire.Add(verifyFor))
	}

	// A rewrite activating earlier than the deleted key moves both earlier.
	if _, err := pg.Pool.Exec(ctx, `DELETE FROM platform_signing_keys WHERE key_id = 'k3'`); err != nil {
		t.Fatal(err)
	}
	earlier := t0.Add(30 * time.Minute)
	if ok, err := store.Insert(ctx, rec("k4", 2, earlier), verifyFor); !ok || err != nil {
		t.Fatalf("insert an earlier key at the freed slot = %v, %v", ok, err)
	}
	recs, err = store.List(ctx, signingkeys.PurposeUploadToken, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if !recs[0].RetireAfter.Equal(earlier) || !recs[0].DeleteAfter.Equal(earlier.Add(verifyFor)) {
		t.Errorf("k1 after an earlier rewrite = retire %v delete %v, want %v and %v", recs[0].RetireAfter, recs[0].DeleteAfter, earlier, earlier.Add(verifyFor))
	}
}

// spec: §7.1 (Normal Flow), §15.1 (REST API)
// diagnosis: a forced rotation over the Postgres key store did not write
// the next key one publish lead ahead, or a peer replica over the same
// table did not load it before its activation; a replica would then sign
// with a key its peers cannot verify.
func TestAForcedRotationOverPostgresReachesThePeerBeforeItsActivation(t *testing.T) {
	t.Parallel()
	_, pg := startStore(t)
	ctx := context.Background()
	store, err := signingkeyspg.New(pg.Pool)
	if err != nil {
		t.Fatal(err)
	}
	master := bytes.Repeat([]byte{7}, 32)
	now := time.Now().UTC().Truncate(time.Microsecond)
	var mu sync.Mutex
	clock := func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return now
	}
	a := openUploadRing(t, store, master, clock)
	b := openUploadRing(t, store, master, clock)
	first, err := a.SigningKey()
	if err != nil {
		t.Fatal(err)
	}
	rot, err := a.RotateNow(ctx)
	if err != nil {
		t.Fatalf("RotateNow: %v", err)
	}
	if !rot.ActiveFrom.Equal(now.Add(signingkeys.DefaultPublishLead)) || rot.RotationSlot != first.RotationSlot+1 {
		t.Errorf("rotation = %+v, want slot %d active one publish lead ahead", rot, first.RotationSlot+1)
	}
	if err := b.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, k := range b.VerificationKeys() {
		found = found || k.KeyID == rot.KeyID
	}
	if !found {
		t.Fatalf("peer verification keys lack the rotated key %s", rot.KeyID)
	}
	mu.Lock()
	now = now.Add(signingkeys.DefaultPublishLead)
	mu.Unlock()
	for name, r := range map[string]*signingkeys.Ring{"a": a, "b": b} {
		if err := r.Refresh(ctx); err != nil {
			t.Fatal(err)
		}
		k, err := r.SigningKey()
		if err != nil || k.KeyID != rot.KeyID {
			t.Errorf("replica %s signs with %q, %v; want %s", name, k.KeyID, err, rot.KeyID)
		}
	}
}
