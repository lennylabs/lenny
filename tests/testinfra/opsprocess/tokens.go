// SPDX-License-Identifier: MIT

package opsprocess

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/stackkey"
)

// Responses lenny-ops writes for a Lenny-issued token it refuses. They are
// the responses a gateway replica's authmw writes for the same cases.
//
// spec: §25.4 (The lenny-ops Service: Authentication, revocation set
// rules 3, 4, and 7).
const (
	// CodeRevoked is the error code of the 401 for a revoked jti (rule 3).
	CodeRevoked = "TOKEN_REVOKED"
	// CodeStale is the error code of the 503 for a token presented before
	// the first successful revocation set read (rule 4).
	CodeStale = "token_validation_unavailable"
	// CodeUnauthorized is the error code of the 401 for a revoked
	// playground bearer, which lenny-ops writes for every playground
	// bearer (rule 7) with details.reason bearer_revoked.
	CodeUnauthorized = "UNAUTHORIZED"
	// CodeInvalid is the error code of the 401 for a token the key set
	// does not verify.
	CodeInvalid = "TOKEN_INVALID"
	// CodeForbidden is the error code of the 403 the §25.4 role gate
	// writes for an authenticated principal that holds neither
	// platform-admin nor tenant-admin.
	CodeForbidden = "FORBIDDEN"
)

// pollInterval is how often the token helpers re-send a request.
const pollInterval = 250 * time.Millisecond

// readFailedRecord is the text of the record lenny-ops logs for each
// failed revocation set read (cmd/lenny-ops logRevocationRead).
const readFailedRecord = "revocation set read failed"

// AuthArgs returns the flags that make a dev-mode lenny-ops authenticate
// its requests: the §17.4 dev bearer-trust hook over a fresh key, which
// lenny-ops admits only under LENNY_DEV_MODE (StartOpts sets it). A
// dev-mode lenny-ops started without them, and without --production,
// serves its surface unauthenticated, so a test that asserts a token
// outcome on it passes these.
func AuthArgs(t testing.TB) []string {
	t.Helper()
	return []string{"--bearer-trust-hmac-key-file=" + stackkey.WriteBearerTrustKey(t)}
}

// Outcome is one lenny-ops answer to GET /v1/admin/me.
type Outcome struct {
	// Status is the HTTP status.
	Status int
	// Code is error.code of an error envelope, empty otherwise.
	Code string
	// Reason is error.details.reason of an error envelope, empty
	// otherwise.
	Reason string
	// Subject is identity.sub of a 200 body, empty otherwise.
	Subject string
	// Body is the raw response body.
	Body string
}

// Me sends GET /v1/admin/me with bearer and no dev headers, and returns
// lenny-ops's answer.
func (p *Process) Me(t testing.TB, bearer string) Outcome {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+"/v1/admin/me", nil)
	if err != nil {
		t.Fatalf("opsprocess: new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+bearer)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("opsprocess: GET /v1/admin/me: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return decodeOutcome(resp.StatusCode, raw)
}

// decodeOutcome reads the fields Outcome carries from a response body.
func decodeOutcome(status int, raw []byte) Outcome {
	var body struct {
		Identity struct {
			Sub string `json:"sub"`
		} `json:"identity"`
		Error struct {
			Code    string         `json:"code"`
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	_ = json.Unmarshal(raw, &body)
	reason, _ := body.Error.Details["reason"].(string)
	return Outcome{
		Status:  status,
		Code:    body.Error.Code,
		Reason:  reason,
		Subject: body.Identity.Sub,
		Body:    string(raw),
	}
}

// WaitAccepted polls GET /v1/admin/me until lenny-ops answers 200 for
// bearer, and fails the test when it has not within d. lenny-ops refuses
// every Lenny-issued token until its first revocation set read succeeds,
// so a test calls it before asserting that a token is accepted or
// refused as revoked. It returns the accepted outcome.
//
// spec: §25.4 (The lenny-ops Service: Authentication, revocation set
// rules 1 and 4).
func (p *Process) WaitAccepted(t testing.TB, bearer string, d time.Duration) Outcome {
	t.Helper()
	return p.waitFor(t, "200", d, bearer, func(o Outcome) bool { return o.Status == http.StatusOK })
}

// Authenticated reports whether lenny-ops's authentication chain admitted
// the bearer: the key set verified it and the revocation set let it
// through, so lenny-ops answered 200 or the §25.4 role gate's 403. The
// role gate runs after the bearer middleware, so its 403 is reached only
// by a token rules 3, 4, 5, and 7 do not refuse. A POST /v1/oauth/token
// token carries no roles claim, so lenny-ops admits it through
// authentication and its role gate then refuses it.
//
// spec: §25.4 (The lenny-ops Service: Authentication, revocation set).
func (o Outcome) Authenticated() bool {
	return o.Status == http.StatusOK || (o.Status == http.StatusForbidden && o.Code == CodeForbidden)
}

// WaitAuthenticated polls GET /v1/admin/me until lenny-ops's
// authentication chain admits bearer (Outcome.Authenticated), and fails
// the test when it has not within d. A test calls it before asserting
// that a token without an admin role is refused as revoked, because
// lenny-ops refuses every Lenny-issued token until its first revocation
// set read succeeds.
//
// spec: §25.4 (The lenny-ops Service: Authentication, revocation set
// rules 1 and 4).
func (p *Process) WaitAuthenticated(t testing.TB, bearer string, d time.Duration) Outcome {
	t.Helper()
	return p.waitFor(t, "200 or the role gate's 403", d, bearer, Outcome.Authenticated)
}

// WaitRefused polls GET /v1/admin/me until lenny-ops refuses bearer with
// status and code, and fails the test when it has not within d.
func (p *Process) WaitRefused(t testing.TB, bearer string, status int, code string, d time.Duration) Outcome {
	t.Helper()
	return p.waitFor(t, code, d, bearer, func(o Outcome) bool { return o.Status == status && o.Code == code })
}

// waitFor polls until done reports true for an outcome or d passes.
func (p *Process) waitFor(t testing.TB, want string, d time.Duration, bearer string, done func(Outcome) bool) Outcome {
	t.Helper()
	deadline := time.Now().Add(d)
	for {
		o := p.Me(t, bearer)
		if done(o) {
			return o
		}
		if time.Now().After(deadline) {
			t.Fatalf("opsprocess: lenny-ops did not answer %s within %s; last answer %d %s\nlogs:\n%s", want, d, o.Status, o.Body, p.Logs())
		}
		time.Sleep(pollInterval)
	}
}

// RequireRefusedThroughout asserts that lenny-ops refuses bearer with
// status and code on every request it sends during window, and fails the
// test on the first answer that differs. It checks a refusal that must
// hold for a whole interval, such as the refusal of every Lenny-issued
// token before the first successful revocation set read, rather than
// retrying until the refusal appears.
//
// spec: §25.4 (The lenny-ops Service: Authentication, revocation set
// rule 4).
func (p *Process) RequireRefusedThroughout(t testing.TB, bearer string, status int, code string, window time.Duration) {
	t.Helper()
	end := time.Now().Add(window)
	for time.Now().Before(end) {
		if o := p.Me(t, bearer); o.Status != status || o.Code != code {
			t.Fatalf("opsprocess: lenny-ops answered %d %s during a window in which it must refuse with %d %s", o.Status, o.Body, status, code)
		}
		time.Sleep(pollInterval)
	}
}

// Healthz returns the status of GET /healthz, the permissive check the
// production readiness probe uses.
func (p *Process) Healthz(t testing.TB) int {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), readyRequestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+"/healthz", nil)
	if err != nil {
		t.Fatalf("opsprocess: new request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("opsprocess: GET /healthz: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	return resp.StatusCode
}

// FailedReads returns how many failed revocation set reads lenny-ops has
// logged so far.
func (p *Process) FailedReads() int {
	return strings.Count(p.Logs(), readFailedRecord)
}

// RequireThroughFailedReads runs check on every poll until lenny-ops has
// logged n failed revocation set reads more than it had at the call, and
// then once more. It fails the test on the first error check returns, and
// when the reads have not failed within d. A test uses it to hold an
// assertion across a Postgres outage until the outage has reached the
// revocation set, so the assertion covers the state in which a read has
// failed after one succeeded rather than the moment the outage began.
//
// spec: §25.4 (The lenny-ops Service: Authentication, revocation set
// rules 1 and 5).
func (p *Process) RequireThroughFailedReads(t testing.TB, n int, d time.Duration, check func() error) {
	t.Helper()
	want := p.FailedReads() + n
	deadline := time.Now().Add(d)
	for {
		reached := p.FailedReads() >= want
		if err := check(); err != nil {
			t.Fatalf("opsprocess: %v (failed revocation set reads so far: %d)\nlogs:\n%s", err, p.FailedReads(), p.Logs())
		}
		if reached {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("opsprocess: lenny-ops logged no further %d failed revocation set read(s) within %s\nlogs:\n%s", n, d, p.Logs())
		}
		time.Sleep(pollInterval)
	}
}
