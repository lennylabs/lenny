// SPDX-License-Identifier: MIT

package translator_test

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/ownerprincipal"
	"github.com/lennylabs/lenny/tests/testinfra/responsesstub"

	"github.com/lennylabs/lenny/pkg/gateway/environment/translator"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

// releasingEcho is the echo executor with a SessionReleaser whose Release
// returns err and counts its calls.
type releasingEcho struct {
	executor.Executor
	err      error
	releases atomic.Int32
}

func (r *releasingEcho) Release(context.Context, string, executor.Disposition) error {
	r.releases.Add(1)
	return r.err
}

// markCounter counts the Updates that set the release marker on a row that
// had none.
type markCounter struct {
	sessionstore.Store
	marks atomic.Int32
}

func (m *markCounter) Update(ctx context.Context, tenantID, id string, mutate func(*sessionstore.Session) error) (sessionstore.Session, error) {
	var before time.Time
	out, err := m.Store.Update(ctx, tenantID, id, func(r *sessionstore.Session) error {
		before = r.PodReleasedAt
		return mutate(r)
	})
	if err == nil && before.IsZero() && !out.PodReleasedAt.IsZero() {
		m.marks.Add(1)
	}
	return out, err
}

// TestSingleShotReleaseRecordsTheRelease_spec_7_1 pins the translator
// release site: a completed single-shot request on the chat completions
// handler and on the Open Responses handler records the pod release on the
// session row once, both when the executor release succeeds and when it
// returns an error. A built-in adapter session's completed write runs no
// post-terminal hook, so without this record its row would stay in its
// post-terminal window until the cap.
//
// spec: §7.1 (Post-terminal window), §15 (built-in adapter single-shot compute model)
func TestSingleShotReleaseRecordsTheRelease_spec_7_1(t *testing.T) {
	clock := func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }
	for _, releaseErr := range []error{nil, errors.New("adapter unreachable")} {
		for _, handler := range []string{"chat", "responses"} {
			name := handler + " release ok"
			if releaseErr != nil {
				name = handler + " release fails"
			}
			t.Run(name, func(t *testing.T) {
				store := &markCounter{Store: memstore.New()}
				exec := &releasingEcho{Executor: executor.NewEchoExecutor(), err: releaseErr}
				var h http.Handler
				var id string
				switch handler {
				case "chat":
					id = "sess_rm_chat"
					h = translator.NewOpenAIChatHandler(store, exec, translator.OpenAIChatOptions{
						DevTenantHeader: true, Clock: clock, IDFunc: func() string { return id },
					}).Handler()
					if rr := openaiPost(t, h, translator.OpenAIChatCompletionsRequest{
						Model: "echo", Messages: []translator.OpenAIChatMessage{{Role: "user", Content: "hello"}},
					}); rr.Code != http.StatusOK {
						t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
					}
				case "responses":
					id = "resp_rm"
					h = translator.NewOpenResponsesHandler(store, exec, translator.OpenResponsesOptions{
						HolderForwarder: responsesstub.LocalForwarder{},
						SessionAccess:   ownerprincipal.SessionAccess{},
						Terminal:        responsesstub.ReleasingTerminal{Exec: exec, Store: store},
						DevTenantHeader: true, Clock: clock, IDFunc: func() string { return id },
					}).Handler()
					if rr := respPost(t, h, map[string]any{"model": "echo", "input": "hello"}); rr.Code != http.StatusOK {
						t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
					}
				}
				if exec.releases.Load() != 1 {
					t.Errorf("executor releases = %d, want 1", exec.releases.Load())
				}
				row, err := store.Get(context.Background(), "acme", id)
				if err != nil {
					t.Fatalf("read row: %v", err)
				}
				if row.PodReleasedAt.IsZero() || store.marks.Load() != 1 {
					t.Errorf("PodReleasedAt = %v with %d marker writes, want recorded once", row.PodReleasedAt, store.marks.Load())
				}
			})
		}
	}
}
