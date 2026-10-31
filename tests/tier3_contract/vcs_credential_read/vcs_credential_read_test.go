//go:build contract

// SPDX-License-Identifier: MIT

// Tier-3 contract test for the plan-binding response when the VCS
// credential read fails. It drives the production session handler
// (POST /v1/sessions, router → workspace-plan binding → JSON error
// encoder) with the production vcscred.StoreResolver over an in-memory
// credential-pool store, and a SecretReader that fails as the gateway's
// Token Service keyed read fails: a wrapped gRPC Unavailable or an open
// breaker when the Token Service cannot answer, and
// vcscred.ErrNoUsableCredential when the Secret is missing or refused.
//
// Each case asserts the status, the code, the body's category and
// retryable fields, and the details keys against the §15.1 catalog rows
// for GIT_CLONE_REF_RESOLVE_TRANSIENT and GIT_CLONE_REF_UNRESOLVABLE, and
// that errorclassify.Classify on the code string agrees with the body.
//
// spec: §15.1 (Error codes), §4.9 (Token Service secret resolution), §14.
package vcs_credential_read_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/lennylabs/lenny/tests/testinfra/ownerprincipal"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/lennylabs/lenny/pkg/gateway/core/subsystem"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credentialpoolstore"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/errorclassify"
	"github.com/lennylabs/lenny/pkg/gateway/provisioning/vcscred"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/workspaceplan"
)

const sha = "a1b2c3d4e5f60718293a4b5c6d7e8f9012345678"

// failingSecrets is a vcscred.SecretReader that fails every read with err
// and counts the reads.
type failingSecrets struct {
	err   error
	calls int
}

func (f *failingSecrets) Resolve(context.Context, string) (string, error) {
	f.calls++
	return "", f.err
}

// lsRemote is a workspaceplan.RefResolver that counts its calls.
type lsRemote struct{ calls int }

func (r *lsRemote) Resolve(context.Context, workspaceplan.GitClone, workspaceplan.VCSCredential) (string, error) {
	r.calls++
	return sha, nil
}

func poolStore(t *testing.T) credentialpoolstore.Store {
	t.Helper()
	store := credentialpoolstore.NewMemory()
	if err := store.Create(context.Background(), credentialpoolstore.CredentialPool{
		TenantID: "acme", Name: "github-pool", Provider: "github", HostPatterns: []string{"github.com"},
		Credentials: []credentialpoolstore.Credential{{ID: "c1", SecretRef: "lenny-credentials/gh-token"}},
	}); err != nil {
		t.Fatalf("seed pool: %v", err)
	}
	return store
}

type envelope struct {
	Error struct {
		Code      string         `json:"code"`
		Category  string         `json:"category"`
		Retryable bool           `json:"retryable"`
		Details   map[string]any `json:"details"`
	} `json:"error"`
}

func createSession(t *testing.T, h http.Handler) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(sessionserver.CreateSessionRequest{
		RuntimeRef: "echo",
		WorkspacePlan: json.RawMessage(`{"schemaVersion":1,"sources":[{"type":"gitClone",` +
			`"url":"https://github.com/acme/r.git","ref":"main",` +
			`"auth":{"mode":"credential-lease","leaseScope":"vcs.github.read"}}]}`),
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

// spec: §15.1 (Error codes), §4.9 (Token Service secret resolution) rule 3, §14
// diagnosis: a credential read the Token Service could not answer must reach
// the client as the retryable 503 GIT_CLONE_REF_RESOLVE_TRANSIENT with reason
// network_error, and a missing or refused credential as the non-retryable 422
// GIT_CLONE_REF_UNRESOLVABLE with reason auth_failed. A transient case
// answering 422 means plan binding relabels every credential failure as a
// configuration failure, and clients stop retrying a recoverable outage.
func TestPlanBindingVCSCredentialReadCodes(t *testing.T) {
	cases := []struct {
		name          string
		readErr       error
		wantStatus    int
		wantCode      string
		wantCategory  errorclassify.Category
		wantRetryable bool
		wantReason    string
	}{
		{
			name:       "token service unavailable",
			readErr:    fmt.Errorf("read VCS token Secret %q through the Token Service: %w", "gh-token", status.Error(codes.Unavailable, "unavailable")),
			wantStatus: http.StatusServiceUnavailable, wantCode: "GIT_CLONE_REF_RESOLVE_TRANSIENT",
			wantCategory: errorclassify.CategoryTransient, wantRetryable: true, wantReason: "network_error",
		},
		{
			name:       "breaker open",
			readErr:    fmt.Errorf("read VCS token Secret %q through the Token Service: %w", "gh-token", subsystem.ErrCircuitOpen),
			wantStatus: http.StatusServiceUnavailable, wantCode: "GIT_CLONE_REF_RESOLVE_TRANSIENT",
			wantCategory: errorclassify.CategoryTransient, wantRetryable: true, wantReason: "network_error",
		},
		{
			name:       "secret not found",
			readErr:    fmt.Errorf("%w: Secret %q or its token key not found", vcscred.ErrNoUsableCredential, "gh-token"),
			wantStatus: http.StatusUnprocessableEntity, wantCode: "GIT_CLONE_REF_UNRESOLVABLE",
			wantCategory: errorclassify.CategoryPermanent, wantRetryable: false, wantReason: "auth_failed",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pools := poolStore(t)
			secrets := &failingSecrets{err: tc.readErr}
			ls := &lsRemote{}
			srv := sessionserver.New(memstore.New(), sessionserver.Options{
				DevTenantHeader: true,
				IDFunc:          func() string { return "sess_vcs_contract" },
				RefResolver:     ls,
				CredentialPools: pools,
				VCSCredentials:  &vcscred.StoreResolver{Pools: pools, Secrets: secrets},
			})
			rr := createSession(t, ownerprincipal.Wrap(srv.Handler(), ownerprincipal.Alice))
			if rr.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d; body %s", rr.Code, tc.wantStatus, rr.Body.String())
			}
			var env envelope
			if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
				t.Fatalf("decode %s: %v", rr.Body.String(), err)
			}
			if env.Error.Code != tc.wantCode || env.Error.Category != string(tc.wantCategory) || env.Error.Retryable != tc.wantRetryable {
				t.Fatalf("code/category/retryable = %s/%s/%v, want %s/%s/%v",
					env.Error.Code, env.Error.Category, env.Error.Retryable, tc.wantCode, tc.wantCategory, tc.wantRetryable)
			}
			cat, retry := errorclassify.Classify(tc.wantCode)
			if cat != tc.wantCategory || retry != tc.wantRetryable {
				t.Fatalf("errorclassify.Classify(%s) = %s/%v, want %s/%v", tc.wantCode, cat, retry, tc.wantCategory, tc.wantRetryable)
			}
			keys := make([]string, 0, len(env.Error.Details))
			for k := range env.Error.Details {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			if fmt.Sprint(keys) != "[reason ref sourceIndex url]" {
				t.Fatalf("details keys = %v, want [reason ref sourceIndex url]", keys)
			}
			if env.Error.Details["reason"] != tc.wantReason || env.Error.Details["url"] != "https://github.com/acme/r.git" ||
				env.Error.Details["ref"] != "main" {
				t.Fatalf("details = %v, want reason %s for the source", env.Error.Details, tc.wantReason)
			}
			if secrets.calls != 1 || ls.calls != 0 {
				t.Fatalf("secret reads = %d, ls-remote calls = %d, want 1 and 0", secrets.calls, ls.calls)
			}
		})
	}
}
