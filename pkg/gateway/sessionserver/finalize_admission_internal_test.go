// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

// finalizeTestServerWithStore builds the minimal finalize test server (no
// pod binder) over a wrapped store.
func finalizeTestServerWithStore(store sessionstore.Store) *Server {
	clock := func() time.Time { return time.Date(2026, 6, 8, 9, 0, 0, 0, time.UTC) }
	return New(store, Options{Clock: clock})
}

// barrierGetStore holds every Get for one session id until `parties` Gets
// have arrived, so each concurrent finalize call reads the pre-lock row
// before any of them reaches its entry Update. Without the barrier a second
// call that starts after the first commits reads `finalizing` in its
// pre-lock Validate, and the test would pass without the locked re-check.
type barrierGetStore struct {
	sessionstore.Store
	id      string
	arrived sync.WaitGroup
}

func newBarrierGetStore(inner sessionstore.Store, id string, parties int) *barrierGetStore {
	b := &barrierGetStore{Store: inner, id: id}
	b.arrived.Add(parties)
	return b
}

func (b *barrierGetStore) Get(ctx context.Context, tenantID, id string) (sessionstore.Session, error) {
	row, err := b.Store.Get(ctx, tenantID, id)
	if id == b.id {
		b.arrived.Done()
		b.arrived.Wait()
	}
	return row, err
}

// updateErrStore fails every Update with a fixed error, standing in for a
// row deleted between the pre-lock read and the entry write, or a store
// outage at that write.
type updateErrStore struct {
	sessionstore.Store
	err error
}

func (u *updateErrStore) Update(context.Context, string, string, func(*sessionstore.Session) error) (sessionstore.Session, error) {
	return sessionstore.Session{}, u.err
}

// TestFinalizeOverlappingCallsAdmitExactlyOne_spec_15_1 drives two /finalize
// calls that both read `created` before either entry write. The entry write
// re-checks the finalize precondition against the locked row, so exactly one
// call commits and returns 200, and the other answers 409
// INVALID_STATE_TRANSITION naming the state the winner wrote. Before the
// locked re-check both calls committed `finalizing` and both returned 200.
// spec: 15.1 (REST API, State-mutating endpoint preconditions, finalize row), 7.1 (Normal Flow, steps 11-13)
func TestFinalizeOverlappingCallsAdmitExactlyOne_spec_15_1(t *testing.T) {
	inner := memstore.New()
	seedCreated(t, inner, "sess-race", "")
	srv := finalizeTestServerWithStore(newBarrierGetStore(inner, "sess-race", 2))

	codes := make([]int, 2)
	bodies := make([][]byte, 2)
	var wg sync.WaitGroup
	for i := range codes {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			rr := postFinalize(t, srv, "sess-race", "")
			codes[i] = rr.Code
			bodies[i] = rr.Body.Bytes()
		}(i)
	}
	wg.Wait()

	var ok, conflict int
	for i, code := range codes {
		switch code {
		case http.StatusOK:
			ok++
		case http.StatusConflict:
			conflict++
			assertInvalidTransitionBody(t, bodies[i])
		default:
			t.Fatalf("call %d: status %d, body %s", i, code, bodies[i])
		}
	}
	if ok != 1 || conflict != 1 {
		t.Fatalf("statuses %v: want exactly one 200 and one 409", codes)
	}
	row, err := inner.Get(context.Background(), "default", "sess-race")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if row.State != session.StateReady {
		t.Fatalf("state = %s, want ready", row.State)
	}
}

// assertInvalidTransitionBody checks the refused call's envelope carries the
// §15.1 precondition error with the locked state the winner committed.
func assertInvalidTransitionBody(t *testing.T, body []byte) {
	t.Helper()
	var env struct {
		Error struct {
			Code    string         `json:"code"`
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("decode 409 body %s: %v", body, err)
	}
	if env.Error.Code != "INVALID_STATE_TRANSITION" {
		t.Fatalf("code = %q, want INVALID_STATE_TRANSITION (body %s)", env.Error.Code, body)
	}
	switch env.Error.Details["currentState"] {
	case string(session.StateFinalizing), string(session.StateReady):
	default:
		t.Fatalf("details.currentState = %v, want finalizing or ready (body %s)",
			env.Error.Details["currentState"], body)
	}
}

// TestFinalizeEntryWriteErrors_spec_15_1 pins the entry write's error
// mapping: a row that vanished between the pre-lock read and the locked
// write answers 404 RESOURCE_NOT_FOUND, and any other store error answers
// 500 INTERNAL_ERROR.
// spec: 15.1 (REST API, State-mutating endpoint preconditions, finalize row)
func TestFinalizeEntryWriteErrors_spec_15_1(t *testing.T) {
	cases := []struct {
		name     string
		err      error
		wantCode int
		wantErr  string
	}{
		{"row deleted after pre-lock read", sessionstore.ErrNotFound, http.StatusNotFound, "RESOURCE_NOT_FOUND"},
		{"store outage", errors.New("connection reset"), http.StatusInternalServerError, "INTERNAL_ERROR"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			inner := memstore.New()
			seedCreated(t, inner, "sess-err", "")
			srv := finalizeTestServerWithStore(&updateErrStore{Store: inner, err: tc.err})
			rr := postFinalize(t, srv, "sess-err", "")
			if rr.Code != tc.wantCode {
				t.Fatalf("status = %d, want %d (body %s)", rr.Code, tc.wantCode, rr.Body.String())
			}
			var env struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
				t.Fatalf("decode body: %v", err)
			}
			if env.Error.Code != tc.wantErr {
				t.Fatalf("code = %q, want %q", env.Error.Code, tc.wantErr)
			}
			row, err := inner.Get(context.Background(), "default", "sess-err")
			if err != nil || row.State != session.StateCreated {
				t.Fatalf("row state = %s (err %v), want created", row.State, err)
			}
		})
	}
}
