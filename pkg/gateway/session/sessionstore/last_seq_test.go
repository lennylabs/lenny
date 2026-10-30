// SPDX-License-Identifier: MIT

package sessionstore_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionevents"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

func seededStore(t *testing.T) *memstore.Store {
	t.Helper()
	st := memstore.New()
	if err := st.Create(context.Background(), sessionstore.Session{
		ID: "s1", TenantID: "acme", State: session.StateRunning, CreatedAt: windowNow, UpdatedAt: windowNow,
	}); err != nil {
		t.Fatalf("create: %v", err)
	}
	return st
}

// spec: 7.3 (Retry and Resume), 10.4 (Gateway Reliability)
// diagnosis: the last_seq adapter rewound the persisted counter on a late
// advance, failed a load or advance for a missing row, or did not seed a
// second replica's bus from the row, so a replica publishing for a session
// another replica coordinates reused the coordinator's sequence numbers.
func TestLastSeqCounterSeedsASecondReplicaBus_spec_7_3(t *testing.T) {
	ctx := context.Background()
	st := seededStore(t)
	counter := sessionstore.LastSeqCounter{Sessions: st}

	t.Run("advance is monotonic", func(t *testing.T) {
		for _, seq := range []int64{3, 1, 5, 4} {
			if err := counter.AdvanceLastSeq(ctx, "acme", "s1", seq); err != nil {
				t.Fatalf("advance %d: %v", seq, err)
			}
		}
		if got, err := counter.LoadLastSeq(ctx, "acme", "s1"); err != nil || got != 5 {
			t.Fatalf("load = %d, %v; want 5", got, err)
		}
	})

	t.Run("missing row reads zero and advances nothing", func(t *testing.T) {
		if got, err := counter.LoadLastSeq(ctx, "acme", "absent"); err != nil || got != 0 {
			t.Fatalf("load absent = %d, %v; want 0, nil", got, err)
		}
		if err := counter.AdvanceLastSeq(ctx, "acme", "absent", 7); err != nil {
			t.Fatalf("advance absent: %v", err)
		}
		if got, err := counter.LoadLastSeq(ctx, "globex", "s1"); err != nil || got != 0 {
			t.Fatalf("load cross-tenant = %d, %v; want 0, nil", got, err)
		}
	})

	t.Run("store error propagates", func(t *testing.T) {
		failing := sessionstore.LastSeqCounter{Sessions: failingStore{Store: st}}
		if _, err := failing.LoadLastSeq(ctx, "acme", "s1"); !errors.Is(err, errStoreDown) {
			t.Fatalf("load err = %v, want errStoreDown", err)
		}
		if err := failing.AdvanceLastSeq(ctx, "acme", "s1", 9); !errors.Is(err, errStoreDown) {
			t.Fatalf("advance err = %v, want errStoreDown", err)
		}
	})

	t.Run("second replica continues the sequence", func(t *testing.T) {
		st := seededStore(t)
		counter := sessionstore.LastSeqCounter{Sessions: st}
		holder := sessionevents.NewBus(16).WithLastSeqPersister(counter).WithLastSeqLoader(counter)
		waiter := sessionevents.NewBus(16).WithLastSeqPersister(counter).WithLastSeqLoader(counter)
		first := holder.PublishForTenant("acme", "s1", "status_change", `{"state":"running"}`, windowNow)
		deadline := time.Now().Add(5 * time.Second)
		for {
			row, err := st.Get(ctx, "acme", "s1")
			if err == nil && row.LastSeq >= int64(first.Seq) {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("last_seq never reached %d", first.Seq)
			}
			time.Sleep(5 * time.Millisecond)
		}
		next := waiter.PublishForTenant("acme", "s1", "status_change", `{"state":"input_required"}`, windowNow)
		if next.Seq != first.Seq+1 {
			t.Fatalf("second replica's first seq = %d, want %d", next.Seq, first.Seq+1)
		}
	})
}

var errStoreDown = errors.New("store down")

// failingStore fails every Get and Update.
type failingStore struct{ sessionstore.Store }

func (failingStore) Get(context.Context, string, string) (sessionstore.Session, error) {
	return sessionstore.Session{}, errStoreDown
}

func (failingStore) Update(context.Context, string, string, func(*sessionstore.Session) error) (sessionstore.Session, error) {
	return sessionstore.Session{}, errStoreDown
}
