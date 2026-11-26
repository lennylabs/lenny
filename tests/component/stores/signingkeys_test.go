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
	"github.com/lennylabs/lenny/pkg/kms"
	"github.com/lennylabs/lenny/pkg/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/signingkeys"
	signingkeyspg "github.com/lennylabs/lenny/pkg/signingkeys/pgstore"
	"github.com/lennylabs/lenny/pkg/uploadtoken"
)

// openUploadRing opens one replica's upload-token ring over the Postgres
// key store, with the local KMS provider over master.
func openUploadRing(t *testing.T, store signingkeys.Store, master []byte, clock func() time.Time) *signingkeys.Ring {
	t.Helper()
	provider, err := kms.NewLocal(master)
	if err != nil {
		t.Fatal(err)
	}
	sealer, err := signingkeys.NewEnvelopeSealer(provider, signingkeys.AliasUploadTokenSigning)
	if err != nil {
		t.Fatal(err)
	}
	ring, err := signingkeys.Open(context.Background(),
		signingkeys.UploadTokenPolicy(uploadtoken.DefaultRotationInterval, uploadtoken.DefaultOverlapWindow, uploadtoken.DefaultTTL),
		store, sealer, signingkeys.Options{StaleAfter: uploadtoken.DefaultOverlapWindow, Clock: clock})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return ring
}

// spec: §7.1 (Normal Flow), §12.3 (Postgres HA Requirements)
// diagnosis: the Postgres signing-key store does not share one wrapped
// upload-token key between replicas. Either the first key was not written
// or was written twice, the secret reached platform_signing_keys in
// plaintext, a second replica over the same table and KMS master key
// could not unwrap it, or a rotation did not retire the previous key with
// the next key's activation instant plus the overlap window. A token one
// gateway replica mints would then fail verification on another.
func TestSigningKeyStoreSharesOneWrappedKeyBetweenReplicas(t *testing.T) {
	t.Parallel()
	_, pg := startStore(t)
	ctx := context.Background()
	store, err := signingkeyspg.New(pg.Pool)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	clock := func() time.Time { return now }
	master := bytes.Repeat([]byte{0x11}, 32)
	a := openUploadRing(t, store, master, clock)
	b := openUploadRing(t, store, master, clock)
	ka, err := a.SigningKey()
	if err != nil {
		t.Fatal(err)
	}
	kb, err := b.SigningKey()
	if err != nil {
		t.Fatal(err)
	}
	if ka.KeyID != kb.KeyID || !bytes.Equal(ka.Secret, kb.Secret) {
		t.Fatalf("replicas loaded different keys: %s and %s", ka.KeyID, kb.KeyID)
	}
	var (
		count   int
		wrapped []byte
		alias   string
	)
	if err := pg.Pool.QueryRow(ctx, `SELECT count(*) FROM platform_signing_keys WHERE purpose = $1`, signingkeys.PurposeUploadToken).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("rows = %d, want 1", count)
	}
	if err := pg.Pool.QueryRow(ctx, `SELECT wrapped_secret, kek_alias FROM platform_signing_keys WHERE key_id = $1`, ka.KeyID).Scan(&wrapped, &alias); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(wrapped, ka.Secret) {
		t.Error("platform_signing_keys holds the plaintext secret")
	}
	if alias != signingkeys.AliasUploadTokenSigning {
		t.Errorf("kek_alias = %q, want %q", alias, signingkeys.AliasUploadTokenSigning)
	}

	// The next key became active ten minutes ago, so the previous key's
	// verification time ended five minutes ago on every clock.
	next := signingkeys.Record{
		Purpose: signingkeys.PurposeUploadToken, KeyID: "next", Algorithm: signingkeys.AlgorithmHMACSHA256,
		KEKAlias: signingkeys.AliasUploadTokenSigning, WrappedSecret: wrapped, RotationSlot: 2, ActiveFrom: now.Add(-10 * time.Minute),
	}
	if ok, err := store.Insert(ctx, next, 5*time.Minute); !ok || err != nil {
		t.Fatalf("Insert next = %v, %v", ok, err)
	}
	recs, err := store.List(ctx, signingkeys.PurposeUploadToken, time.Time{})
	if err != nil || len(recs) != 2 {
		t.Fatalf("List = %d keys, %v; want 2", len(recs), err)
	}
	if !recs[0].RetireAfter.Equal(next.ActiveFrom) || !recs[0].DeleteAfter.Equal(next.ActiveFrom.Add(5*time.Minute)) {
		t.Errorf("previous key retire %s delete %s, want %s and %s", recs[0].RetireAfter, recs[0].DeleteAfter, next.ActiveFrom, next.ActiveFrom.Add(5*time.Minute))
	}
	if live, _ := store.List(ctx, signingkeys.PurposeUploadToken, now); len(live) != 1 {
		t.Errorf("keys still verifying = %d, want the next key alone", len(live))
	}
	// A caller clock far ahead of the database's deletes only what the
	// database clock also holds expired.
	future := signingkeys.Record{
		Purpose: signingkeys.PurposeUploadToken, KeyID: "future", Algorithm: signingkeys.AlgorithmHMACSHA256,
		KEKAlias: signingkeys.AliasUploadTokenSigning, WrappedSecret: wrapped, RotationSlot: 3, ActiveFrom: now.Add(time.Hour),
	}
	if ok, err := store.Insert(ctx, future, 5*time.Minute); !ok || err != nil {
		t.Fatalf("Insert future = %v, %v", ok, err)
	}
	if n, err := store.DeleteExpired(ctx, signingkeys.PurposeUploadToken, now.Add(24*time.Hour)); err != nil || n != 1 {
		t.Errorf("DeleteExpired with a fast caller clock = %d, %v; want the one key expired on the database clock", n, err)
	}
	if recs, _ := store.List(ctx, signingkeys.PurposeUploadToken, time.Time{}); len(recs) != 2 || recs[0].KeyID != "next" {
		t.Errorf("keys after the sweep = %+v, want next and future", recs)
	}
}

// spec: §7.1 (Normal Flow), §12.3 (Postgres HA Requirements)
// diagnosis: the unique (purpose, rotation_slot) constraint of
// platform_signing_keys did not make concurrent writers of one slot
// produce one key. Two replicas rotating at once would then publish two
// next keys and sign with different secrets.
func TestSigningKeyStoreSlotConstraintHoldsUnderConcurrentInserts(t *testing.T) {
	t.Parallel()
	_, pg := startStore(t)
	ctx := context.Background()
	store, err := signingkeyspg.New(pg.Pool)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	const writers = 12
	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		inserted int
	)
	for i := range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, err := store.Insert(ctx, signingkeys.Record{
				Purpose: signingkeys.PurposeUploadToken, KeyID: "k-" + string(rune('a'+i)), Algorithm: signingkeys.AlgorithmHMACSHA256,
				KEKAlias: signingkeys.AliasUploadTokenSigning, WrappedSecret: []byte{byte(i)}, RotationSlot: 1, ActiveFrom: now,
			}, time.Minute)
			if err != nil {
				t.Errorf("Insert: %v", err)
				return
			}
			if ok {
				mu.Lock()
				inserted++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if inserted != 1 {
		t.Errorf("writers that won the slot = %d, want 1", inserted)
	}
	var count int
	if err := pg.Pool.QueryRow(ctx, `SELECT count(*) FROM platform_signing_keys WHERE purpose = $1 AND rotation_slot = 1`, signingkeys.PurposeUploadToken).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("rows at slot 1 = %d, want 1", count)
	}
}

// spec: §7.1 (Normal Flow)
// diagnosis: the Postgres session store did not persist the uploadToken
// consumed mark the finalize ready write sets, or a later write cleared or
// moved it. Another gateway replica would then admit an upload or a second
// finalize with a token FinalizeWorkspace already invalidated.
func TestSessionStoreKeepsTheUploadTokenConsumedMark(t *testing.T) {
	t.Parallel()
	store, pg := startStore(t)
	ctx := context.Background()
	tenant := freshTenant(t, ctx, pg)
	id := newUUID(t)
	if err := store.Create(ctx, sessionstore.Session{ID: id, TenantID: tenant, State: session.StateFinalizing, RuntimeRef: "echo"}); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get(ctx, tenant, id)
	if err != nil || !got.UploadTokenConsumedAt.IsZero() {
		t.Fatalf("new row consumed mark = %v, %v; want unset", got.UploadTokenConsumedAt, err)
	}
	consumed := time.Now().UTC().Truncate(time.Microsecond)
	if _, err := store.Update(ctx, tenant, id, func(row *sessionstore.Session) error {
		row.State = session.StateReady
		row.UploadTokenConsumedAt = consumed
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	got, err = store.Get(ctx, tenant, id)
	if err != nil || !got.UploadTokenConsumedAt.Equal(consumed) {
		t.Fatalf("consumed mark = %v, %v; want %v", got.UploadTokenConsumedAt, err, consumed)
	}
	out, err := store.Update(ctx, tenant, id, func(row *sessionstore.Session) error {
		row.UploadTokenConsumedAt = time.Time{}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !out.UploadTokenConsumedAt.Equal(consumed) {
		t.Errorf("Update returned consumed mark %v after a write that cleared it, want %v", out.UploadTokenConsumedAt, consumed)
	}
	got, _ = store.Get(ctx, tenant, id)
	if !got.UploadTokenConsumedAt.Equal(consumed) {
		t.Errorf("stored consumed mark = %v after a write that cleared it, want %v", got.UploadTokenConsumedAt, consumed)
	}
}
