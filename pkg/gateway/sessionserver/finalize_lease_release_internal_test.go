// SPDX-License-Identifier: MIT

package sessionserver

import (
	"bytes"
	"errors"
	"log"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// erroringReleaseAssigner fails every session release with err.
type erroringReleaseAssigner struct {
	err      error
	released []string
}

func (a *erroringReleaseAssigner) AssignProto(_, _, _, _ string) (*adapterv1.CredentialLease, error) {
	return nil, nil
}

func (a *erroringReleaseAssigner) ReleaseSession(sessionID string) error {
	a.released = append(a.released, sessionID)
	return a.err
}

func (*erroringReleaseAssigner) Release(string) error { return nil }

// TestRevokeFinalizeLeaseSurfacesFailure_spec_7_1 asserts that the
// finalize-failure lease release logs a failed release with the session
// and the cause instead of dropping it, logs nothing on success, and is
// a no-op without a binder or a credential service.
//
// spec: §7.1 (Normal Flow, step 23), §4.9 (Credential Leasing Service).
func TestRevokeFinalizeLeaseSurfacesFailure_spec_7_1(t *testing.T) {
	var buf bytes.Buffer
	prevOut, prevFlags := log.Writer(), log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(prevOut)
		log.SetFlags(prevFlags)
	})

	failing := &erroringReleaseAssigner{err: errors.New("credassign: Token Service unavailable")}
	s := &Server{podBinder: &podsession.Binder{Credentials: failing}}
	s.revokeFinalizeLease("sess-9")
	if len(failing.released) != 1 || failing.released[0] != "sess-9" {
		t.Fatalf("ReleaseSession calls = %v, want [sess-9]", failing.released)
	}
	if out := buf.String(); !strings.Contains(out, "sess-9") || !strings.Contains(out, "Token Service unavailable") {
		t.Fatalf("log %q does not name the session and the cause", out)
	}

	buf.Reset()
	clean := &erroringReleaseAssigner{}
	(&Server{podBinder: &podsession.Binder{Credentials: clean}}).revokeFinalizeLease("sess-10")
	if buf.Len() != 0 {
		t.Fatalf("clean release logged %q, want nothing", buf.String())
	}

	(&Server{}).revokeFinalizeLease("sess-11")
	(&Server{podBinder: &podsession.Binder{}}).revokeFinalizeLease("sess-12")
	if buf.Len() != 0 {
		t.Fatalf("no-binder release logged %q, want nothing", buf.String())
	}
}
