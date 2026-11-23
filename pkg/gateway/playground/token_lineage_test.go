// SPDX-License-Identifier: MIT

package playground

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/lennylabs/lenny/pkg/storage/issuedtokenstore"
)

// spec: §10.2 (Authentication), §13.3 (Credential Flow)
//
// The apiKey mint records the playground bearer in issued_tokens with the
// pasted subject's jti as its parent and the subject as the source the
// record re-reads, so a recursive revocation of the subject reaches the
// bearer (the scenario CX-3 of change 0165's model). The row carries the
// bearer's jti, tenant, sub, the SHA-256 of the returned token, its scope,
// audience, and expiry.
func TestPlaygroundAPIKeyMintRecordsTheBearerUnderTheSubject(t *testing.T) {
	store := &fakeIssuedTokens{err: issuedtokenstore.ErrNotFound}
	status, env, _, _ := postAPIKeyMint(t, store)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200; error=%+v", status, env.Error)
	}
	if len(store.recorded) != 1 {
		t.Fatalf("recorded rows = %d, want 1", len(store.recorded))
	}
	row := store.recorded[0]
	if row.ParentJTI != "imp-jti-1" || row.ActSubject != "" {
		t.Errorf("row parent %q act_sub %q, want the pasted subject and no act_sub", row.ParentJTI, row.ActSubject)
	}
	if row.TenantID != "acme" || row.Subject != "bob" || row.JTI == "" || row.JTI == "imp-jti-1" {
		t.Errorf("row identity = (%q, %q, %q)", row.TenantID, row.Subject, row.JTI)
	}
	if !reflect.DeepEqual(row.Audiences, []string{"lenny-gateway"}) || len(row.Scope) == 0 || !row.ExpiresAt.After(row.IssuedAt) {
		t.Errorf("row audiences %v scope %v window %v..%v", row.Audiences, row.Scope, row.IssuedAt, row.ExpiresAt)
	}
	want := []issuedtokenstore.SourceToken{{Role: issuedtokenstore.SourceSubjectToken, JTI: "imp-jti-1"}}
	if len(store.recordSources) != 1 || !reflect.DeepEqual(store.recordSources[0], want) {
		t.Errorf("record sources = %+v, want %+v", store.recordSources, want)
	}
}

// spec: §10.2 (Authentication), §13.3 (Credential Flow)
//
// A record that the in-lock re-read refuses, because the subject was revoked
// after the pre-sign read, refuses the mint as the pre-sign read does and
// returns no bearer. A Postgres outage refuses it with 503
// SERVICE_UNAVAILABLE and any other record failure with 500 INTERNAL_ERROR.
func TestPlaygroundMintRefusesABearerItCannotRecord(t *testing.T) {
	cases := []struct {
		name       string
		recordErr  error
		wantStatus int
		wantCode   string
		wantReason string
	}{
		{
			name:       "subject revoked after the pre-sign read",
			recordErr:  &issuedtokenstore.SourceRevokedError{Role: issuedtokenstore.SourceSubjectToken, JTI: "imp-jti-1", RevokedAt: time.Now()},
			wantStatus: http.StatusUnauthorized,
			wantCode:   "LENNY_PLAYGROUND_BEARER_TYPE_REJECTED",
			wantReason: "subject_token_revoked",
		},
		{
			name:       "Postgres outage",
			recordErr:  &pgconn.PgError{Code: "57P01"},
			wantStatus: http.StatusServiceUnavailable,
			wantCode:   "SERVICE_UNAVAILABLE",
		},
		{
			name:       "other record failure",
			recordErr:  errors.New("constraint violated"),
			wantStatus: http.StatusInternalServerError,
			wantCode:   "INTERNAL_ERROR",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeIssuedTokens{err: issuedtokenstore.ErrNotFound, recordErr: tc.recordErr}
			status, env, _, rejections := postAPIKeyMint(t, store)
			if status != tc.wantStatus || env.Error.Code != tc.wantCode {
				t.Fatalf("status %d code %q, want %d %q", status, env.Error.Code, tc.wantStatus, tc.wantCode)
			}
			assertStep1aRejection(t, tc.wantReason, env, rejections)
		})
	}
}

// spec: §10.2 (Authentication), §13.3 (Credential Flow)
//
// The dev mode, whose subject is not a token, records the bearer with no
// parent and no source to re-read.
func TestPlaygroundDevMintRecordsTheBearerWithoutAParent(t *testing.T) {
	store := &fakeIssuedTokens{}
	h := New(Config{Enabled: true, AuthMode: AuthModeDev, DevTenantID: "acme", CapabilityTokenTTL: 900 * time.Second}, Options{
		Signer: &countingSigner{inner: devSigner()}, IssuedTokens: store,
	})
	srv := httptest.NewServer(h.TokenRoutes())
	defer srv.Close()
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, srv.URL+"/v1/playground/token", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST token: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var body tokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(store.recorded) != 1 || store.recorded[0].ParentJTI != "" || len(store.recordSources[0]) != 0 {
		t.Fatalf("recorded %+v sources %+v, want one row without parent or sources", store.recorded, store.recordSources)
	}
	hash := sha256.Sum256([]byte(body.BearerToken))
	if !reflect.DeepEqual(store.recorded[0].TokenHash, hash[:]) {
		t.Error("row token hash is not the SHA-256 of the returned bearer")
	}
}
