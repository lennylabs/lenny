// SPDX-License-Identifier: MIT

package opsserver_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/lennylabs/lenny/pkg/ops/eventsubscription"
	"github.com/lennylabs/lenny/pkg/ops/opsserver"
	"github.com/lennylabs/lenny/pkg/ops/opsservice"
)

// countingInvalidator records how many times Invalidate was called.
type countingInvalidator struct{ calls atomic.Int64 }

func (c *countingInvalidator) Invalidate(context.Context) error {
	c.calls.Add(1)
	return nil
}

// TestCacheInvalidateRPC is the §25.5 line 2751 contract: a peer that
// presents the shared-secret-derived token triggers a cache refresh
// (204); a missing or wrong token is rejected (401) without refreshing.
func TestCacheInvalidateRPC_spec_25_5_2751(t *testing.T) {
	inv := &countingInvalidator{}
	token := opsservice.InvalidateToken([]byte("shared-key"))
	srv := opsserver.New(opsserver.Options{
		EventSubscriptions:   eventsubscription.NewService(eventsubscription.NewMemoryStore()),
		CacheInvalidator:     inv,
		CacheInvalidateToken: token,
	})

	post := func(tok string) int {
		req := httptest.NewRequest(http.MethodPost, opsservice.DefaultCacheInvalidatePath, nil)
		if tok != "" {
			req.Header.Set(opsservice.CacheInvalidateHeader, tok)
		}
		rr := httptest.NewRecorder()
		srv.ServeHTTP(rr, req)
		return rr.Code
	}

	if code := post(token); code != http.StatusNoContent {
		t.Errorf("valid token: status = %d, want 204", code)
	}
	if got := inv.calls.Load(); got != 1 {
		t.Errorf("Invalidate calls = %d, want 1", got)
	}
	if code := post("wrong"); code != http.StatusUnauthorized {
		t.Errorf("wrong token: status = %d, want 401", code)
	}
	if code := post(""); code != http.StatusUnauthorized {
		t.Errorf("missing token: status = %d, want 401", code)
	}
	if got := inv.calls.Load(); got != 1 {
		t.Errorf("Invalidate called %d times total, want 1 (rejected requests must not refresh)", got)
	}
}

// TestCacheInvalidateRPCUnmappedWithoutToken confirms the internal route
// is not registered when no invalidate token is configured (dev mode),
// so the surface is not advertised. spec: §25.5 line 2751.
func TestCacheInvalidateRPCUnmappedWithoutToken(t *testing.T) {
	srv := opsserver.New(opsserver.Options{
		EventSubscriptions: eventsubscription.NewService(eventsubscription.NewMemoryStore()),
		CacheInvalidator:   &countingInvalidator{},
		// no token
	})
	req := httptest.NewRequest(http.MethodPost, opsservice.DefaultCacheInvalidatePath, nil)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("unconfigured invalidate route status = %d, want 404", rr.Code)
	}
}
