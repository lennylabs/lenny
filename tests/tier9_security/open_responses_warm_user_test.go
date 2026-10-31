// SPDX-License-Identifier: MIT

//go:build security

package tier9_security_test

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/responsesstub"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/environment/transcriptstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/translator"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/tests/testinfra/ownerprincipal"
)

// orHoldingBinder creates a running session row per bind and reports hold,
// as the gateway's binder does for a multi_turn runtime with injection
// supported. It records each created session's owner, the request
// principal's subject.
type orHoldingBinder struct {
	store sessionstore.Store
	mu    sync.Mutex
	users []string
}

func (b *orHoldingBinder) BindSingleShot(ctx context.Context, spec translator.SingleShotSpec) (translator.SingleShotBinding, error) {
	b.mu.Lock()
	b.users = append(b.users, ownerprincipal.Subject(ctx))
	b.mu.Unlock()
	id := session.NewID()
	now := time.Now().UTC()
	err := b.store.Create(ctx, sessionstore.Session{
		ID: id, TenantID: spec.TenantID, UserID: ownerprincipal.Subject(ctx), RuntimeRef: spec.RuntimeRef,
		State: session.StateRunning, CreatedAt: now, UpdatedAt: now,
	})
	return translator.SingleShotBinding{SessionID: id, Hold: true}, err
}

// spec: §15 (Open Responses conversation sessions)
// diagnosis: a continuation of a held session's newest response sent with a
// user other than the one the creating request named ran on the held
// session, so the turn would be attributed to the creating user's session
// rather than to a session of the caller's own user. Warm condition 4 in
// warmEligible (pkg/gateway/environment/translator/open_responses.go) must
// compare the request's user with the row's UserID and send any other user
// cold onto a new session of that user.
func TestOpenResponsesWarmContinuationByAnotherUserGoesCold_spec_15(t *testing.T) {
	store := memstore.New()
	exec := &orRecordingExecutor{}
	binder := &orHoldingBinder{store: store}
	f := &orFixture{sessions: store, transcripts: transcriptstore.NewMemory(), exec: exec}
	f.handler = translator.NewOpenResponsesHandler(store, exec, translator.OpenResponsesOptions{
		HolderForwarder:  responsesstub.LocalForwarder{},
		Terminal:         responsesstub.ReleasingTerminal{Exec: exec, Store: store},
		DevTenantHeader:  true,
		SingleShotBinder: binder,
		Transcripts:      f.transcripts,
	}).Handler()

	post := func(user, prev string) string {
		body := map[string]any{"model": "echo", "input": "turn by " + user, "user": user}
		if prev != "" {
			body["previous_response_id"] = prev
		}
		code, raw := f.doAs(t, user, http.MethodPost, "/v1/responses", "acme", body)
		if code != http.StatusOK {
			t.Fatalf("POST by %s: %d, body=%s", user, code, raw)
		}
		var out struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(raw, &out)
		return out.ID
	}

	head := post("alice", "")
	held := orSessionOf(t, head)
	other := post("bob", head)
	if orSessionOf(t, other) == held {
		t.Fatalf("a continuation by bob ran on alice's held session %s", held)
	}
	binder.mu.Lock()
	users := append([]string(nil), binder.users...)
	binder.mu.Unlock()
	if len(users) != 2 || users[1] != "bob" {
		t.Errorf("binds by user = %v, want a second bind for bob", users)
	}
	row, err := store.Get(context.Background(), "acme", held)
	if err != nil {
		t.Fatal(err)
	}
	if len(row.Responses) != 1 {
		t.Errorf("alice's held session gained %d responses, want only its first", len(row.Responses)-1)
	}
	if same := post("alice", head); orSessionOf(t, same) != held {
		t.Errorf("alice's own continuation %q left the held session %s", same, held)
	}
}
