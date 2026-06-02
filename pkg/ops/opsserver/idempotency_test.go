// SPDX-License-Identifier: MIT

package opsserver_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/ops/backup"
	"github.com/lennylabs/lenny/pkg/ops/opsidem"
	"github.com/lennylabs/lenny/pkg/ops/opsserver"
)

// idemBackupServer builds an opsserver with a §25.11 BackupService and
// the §25.4 idempotency middleware wired, in the given Tier posture.
func idemBackupServer(t *testing.T, production bool) *opsserver.Server {
	t.Helper()
	svc, err := backup.NewService(backup.Config{
		Store:           backup.NewMemStore(),
		Launcher:        backup.NewFakeLauncher(),
		Locker:          backup.NewMemLocker(),
		PlatformVersion: "1.5.0",
		SchemaVersion:   42,
		Now:             func() time.Time { return time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return opsserver.New(opsserver.Options{
		Backups:     svc,
		Production:  production,
		Idempotency: opsidem.NewMemoryStore(),
	})
}

func postIdem(srv *opsserver.Server, path, key, caller, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set(opsidem.HeaderName, key)
	}
	if caller != "" {
		req.Header.Set("X-Lenny-Caller", caller)
	}
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	return rec
}

// spec: §25.4 lines 2031-2035 — through the full Server, a full backup
// (a required-key endpoint) without an Idempotency-Key is rejected at
// Tier 2/3 before the handler runs.
func TestServerRequiredKeyMissing_spec_25_4(t *testing.T) {
	srv := idemBackupServer(t, true)
	rec := postIdem(srv, "/v1/admin/backups", "", "alice", `{"type":"full","confirm":true}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "IDEMPOTENCY_KEY_REQUIRED") {
		t.Errorf("body missing IDEMPOTENCY_KEY_REQUIRED: %s", rec.Body.String())
	}
}

// A repeated full-backup request with the same key replays the cached
// response and does not create a second backup.
func TestServerReplayThroughFullChain_spec_25_4(t *testing.T) {
	srv := idemBackupServer(t, true)
	first := postIdem(srv, "/v1/admin/backups", "key-1", "alice", `{"type":"full","confirm":true}`)
	if first.Code != http.StatusAccepted {
		t.Fatalf("first status = %d, want 202; body=%s", first.Code, first.Body.String())
	}
	second := postIdem(srv, "/v1/admin/backups", "key-1", "alice", `{"type":"full","confirm":true}`)
	if second.Code != http.StatusAccepted {
		t.Fatalf("replay status = %d, want 202", second.Code)
	}
	if second.Header().Get("X-Lenny-Idempotent-Replay") != "true" {
		t.Errorf("replay missing X-Lenny-Idempotent-Replay header")
	}
	if first.Body.String() != second.Body.String() {
		t.Errorf("replay body differs from original:\n first=%s\nsecond=%s", first.Body.String(), second.Body.String())
	}
}

// A diagnostic-style optional POST without a key passes through (only the
// enumerated required endpoints reject a missing key). A non-full backup
// is optional, so it serves without a key.
func TestServerOptionalEndpointNoKeyPasses_spec_25_4(t *testing.T) {
	srv := idemBackupServer(t, true)
	rec := postIdem(srv, "/v1/admin/backups", "", "alice", `{"type":"postgres"}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("optional postgres backup without key: status = %d, want 202; body=%s", rec.Code, rec.Body.String())
	}
}
