// SPDX-License-Identifier: MIT

//go:build security

// Tier-9 security test for the §10.2 "Signing topology" session-creation
// caller check against a real cmd/lenny-gateway over Postgres: a
// platform-admin impersonation bearer cannot create a session, and the
// refusal reaches the client only after its token.exchanged rejection row
// with exchange_type session_mint is committed to the tenant's audit chain.
package tier9_security_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/gateway"
)

// postSessionAsBearer posts a session-creation request to the gateway
// carrying bearer and returns the status and the body.
func (s *opsRevStack) postSessionAsBearer(t *testing.T, bearer string) (int, []byte) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.gw.BaseURL()+"/v1/sessions",
		bytes.NewReader([]byte(`{"runtimeRef":"echo"}`)))
	if err != nil {
		t.Fatalf("new POST /v1/sessions request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+bearer)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /v1/sessions: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, body
}

// sessionMintRejections counts the committed token.exchanged rows of
// tenant whose exchange_type is session_mint and whose subject_token_jti is
// jti, with the policy_result reason.
func (s *opsRevStack) sessionMintRejections(t *testing.T, tenant, jti, policyResult string) int {
	t.Helper()
	var n int
	if err := s.pg.Pool.QueryRow(context.Background(),
		`SELECT count(*) FROM audit_log
		 WHERE tenant_id = $1 AND event_type = 'token.exchanged'
		   AND payload->>'exchange_type' = 'session_mint'
		   AND payload->>'subject_token_jti' = $2
		   AND payload->>'policy_result' = $3
		   AND payload->>'issued_token_jti' IS NULL`, tenant, jti, policyResult).Scan(&n); err != nil {
		t.Fatalf("count session_mint rejection rows: %v", err)
	}
	return n
}

// sessionsOwnedBy counts the session rows of tenant owned by user.
func (s *opsRevStack) sessionsOwnedBy(t *testing.T, tenant, user string) int {
	t.Helper()
	var n int
	if err := s.pg.Pool.QueryRow(context.Background(),
		`SELECT count(*) FROM sessions WHERE tenant_id = $1 AND user_id = $2`, tenant, user).Scan(&n); err != nil {
		t.Fatalf("count sessions of %s/%s: %v", tenant, user, err)
	}
	return n
}

// spec: 10.2 (Signing topology), 13.3 (Credential Flow: Write-before-issue ordering, Platform-admin impersonation), 16.7 (token.exchanged), 11.7
// diagnosis: a 2xx or a non-403 status means session creation accepted a
// platform-admin impersonation bearer, so a session it creates outlives
// the end of the impersonation. A 403 with no committed session_mint row
// means the refusal was answered before its rejection audit was durable.
// Any session row for the impersonated user means the check ran after a
// store write.
func TestSessionCreationRefusesImpersonationBearer_spec_10_2(t *testing.T) {
	gateway.SkipUnlessAvailable(t)
	s := startOpsRevStack(t)
	const subject = "grace@acme.com"
	imp, _ := s.tenantAdmin(t, "acme", subject)
	jti := tokenJTI(t, imp)

	status, body := s.postSessionAsBearer(t, imp)
	if status != http.StatusForbidden {
		t.Fatalf("POST /v1/sessions with an impersonation bearer = %d, want 403; body %s\n%s", status, body, s.gw.Logs())
	}
	if !bytes.Contains(body, []byte(`"FORBIDDEN"`)) {
		t.Errorf("refusal body %s, want code FORBIDDEN", body)
	}
	if n := s.sessionMintRejections(t, "acme", jti, "rejected:subject_token_impersonation"); n != 1 {
		t.Errorf("committed session_mint rejection rows for %s = %d, want 1", jti, n)
	}
	if n := s.sessionsOwnedBy(t, "acme", subject); n != 0 {
		t.Errorf("sessions owned by %s = %d, want 0", subject, n)
	}
}
