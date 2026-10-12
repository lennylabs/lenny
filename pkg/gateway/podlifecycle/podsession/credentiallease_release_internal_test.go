// SPDX-License-Identifier: MIT

package podsession

import (
	"bytes"
	"errors"
	"log"
	"strings"
	"testing"

	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// failingReleaser is a CredentialAssigner whose release methods fail
// with a fixed error and record every call.
type failingReleaser struct {
	err             error
	sessionReleases []string
	leaseReleases   []string
}

func (f *failingReleaser) AssignProto(string, string, string, string) (*adapterv1.CredentialLease, error) {
	return nil, errors.New("not used")
}

func (f *failingReleaser) ReleaseSession(sessionID string) error {
	f.sessionReleases = append(f.sessionReleases, sessionID)
	return f.err
}

func (f *failingReleaser) Release(leaseID string) error {
	f.leaseReleases = append(f.leaseReleases, leaseID)
	return f.err
}

// captureLog redirects the standard logger for the test and returns the
// buffer it writes to.
func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prevOut, prevFlags := log.Writer(), log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(prevOut)
		log.SetFlags(prevFlags)
	})
	return &buf
}

// TestReleaseCredentialsSurfacesFailure_spec_7_1 asserts that a failed
// §7.1 step-23 session release is logged with the session and the cause
// rather than dropped, and that a clean release logs nothing. Teardown
// still completes in both cases.
//
// spec: §7.1 (Normal Flow, step 23), §4.9 (Credential Leasing Service).
func TestReleaseCredentialsSurfacesFailure_spec_7_1(t *testing.T) {
	cases := []struct {
		name    string
		err     error
		wantLog bool
	}{
		{name: "release fails", err: errors.New("revoke lease cl-1: rpc error: code = InvalidArgument desc = tenant_id is required"), wantLog: true},
		{name: "release succeeds", err: nil, wantLog: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			buf := captureLog(t)
			fr := &failingReleaser{err: tc.err}
			b := &Binder{Credentials: fr}
			b.releaseCredentials("sess-1")
			if len(fr.sessionReleases) != 1 || fr.sessionReleases[0] != "sess-1" {
				t.Fatalf("ReleaseSession calls = %v, want [sess-1]", fr.sessionReleases)
			}
			out := buf.String()
			if tc.wantLog {
				if !strings.Contains(out, "sess-1") || !strings.Contains(out, "tenant_id is required") {
					t.Fatalf("log %q does not name the session and the cause", out)
				}
			} else if out != "" {
				t.Fatalf("clean release logged %q, want nothing", out)
			}
		})
	}
}

// TestReleaseAttemptCredentialsSurfacesEachFailure_spec_7_1 asserts
// that a failed bind attempt releases every lease it minted, skips empty
// identifiers, and logs each failed release by lease identifier.
//
// spec: §7.1 (Normal Flow), §4.9 (Credential Leasing Service).
func TestReleaseAttemptCredentialsSurfacesEachFailure_spec_7_1(t *testing.T) {
	buf := captureLog(t)
	fr := &failingReleaser{err: errors.New("token service unavailable")}
	b := &Binder{Credentials: fr}
	b.releaseAttemptCredentials([]string{"cl-a", "", "cl-b"})
	if got := strings.Join(fr.leaseReleases, ","); got != "cl-a,cl-b" {
		t.Fatalf("Release calls = %q, want cl-a,cl-b", got)
	}
	out := buf.String()
	for _, id := range []string{"cl-a", "cl-b"} {
		if !strings.Contains(out, id) {
			t.Errorf("log %q does not name failed lease %s", out, id)
		}
	}
}

// TestReleaseCredentialsNoAssignerIsNoOp_spec_7_1 asserts that a
// binder with no credential service tears down without a release.
//
// spec: §7.1 (Normal Flow, step 23).
func TestReleaseCredentialsNoAssignerIsNoOp_spec_7_1(t *testing.T) {
	buf := captureLog(t)
	b := &Binder{}
	b.releaseCredentials("sess-1")
	b.releaseAttemptCredentials([]string{"cl-a"})
	if buf.Len() != 0 {
		t.Fatalf("no-assigner teardown logged %q, want nothing", buf.String())
	}
}
