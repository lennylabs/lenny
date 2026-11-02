//go:build chaos

// SPDX-License-Identifier: MIT

// Tier-8 chaos coverage for the §7.1 uploadToken under a Postgres primary
// outage. The session server runs in process, because a gateway process answers
// 500 ahead of every session route during a primary cut at the environment
// middleware. Its store is pgstore over a pool that dials a tcpfault proxy,
// with a read pool that dials the database directly, which is the failure a
// lost primary with a healthy replica presents. Upload and finalize read the
// primary, so both refuse during the cut while GET serves from the replica, and
// both succeed on the same server after the cut ends.
package tier8_chaos_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/uploadtoken"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/ownerprincipal"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
	"github.com/lennylabs/lenny/tests/testinfra/tcpfault"
)

const (
	tokenOutageTenant  = "acme"
	tokenOutageSession = "00000000-0000-4000-8000-0000000071a1"
	// tokenOutagePrimaryRead is Options.PrimaryReadTimeout, so a refusal
	// during the cut has a bound the test states.
	tokenOutagePrimaryRead = 2 * time.Second
	// tokenOutageCap is the §11.1 per-session upload cap. tokenOutageProbe
	// exceeds it, so a request that clears the token check answers 429.
	tokenOutageCap = 64
	// tokenOutageRecovery bounds the pool's reconnect after Restore.
	tokenOutageRecovery = 30 * time.Second
)

var (
	tokenOutageProbe = strings.Repeat("p", 2*tokenOutageCap)
	tokenOutageInCap = "payload"
)

// tokenOutageEnv is a session server whose primary pool sits behind proxy.
type tokenOutageEnv struct {
	proxy   *tcpfault.Proxy
	store   *pgstore.Store
	handler http.Handler
	token   string
}

func newTokenOutageEnv(t *testing.T) *tokenOutageEnv {
	t.Helper()
	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: filepath.Join(schematest.RepoRoot(t), "migrations"),
	})
	if _, err := pg.Pool.Exec(context.Background(), `INSERT INTO tenants (id, genesis_nonce) VALUES ($1, $2)`, tokenOutageTenant, []byte{0x01}); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	proxy, dsn := tcpfault.StartURL(t, pg.DSN)
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse proxied dsn: %v", err)
	}
	cfg.ConnConfig.ConnectTimeout = time.Second
	primary, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("proxied pool: %v", err)
	}
	t.Cleanup(primary.Close)
	store := pgstore.New(primary, pgstore.WithReadPool(pg.Pool))

	token, issued, err := uploadtoken.NewIssuer(nil).IssueDetailed(tokenOutageSession, uploadtoken.DefaultTTL)
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}
	now := time.Now().UTC()
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: tokenOutageSession, TenantID: tokenOutageTenant, State: session.StateCreated,
		UserID: "alice@acme.com", RuntimeRef: "echo", CreatedAt: now, UpdatedAt: now,
		UploadTokenDigest: issued.Digest, UploadTokenExpiry: issued.Expiry,
	}); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	srv := sessionserver.New(store, sessionserver.Options{
		DevTenantHeader:          true,
		UploadTokenVerifier:      uploadtoken.NewVerifier(nil),
		Blobs:                    blobstore.NewMemoryStore(nil),
		MaxUploadBytesPerSession: tokenOutageCap,
		PrimaryReadTimeout:       tokenOutagePrimaryRead,
	})
	t.Cleanup(srv.Close)
	return &tokenOutageEnv{
		proxy: proxy, store: store, token: token,
		handler: ownerprincipal.Wrap(srv.Handler(), ownerprincipal.Alice),
	}
}

// call serves one request carrying the valid token and returns its recorder
// and how long it took.
func (e *tokenOutageEnv) call(method, suffix, contentType, body string) (*httptest.ResponseRecorder, time.Duration) {
	req := httptest.NewRequest(method, "/v1/sessions/"+tokenOutageSession+suffix, bytes.NewBufferString(body))
	req.Header.Set("X-Lenny-Tenant-ID", tokenOutageTenant)
	req.Header.Set("X-Lenny-Upload-Token", e.token)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	rr := httptest.NewRecorder()
	start := time.Now()
	e.handler.ServeHTTP(rr, req)
	return rr, time.Since(start)
}

func (e *tokenOutageEnv) upload(body string) (*httptest.ResponseRecorder, time.Duration) {
	return e.call(http.MethodPost, "/upload", "text/plain", body)
}

func (e *tokenOutageEnv) finalize() (*httptest.ResponseRecorder, time.Duration) {
	return e.call(http.MethodPost, "/finalize", "", "")
}

// wantPrimaryRefusal fails unless the call answered 500 within the primary
// read bound. The margin covers the handler's own work around the read.
func wantPrimaryRefusal(t *testing.T, what string, rr *httptest.ResponseRecorder, took time.Duration) {
	t.Helper()
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("%s during the cut: status %d, body %s, want 500", what, rr.Code, rr.Body.String())
	}
	if bound := tokenOutagePrimaryRead + 3*time.Second; took > bound {
		t.Errorf("%s during the cut took %s, want within %s", what, took, bound)
	}
}

// retryOn500 repeats call while it answers 500, up to the recovery bound.
func retryOn500(t *testing.T, call func() (*httptest.ResponseRecorder, time.Duration)) *httptest.ResponseRecorder {
	t.Helper()
	deadline := time.Now().Add(tokenOutageRecovery)
	for {
		rr, _ := call()
		if rr.Code != http.StatusInternalServerError || time.Now().After(deadline) {
			return rr
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// spec: 7.1 (Check order), 7.4 (Upload Safety), 12.3 (Postgres HA Requirements) "Behavior during Postgres failover"
// diagnosis: a 429 on the probe during the cut means upload admission read the
// lagging replica pool rather than the primary, because the only way past the
// token check to the §11.1 cap is a row read that succeeded. A 200 on the
// finalize during the cut means finalize reads the replica. A 500 slower than
// the bound means the primary read lost Options.PrimaryReadTimeout. A row that
// left `created` means a refused request wrote. A failure after Restore means
// the outage left the same server unable to recover, and a replay that is not
// 410 means consumption is not read from the primary row.
func TestUploadAndFinalizeRefuseDuringPrimaryOutage_spec_7_1(t *testing.T) {
	e := newTokenOutageEnv(t)
	if rr, _ := e.upload(tokenOutageProbe); rr.Code != http.StatusTooManyRequests || !strings.Contains(rr.Body.String(), "QUOTA_EXCEEDED") {
		t.Fatalf("probe before the cut: status %d, body %s, want 429 QUOTA_EXCEEDED", rr.Code, rr.Body.String())
	}

	e.proxy.Cut()
	if rr, _ := e.call(http.MethodGet, "", "", ""); rr.Code != http.StatusOK {
		t.Errorf("GET during the cut: status %d, body %s, want 200 from the read pool", rr.Code, rr.Body.String())
	}
	rr, took := e.upload(tokenOutageProbe)
	wantPrimaryRefusal(t, "probe upload", rr, took)
	rr, took = e.finalize()
	wantPrimaryRefusal(t, "finalize", rr, took)
	row, err := e.store.Get(context.Background(), tokenOutageTenant, tokenOutageSession)
	if err != nil {
		t.Fatalf("read the row through the read pool: %v", err)
	}
	if row.State != session.StateCreated {
		t.Errorf("state during the cut = %q, want created", row.State)
	}

	e.proxy.Restore(t)
	if rr := retryOn500(t, func() (*httptest.ResponseRecorder, time.Duration) { return e.upload(tokenOutageInCap) }); rr.Code != http.StatusCreated {
		t.Fatalf("in-cap upload after Restore: status %d, body %s", rr.Code, rr.Body.String())
	}
	if rr := retryOn500(t, e.finalize); rr.Code != http.StatusOK {
		t.Fatalf("finalize after Restore: status %d, body %s", rr.Code, rr.Body.String())
	}
	if rr, _ := e.finalize(); rr.Code != http.StatusGone || !strings.Contains(rr.Body.String(), "UPLOAD_TOKEN_CONSUMED") {
		t.Errorf("finalize replay: status %d, body %s, want 410 UPLOAD_TOKEN_CONSUMED", rr.Code, rr.Body.String())
	}
	if rr, _ := e.upload(tokenOutageInCap); rr.Code != http.StatusGone || !strings.Contains(rr.Body.String(), "UPLOAD_TOKEN_CONSUMED") {
		t.Errorf("upload replay: status %d, body %s, want 410 UPLOAD_TOKEN_CONSUMED", rr.Code, rr.Body.String())
	}
}
