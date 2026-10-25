// SPDX-License-Identifier: MIT

package issuedtokenstore

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"
)

// spec: 13.3 (Credential Flow) — the step (1a) re-read queries the
// listed source jtis once each and skips an absent actor's empty jti.
func TestSourceJTIsDeduplicatesAndSkipsEmpty_spec_13_3(t *testing.T) {
	t.Parallel()
	got := sourceJTIs([]SourceToken{
		{Role: SourceSubjectToken, JTI: "a"},
		{Role: SourceActorToken, JTI: ""},
		{Role: SourceActorToken, JTI: "a"},
		{Role: SourceActorToken, JTI: "b"},
	})
	if want := []string{"a", "b"}; !slices.Equal(got, want) {
		t.Errorf("sourceJTIs = %v, want %v", got, want)
	}
	if got := sourceJTIs(nil); len(got) != 0 {
		t.Errorf("sourceJTIs(nil) = %v, want empty", got)
	}
}

// spec: 13.3 (Credential Flow) — an empty source list, the gateway's
// in-process admin rotation, makes no query and passes.
func TestRecheckSourcesEmptyListMakesNoQuery_spec_13_3(t *testing.T) {
	t.Parallel()
	// A nil tx would panic on any query, so passing proves no query ran.
	if err := recheckSources(context.Background(), nil, nil); err != nil {
		t.Errorf("recheckSources(nil) = %v, want nil", err)
	}
	if err := recheckSources(context.Background(), nil, []SourceToken{{Role: SourceSubjectToken}}); err != nil {
		t.Errorf("recheckSources(empty jti) = %v, want nil", err)
	}
}

// spec: 13.3 (Credential Flow), 8.2 (Delegation Mechanism) — the typed
// error names the role and maps it to the invalid_grant reason.
func TestSourceRevokedErrorNamesRole_spec_13_3(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	for role, reason := range map[SourceRole]string{
		SourceSubjectToken: "subject_token_revoked",
		SourceActorToken:   "actor_token_revoked",
	} {
		e := &SourceRevokedError{Role: role, JTI: "jti-1", RevokedAt: at}
		if e.Reason() != reason {
			t.Errorf("Reason() = %q, want %q", e.Reason(), reason)
		}
		if msg := e.Error(); !strings.Contains(msg, string(role)) || !strings.Contains(msg, "jti-1") {
			t.Errorf("Error() = %q, want role and jti", msg)
		}
	}
}
