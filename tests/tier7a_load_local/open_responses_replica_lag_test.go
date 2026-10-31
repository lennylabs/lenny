// SPDX-License-Identifier: MIT

//go:build load_local

// Tier-7a concurrent check that Open Responses resolution survives a lagging
// §12.3 read replica. Concurrent conversations each continue straight away
// from the response the gateway has just returned, while the session store's
// Get lags the newest committed write.
//
// spec: §15 (Open Responses conversation sessions), §12.3 (Postgres HA
// Requirements)
package tier7a_load_local_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/environment/transcriptstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/translator"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/tests/testinfra/ownerprincipal"
	"github.com/lennylabs/lenny/tests/testinfra/responsesstub"
)

// lagExecutor answers every Send with one text part and counts the sends.
type lagExecutor struct{ sends atomic.Int64 }

func (e *lagExecutor) Send(context.Context, string, []executor.Message) (executor.Response, error) {
	n := e.sends.Add(1)
	return executor.Response{Parts: []executor.MessagePart{{Type: "text", Text: fmt.Sprintf("out-%d", n)}}}, nil
}

func (e *lagExecutor) Close(context.Context, string) error { return nil }

// lagRecorder answers every Send with "out-<last input>" and records the
// runtime input each session received.
type lagRecorder struct {
	mu     sync.Mutex
	inputs map[string][]executor.Message
}

func (e *lagRecorder) Send(_ context.Context, id string, msgs []executor.Message) (executor.Response, error) {
	e.mu.Lock()
	e.inputs[id] = append([]executor.Message(nil), msgs...)
	e.mu.Unlock()
	return executor.Response{Parts: []executor.MessagePart{{Type: "text", Text: "out-" + msgs[len(msgs)-1].Content}}}, nil
}

func (e *lagRecorder) Close(context.Context, string) error { return nil }

func (e *lagRecorder) input(id string) []executor.Message {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.inputs[id]
}

// lagBinder creates a running session row per bind through store and counts
// the binds. The session is held unless coldOnly is set.
type lagBinder struct {
	store    sessionstore.Store
	coldOnly bool
	binds    atomic.Int64
}

func (b *lagBinder) BindSingleShot(ctx context.Context, spec translator.SingleShotSpec) (translator.SingleShotBinding, error) {
	b.binds.Add(1)
	id := session.NewID()
	now := time.Now().UTC()
	if err := b.store.Create(ctx, sessionstore.Session{
		ID: id, TenantID: spec.TenantID, UserID: ownerprincipal.Subject(ctx), RuntimeRef: spec.RuntimeRef,
		State: session.StateRunning, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		return translator.SingleShotBinding{}, err
	}
	return translator.SingleShotBinding{SessionID: id, Hold: !b.coldOnly}, nil
}

// lagPost sends one turn as alice, the request's principal subject and its
// user field, in tenant acme and returns the status, the
// response id, and the body.
func lagPost(h http.Handler, prev, input string) (int, string, string) {
	body := map[string]any{"model": "echo", "input": input, "user": "alice"}
	if prev != "" {
		body["previous_response_id"] = prev
	}
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(raw))
	req = req.WithContext(ownerprincipal.Context(req.Context(), "alice"))
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	var out struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &out)
	return rr.Code, out.ID, rr.Body.String()
}

// TestOpenResponsesContinuationSurvivesReplicaLag_spec_15 pins that a
// continuation of a just-returned response never answers 404 while the read
// replica lags: each conversation's continuation of its cold S.1 and of its
// warm S.2 answers 200 on the same held session, and the warm turns bind no
// session, so the run binds exactly one session per conversation.
//
// spec: §15 (Open Responses conversation sessions); §12.3 (Postgres HA
// Requirements)
//
// diagnosis: a 404 means resolution of previous_response_id read the lagging
// read pool rather than the primary, so a completed response looked unknown;
// a wrong id or an extra bind means a stale row turned a warm turn cold.
func TestOpenResponsesContinuationSurvivesReplicaLag_spec_15(t *testing.T) {
	const conversations, turns = 32, 4
	store := responsesstub.NewLaggingReadStore(memstore.New())
	exec := &lagExecutor{}
	binder := &lagBinder{store: store}
	h := translator.NewOpenResponsesHandler(store, exec, translator.OpenResponsesOptions{
		SingleShotBinder: binder,
		Transcripts:      transcriptstore.NewMemory(),
		HolderForwarder:  responsesstub.LocalForwarder{},
		SessionAccess:    ownerprincipal.SessionAccess{},
		Terminal:         responsesstub.ReleasingTerminal{Exec: exec, Store: store},
		DevTenantHeader:  true,
	}).Handler()

	var (
		mu         sync.Mutex
		violations []string
		wg         sync.WaitGroup
	)
	report := func(format string, args ...any) {
		mu.Lock()
		violations = append(violations, fmt.Sprintf(format, args...))
		mu.Unlock()
	}
	for c := 0; c < conversations; c++ {
		wg.Add(1)
		go func(c int) {
			defer wg.Done()
			prev, sessionID := "", ""
			for k := 1; k <= turns; k++ {
				code, id, body := lagPost(h, prev, fmt.Sprintf("c%d-in-%d", c, k))
				if code != http.StatusOK {
					report("conversation %d turn %d after %q: status %d, body %s", c, k, prev, code, body)
					return
				}
				if k == 1 {
					sessionID = id[:len(id)-len(".1")]
				}
				if want := fmt.Sprintf("%s.%d", sessionID, k); id != want {
					report("conversation %d turn %d answered %q, want %q on the held session", c, k, id, want)
					return
				}
				prev = id
			}
		}(c)
	}
	wg.Wait()
	for _, v := range violations {
		t.Error(v)
	}
	if got := binder.binds.Load(); got != conversations {
		t.Errorf("binds = %d, want %d (one per conversation, none for a warm turn)", got, conversations)
	}
}

// TestOpenResponsesColdLineageSurvivesReplicaLag_spec_15 pins that a cold
// continuation's lineage walk reads every ancestor from the primary while
// the read replica has applied none of the run's writes: concurrent
// conversations each run three cold turns, each continuing the previous
// turn's response at once, and the third turn's runtime input is the whole
// lineage, the first session's turn included.
//
// spec: §15 (Open Responses conversation sessions); §12.3 (Postgres HA
// Requirements)
//
// diagnosis: a third-turn input that starts at the second turn means an
// ancestor hop read the lagging read pool, found the first session missing,
// and ended the walk as though the ancestor had been erased.
func TestOpenResponsesColdLineageSurvivesReplicaLag_spec_15(t *testing.T) {
	const conversations, turns = 32, 3
	// The replica holds back more writes than the run commits, so it never
	// shows a row the run wrote.
	store := responsesstub.NewLaggingReadStoreBehind(memstore.New(), 1<<20)
	exec := &lagRecorder{inputs: map[string][]executor.Message{}}
	h := translator.NewOpenResponsesHandler(store, exec, translator.OpenResponsesOptions{
		SingleShotBinder: &lagBinder{store: store, coldOnly: true},
		Transcripts:      transcriptstore.NewMemory(),
		HolderForwarder:  responsesstub.LocalForwarder{},
		SessionAccess:    ownerprincipal.SessionAccess{},
		Terminal:         responsesstub.ReleasingTerminal{Store: store},
		DevTenantHeader:  true,
	}).Handler()

	var (
		mu         sync.Mutex
		violations []string
		wg         sync.WaitGroup
	)
	report := func(format string, args ...any) {
		mu.Lock()
		violations = append(violations, fmt.Sprintf(format, args...))
		mu.Unlock()
	}
	for c := 0; c < conversations; c++ {
		wg.Add(1)
		go func(c int) {
			defer wg.Done()
			prev := ""
			var want []string
			for k := 1; k <= turns; k++ {
				in := fmt.Sprintf("c%d-in-%d", c, k)
				code, id, body := lagPost(h, prev, in)
				if code != http.StatusOK {
					report("conversation %d turn %d after %q: status %d, body %s", c, k, prev, code, body)
					return
				}
				want = append(want, in)
				var got []string
				for _, m := range exec.input(id[:len(id)-len(".1")]) {
					got = append(got, m.Content)
				}
				if fmt.Sprint(got) != fmt.Sprint(want) {
					report("conversation %d turn %d runtime input = %v, want %v", c, k, got, want)
					return
				}
				want = append(want, "out-"+in)
				prev = id
			}
		}(c)
	}
	wg.Wait()
	for _, v := range violations {
		t.Error(v)
	}
}
