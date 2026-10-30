// SPDX-License-Identifier: MIT

package scenkit

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/loadgen"
)

func TestCountersIncAddGet(t *testing.T) {
	c := NewCounters()
	c.Inc("hits")
	c.Inc("hits")
	c.Add("bytes", 42)
	if got := c.Get("hits"); got != 2 {
		t.Errorf("hits=%d want 2", got)
	}
	if got := c.Get("bytes"); got != 42 {
		t.Errorf("bytes=%d want 42", got)
	}
	if got := c.Get("missing"); got != 0 {
		t.Errorf("missing=%d want 0", got)
	}
}

func TestCountersIncOnErrorSkipsBenignCancel(t *testing.T) {
	c := NewCounters()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c.IncOnError(ctx, "failures", context.Canceled)
	if got := c.Get("failures"); got != 0 {
		t.Errorf("failures=%d want 0 after benign cancel", got)
	}
	c.IncOnError(context.Background(), "failures", http.ErrAbortHandler)
	if got := c.Get("failures"); got != 1 {
		t.Errorf("failures=%d want 1 for real error", got)
	}
}

func TestCountersIncIsRaceSafe(t *testing.T) {
	c := NewCounters()
	const N = 500
	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.Inc("ops")
		}()
	}
	wg.Wait()
	if got := c.Get("ops"); got != N {
		t.Errorf("ops=%d want %d", got, N)
	}
}

func TestCountersEmitToOrderedAndCovered(t *testing.T) {
	c := NewCounters()
	c.Add("z", 9)
	c.Add("a", 1)
	c.Add("m", 5)
	r := &loadgen.Result{Custom: map[string]float64{}}
	c.EmitTo(r)
	for _, k := range []string{"a", "m", "z"} {
		if _, ok := r.Custom[k]; !ok {
			t.Errorf("missing custom metric %q", k)
		}
	}
}

func TestDoJSONStatusAndBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Idempotency-Key") != "k-1" {
			t.Errorf("missing header")
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("Content-Type=%q want application/json", r.Header.Get("Content-Type"))
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()
	status, body, err := DoJSON(context.Background(), "POST", srv.URL, []byte(`{}`), H("Idempotency-Key", "k-1"))
	if err != nil {
		t.Fatalf("DoJSON: %v", err)
	}
	if status != http.StatusCreated {
		t.Errorf("status=%d want 201", status)
	}
	if !strings.Contains(string(body), "ok") {
		t.Errorf("body=%q want contains 'ok'", body)
	}
}

func TestDoJSONReturnsTransportErrorOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := DoJSON(ctx, "GET", "http://127.0.0.1:1/", nil)
	if err == nil {
		t.Fatal("expected transport error on cancelled context")
	}
	if !IsBenignCancel(ctx, err) {
		t.Errorf("IsBenignCancel=false; want true for cancelled-ctx error")
	}
}
