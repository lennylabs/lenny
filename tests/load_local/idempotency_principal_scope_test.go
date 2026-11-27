// SPDX-License-Identifier: MIT

//go:build load_local

// load_local concurrency coverage for the principal scope of §11.5
// idempotency records. Requests from two users of one tenant race on one
// Idempotency-Key and one body through the idempotency middleware over the
// in-memory store.
package loadlocal_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/lennylabs/lenny/pkg/idempotency/idempotencymw"
)

// spec: §11.5 (Idempotency)
// diagnosis: concurrent requests from alice and bob with one key and one
// body did not each run the operation exactly once, or a response carried
// the other user's result. Claims under two subjects must address two
// records (idempotency.Key carries the subject), and claims under one
// subject must collapse to one execution through the atomic Claim of the
// store; a lost-update or a shared map entry in MemoryStore shows up here
// under -race.
func TestConcurrentClaimsOfOneKeyByTwoSubjectsEachRunOnce(t *testing.T) {
	for round := 0; round < 50; round++ {
		runPrincipalScopeRound(t, round)
	}
}

// runPrincipalScopeRound sends perUser concurrent requests from each of
// alice and bob, released together, and checks the executions and the
// bodies each user received.
func runPrincipalScopeRound(t *testing.T, round int) {
	t.Helper()
	const perUser = 16
	var runs sync.Map // subject -> *atomic.Int64
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		subject := r.Header.Get("X-Test-Subject")
		n, _ := runs.LoadOrStore(subject, new(atomic.Int64))
		n.(*atomic.Int64).Add(1)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]string{"owner": subject})
	})
	h := idempotencymw.Wrap(inner, idempotencymw.NewMemoryStore(), idempotencymw.Options{
		PrincipalFromRequest: func(r *http.Request) (string, string, string) {
			return "acme", r.Header.Get("X-Test-Subject"), ""
		},
	})

	key := fmt.Sprintf("round-%d", round)
	start := make(chan struct{})
	var wg sync.WaitGroup
	var crossed atomic.Int64
	for _, subject := range []string{"alice", "bob"} {
		for i := 0; i < perUser; i++ {
			wg.Add(1)
			go func(subject string) {
				defer wg.Done()
				req := httptest.NewRequest(http.MethodPost, "/v1/sessions", strings.NewReader(`{"runtimeRef":"echo"}`))
				req.Header.Set(idempotencymw.HeaderName, key)
				req.Header.Set("X-Test-Subject", subject)
				rec := httptest.NewRecorder()
				<-start
				h.ServeHTTP(rec, req)
				if rec.Code == http.StatusCreated && !strings.Contains(rec.Body.String(), `"owner":"`+subject+`"`) {
					crossed.Add(1)
				}
			}(subject)
		}
	}
	close(start)
	wg.Wait()

	if crossed.Load() != 0 {
		t.Fatalf("round %d: %d responses carried the other user's result", round, crossed.Load())
	}
	for _, subject := range []string{"alice", "bob"} {
		n, ok := runs.Load(subject)
		if !ok {
			t.Fatalf("round %d: %s's operation never ran", round, subject)
		}
		if got := n.(*atomic.Int64).Load(); got != 1 {
			t.Fatalf("round %d: %s's operation ran %d times, want 1", round, subject, got)
		}
	}
}
