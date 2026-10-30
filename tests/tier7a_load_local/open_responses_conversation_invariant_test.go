// SPDX-License-Identifier: MIT

//go:build load_local

// Tier-7a randomized invariant checker for Open Responses conversation
// sessions. Concurrent drivers issue warm continuations, forks, concurrent
// continuations of one head, REST deliveries, session terminates, and
// session erasures against the production translator handler, with executor
// refusals before delivery and failures after delivery injected per turn.
// A model runtime records what each session's runtime received. After each
// run an audit phase reads every completed response's conversation context
// through a cold continuation and checks it, and the checker asserts the
// response-identity, release, and terminal invariants.
//
// spec: §15 (Open Responses conversation sessions), §15.1 (session
// transcript), §12.8 (Compliance Interfaces), §7.2 (Interactive Session
// Model)
package tier7a_load_local_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strconv"
	"strings"
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
	"github.com/lennylabs/lenny/tests/testinfra/responsesstub"
)

const (
	orInvTenant     = "acme"
	orInvUser       = "alice"
	orInvRuntimeRef = "echo"
)

// orInvFault is the fault the model runtime injects into one turn.
type orInvFault int

const (
	orInvNoFault orInvFault = iota
	// orInvRefuse refuses a send to a session that already holds history
	// before delivering any of it, as a sealed adapter does.
	orInvRefuse
	// orInvFail delivers the send and then fails it.
	orInvFail
)

// orInvRuntime is the model runtime behind every session. It keeps each
// session's received history, records the history at the end of each turn
// keyed by the turn's token, and counts releases.
type orInvRuntime struct {
	mu        sync.Mutex
	bound     map[string]bool
	history   map[string][]executor.Message
	snapshots map[int][]executor.Message
	firstSend map[string][]executor.Message
	receiver  map[int]string
	releases  map[string]int
	faults    map[int]orInvFault
}

func newORInvRuntime() *orInvRuntime {
	return &orInvRuntime{
		bound: map[string]bool{}, history: map[string][]executor.Message{},
		snapshots: map[int][]executor.Message{}, firstSend: map[string][]executor.Message{},
		receiver: map[int]string{}, releases: map[string]int{}, faults: map[int]orInvFault{},
	}
}

// orInvToken returns the token of a turn input "in-<k>", or -1.
func orInvToken(content string) int {
	k, err := strconv.Atoi(strings.TrimPrefix(content, "in-"))
	if err != nil || !strings.HasPrefix(content, "in-") {
		return -1
	}
	return k
}

func (m *orInvRuntime) bind(id string) {
	m.mu.Lock()
	m.bound[id] = true
	m.mu.Unlock()
}

func (m *orInvRuntime) setFault(k int, f orInvFault) {
	m.mu.Lock()
	m.faults[k] = f
	m.mu.Unlock()
}

// Send delivers msgs to the session's runtime and answers "out-<k>" for the
// turn token k carried by the last message.
func (m *orInvRuntime) Send(_ context.Context, id string, msgs []executor.Message) (executor.Response, error) {
	runtime.Gosched()
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.bound[id] {
		return executor.Response{}, executor.ErrNotBound
	}
	last := msgs[len(msgs)-1].Content
	k := orInvToken(last)
	if m.faults[k] == orInvRefuse && len(m.history[id]) > 0 {
		m.faults[k] = orInvNoFault
		return executor.Response{}, &executor.SealedError{Delivered: 0}
	}
	if _, seen := m.firstSend[id]; !seen {
		m.firstSend[id] = append([]executor.Message(nil), msgs...)
	}
	m.history[id] = append(m.history[id], msgs...)
	if k >= 0 {
		m.receiver[k] = id
	}
	if k >= 0 && m.faults[k] == orInvFail {
		return executor.Response{}, errors.New("model runtime: injected failure after delivery")
	}
	answer := "ack-" + last
	if k >= 0 {
		answer = "out-" + strconv.Itoa(k)
	}
	m.history[id] = append(m.history[id], executor.Message{Role: "assistant", Content: answer})
	if k >= 0 {
		m.snapshots[k] = append([]executor.Message(nil), m.history[id]...)
	}
	return executor.Response{Parts: []executor.MessagePart{{Type: "text", Text: answer}}}, nil
}

// deliverREST delivers a REST message to a bound session's runtime and
// reports whether it did.
func (m *orInvRuntime) deliverREST(id, content string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.bound[id] {
		return false
	}
	m.history[id] = append(m.history[id], executor.Message{Role: "user", Content: content})
	return true
}

func (m *orInvRuntime) Close(ctx context.Context, id string) error { return m.Release(ctx, id, "") }

// Release ends the session's binding and counts the call.
func (m *orInvRuntime) Release(_ context.Context, id string, _ executor.Disposition) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.releases[id]++
	m.bound[id] = false
	return nil
}

// orInvStore counts each committed transition of a session into a terminal
// state, the write a session_complete follows.
type orInvStore struct {
	sessionstore.Store
	mu        sync.Mutex
	terminals map[string]int
}

func (s *orInvStore) Update(ctx context.Context, tenantID, id string, mut func(*sessionstore.Session) error) (sessionstore.Session, error) {
	entered := false
	out, err := s.Store.Update(ctx, tenantID, id, func(r *sessionstore.Session) error {
		before := r.State
		if err := mut(r); err != nil {
			entered = false
			return err
		}
		entered = !session.IsTerminal(before) && session.IsTerminal(r.State)
		return nil
	})
	if err == nil && entered {
		s.mu.Lock()
		s.terminals[id]++
		s.mu.Unlock()
	}
	return out, err
}

// orInvBinder creates a running session row per bind and holds it.
type orInvBinder struct {
	store sessionstore.Store
	rt    *orInvRuntime
}

func (b orInvBinder) BindSingleShot(ctx context.Context, spec translator.SingleShotSpec) (translator.SingleShotBinding, error) {
	id := session.NewID()
	now := time.Now().UTC()
	if err := b.store.Create(ctx, sessionstore.Session{
		ID: id, TenantID: spec.TenantID, UserID: spec.UserID, RuntimeRef: spec.RuntimeRef,
		State: session.StateRunning, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		return translator.SingleShotBinding{}, err
	}
	b.rt.bind(id)
	return translator.SingleShotBinding{SessionID: id, Hold: true}, nil
}

// orInvWorld is one run's handler, stores, model runtime, and ledger.
type orInvWorld struct {
	t           *testing.T
	handler     http.Handler
	store       *orInvStore
	transcripts *transcriptstore.Memory
	rt          *orInvRuntime
	terminal    responsesstub.ReleasingTerminal
	nextToken   atomic.Int64

	mu sync.Mutex
	// completed lists the 200 response ids in arrival order.
	completed []string
	// tokenOf and sessionOfToken map a 200 response id to its turn token and
	// the turn token to the session that answered it.
	tokenOf        map[string]int
	sessionOfToken map[int]string
	// parentOf is the previous_response_id of the request that created
	// each session.
	parentOf map[string]string
	// failed lists the tokens of turns that answered 500.
	failed []int
	// erased is every session an erasure started on.
	erased map[string]bool
	// violations collects driver-phase invariant violations.
	violations []string
}

func newORInvWorld(t *testing.T) *orInvWorld {
	rt := newORInvRuntime()
	store := &orInvStore{Store: memstore.New(), terminals: map[string]int{}}
	w := &orInvWorld{
		t: t, store: store, transcripts: transcriptstore.NewMemory(), rt: rt,
		terminal: responsesstub.ReleasingTerminal{Exec: rt},
		tokenOf:  map[string]int{}, sessionOfToken: map[int]string{}, parentOf: map[string]string{},
		erased: map[string]bool{},
	}
	w.handler = translator.NewOpenResponsesHandler(store, rt, translator.OpenResponsesOptions{
		SingleShotBinder: orInvBinder{store: store, rt: rt},
		DefaultRuntime:   orInvRuntimeRef,
		Transcripts:      w.transcripts,
		HolderForwarder:  responsesstub.LocalForwarder{},
		Terminal:         w.terminal,
		DevTenantHeader:  true,
	}).Handler()
	return w
}

func (w *orInvWorld) violate(format string, args ...any) {
	w.mu.Lock()
	w.violations = append(w.violations, fmt.Sprintf(format, args...))
	w.mu.Unlock()
}

// post sends one turn and returns the status and the response id.
func (w *orInvWorld) post(user, prev, input string) (int, string) {
	body := map[string]any{"model": orInvRuntimeRef, "input": input, "user": user}
	if prev != "" {
		body["previous_response_id"] = prev
	}
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(raw))
	req.Header.Set("X-Lenny-Tenant-ID", orInvTenant)
	rr := httptest.NewRecorder()
	w.handler.ServeHTTP(rr, req)
	var out struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &out)
	return rr.Code, out.ID
}

func orInvSplit(id string) (string, int) {
	dot := strings.LastIndex(id, ".")
	if dot <= 0 {
		return "", 0
	}
	n, _ := strconv.Atoi(id[dot+1:])
	return id[:dot], n
}

// turn runs one driver turn continuing prev with an optional fault and
// records its outcome in the ledger.
func (w *orInvWorld) turn(prev string, fault orInvFault) {
	k := int(w.nextToken.Add(1))
	if fault != orInvNoFault {
		w.rt.setFault(k, fault)
	}
	code, id := w.post(orInvUser, prev, "in-"+strconv.Itoa(k))
	w.mu.Lock()
	defer w.mu.Unlock()
	switch code {
	case http.StatusOK:
		if _, dup := w.tokenOf[id]; dup {
			w.violations = append(w.violations, fmt.Sprintf("response id %s answered twice", id))
			return
		}
		sid, n := orInvSplit(id)
		w.completed = append(w.completed, id)
		w.tokenOf[id] = k
		w.sessionOfToken[k] = sid
		if n == 1 {
			w.parentOf[sid] = prev
		}
	case http.StatusInternalServerError:
		w.failed = append(w.failed, k)
	case http.StatusNotFound:
		if prevSession, _ := orInvSplit(prev); !w.erased[prevSession] {
			w.violations = append(w.violations, fmt.Sprintf("continuation of %s answered 404 with its session intact", prev))
		}
	default:
		w.violations = append(w.violations, fmt.Sprintf("turn continuing %q answered %d", prev, code))
	}
}

// pick returns a random completed response id, the newest when newest is
// set, or "" when none exists.
func (w *orInvWorld) pick(rng *rand.Rand, newest bool) string {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.completed) == 0 {
		return ""
	}
	if newest {
		return w.completed[len(w.completed)-1]
	}
	return w.completed[rng.Intn(len(w.completed))]
}

// terminate ends a session through its terminal claim and post-terminal
// hook, as a client terminate does.
func (w *orInvWorld) terminate(id string) {
	ctx := context.Background()
	end := w.terminal.BeginTerminal(id)
	defer end()
	var from session.State
	row, err := w.store.Update(ctx, orInvTenant, id, func(s *sessionstore.Session) error {
		if session.IsTerminal(s.State) {
			return sessionstore.ErrSuperseded
		}
		from = s.State
		s.State = session.StateCompleted
		sessionstore.MarkTerminated(s, from, time.Now().UTC())
		return nil
	})
	if err == nil {
		w.terminal.AfterTerminalWrite(ctx, from, row, nil)
	}
}

// erase terminates the session and deletes its row and transcript, as the
// §12.8 erasure does.
func (w *orInvWorld) erase(id string) {
	w.mu.Lock()
	w.erased[id] = true
	w.mu.Unlock()
	w.terminate(id)
	_ = w.store.Delete(context.Background(), orInvTenant, id)
	_, _ = w.transcripts.DeleteBySession(context.Background(), orInvTenant, id)
}

// rest delivers one REST message to the session and appends it to the
// session's transcript, as a POST /v1/sessions/{id}/messages delivery does.
func (w *orInvWorld) rest(id string) {
	content := "rest-" + strconv.FormatInt(w.nextToken.Add(1), 10)
	if !w.rt.deliverREST(id, content) {
		return
	}
	_, _ = w.transcripts.Append(context.Background(), orInvTenant, id,
		transcriptstore.Entry{Role: "user", Content: content})
}

// drive runs ops random operations.
func (w *orInvWorld) drive(rng *rand.Rand, ops int) {
	for i := 0; i < ops; i++ {
		fault := orInvNoFault
		switch r := rng.Intn(10); {
		case r == 0:
			fault = orInvRefuse
		case r == 1:
			fault = orInvFail
		}
		switch op := rng.Intn(20); {
		case op < 3:
			w.turn("", fault)
		case op < 9:
			if prev := w.pick(rng, true); prev != "" {
				w.turn(prev, fault)
			}
		case op < 12:
			if prev := w.pick(rng, false); prev != "" {
				w.turn(prev, fault)
			}
		case op < 14:
			if prev := w.pick(rng, true); prev != "" {
				var wg sync.WaitGroup
				wg.Add(2)
				go func() { defer wg.Done(); w.turn(prev, orInvNoFault) }()
				go func() { defer wg.Done(); w.turn(prev, orInvNoFault) }()
				wg.Wait()
			}
		case op < 17:
			if prev := w.pick(rng, false); prev != "" {
				sid, _ := orInvSplit(prev)
				w.rest(sid)
			}
		case op < 19:
			if prev := w.pick(rng, false); prev != "" {
				sid, _ := orInvSplit(prev)
				w.terminate(sid)
			}
		default:
			if prev := w.pick(rng, false); prev != "" && rng.Intn(2) == 0 {
				sid, _ := orInvSplit(prev)
				w.erase(sid)
			}
		}
	}
}

// orInvLineage keeps only the turn entries of a history, dropping REST
// deliveries and audit inputs.
func orInvLineage(msgs []executor.Message) []string {
	var out []string
	for _, m := range msgs {
		if strings.HasPrefix(m.Content, "in-") || strings.HasPrefix(m.Content, "out-") {
			out = append(out, m.Content)
		}
	}
	return out
}

// expected returns the conversation context a continuation of id must
// carry: the history the runtime held at the end of id's turn, cut at the
// newest erased ancestor of id's session.
func (w *orInvWorld) expected(id string) []string {
	sid, _ := orInvSplit(id)
	keep := map[string]bool{}
	for cur := sid; cur != ""; {
		if w.erased[cur] {
			break
		}
		keep[cur] = true
		cur, _ = orInvSplit(w.parentOf[cur])
	}
	var out []string
	for _, c := range orInvLineage(w.rt.snapshots[w.tokenOf[id]]) {
		k := orInvToken(c)
		if k < 0 {
			k, _ = strconv.Atoi(strings.TrimPrefix(c, "out-"))
		}
		if keep[w.sessionOfToken[k]] {
			out = append(out, c)
		}
	}
	return out
}

// audit reads each completed response's conversation context through a cold
// continuation by another user and returns every violated invariant.
func (w *orInvWorld) audit() []string {
	out := append([]string(nil), w.violations...)
	for i, id := range w.completed {
		sid, _ := orInvSplit(id)
		code, next := w.post("auditor-"+strconv.Itoa(i), id, "chk-"+strconv.Itoa(i))
		if w.erased[sid] {
			if code != http.StatusNotFound {
				out = append(out, fmt.Sprintf("continuation of %s on an erased session answered %d, want 404", id, code))
			}
			continue
		}
		if code != http.StatusOK {
			out = append(out, fmt.Sprintf("audit continuation of %s answered %d", id, code))
			continue
		}
		cold, _ := orInvSplit(next)
		w.rt.mu.Lock()
		sent := w.rt.firstSend[cold]
		w.rt.mu.Unlock()
		got, want := orInvLineage(sent), w.expected(id)
		if strings.Join(got, ",") != strings.Join(want, ",") {
			out = append(out, fmt.Sprintf("context of %s = %v, want %v", id, got, want))
		}
		w.terminate(cold)
	}
	for _, k := range w.failed {
		sid := w.rt.receiver[k]
		if sid == "" || w.erased[sid] {
			continue
		}
		row, err := w.store.Get(context.Background(), orInvTenant, sid)
		if err == nil && !session.IsTerminal(row.State) {
			out = append(out, fmt.Sprintf("failed turn %d left session %s %q", k, sid, row.State))
		}
	}
	for sid, n := range w.rt.releases {
		if n > 1 {
			out = append(out, fmt.Sprintf("session %s released %d times", sid, n))
		}
	}
	for sid, n := range w.store.terminals {
		if n > 1 {
			out = append(out, fmt.Sprintf("session %s entered a terminal state %d times", sid, n))
		}
	}
	return out
}

// spec: §15 (Open Responses conversation sessions), §15.1 (session transcript), §12.8 (Compliance Interfaces), §7.2 (Interactive Session Model)
// diagnosis: under a random interleaving of warm continuations, forks,
// concurrent continuations of one head, REST deliveries, executor refusals
// and failures, terminates, and erasures, a response id answered twice, a
// completed response's conversation context differed from the history its
// runtime held at the end of the turn (cut at an erased ancestor), an erased
// head answered other than 404, a failed turn left its session live, or a
// session was released or ended more than once. Replay with
// LENNY_LIFECYCLE_SEED set to the logged seed. Check reserve, commitWarm,
// serveCold, and endHeldSession in
// pkg/gateway/environment/translator/open_responses.go and rehydrate and
// record in continuity.go.
func TestOpenResponsesConversationInterleavingsHoldTheInvariants_spec_15(t *testing.T) {
	base := lifecycleSeed(t)
	for run := 0; run < 6; run++ {
		seed := base + int64(run)
		t.Run(fmt.Sprintf("seed-%d", seed), func(t *testing.T) {
			w := newORInvWorld(t)
			var wg sync.WaitGroup
			for g := 0; g < 4; g++ {
				wg.Add(1)
				go func(rng *rand.Rand) {
					defer wg.Done()
					w.drive(rng, 30)
				}(rand.New(rand.NewSource(seed*31 + int64(g))))
			}
			wg.Wait()
			if len(w.completed) == 0 {
				t.Fatalf("seed %d completed no response", seed)
			}
			for _, v := range w.audit() {
				t.Errorf("seed %d: %s", seed, v)
			}
		})
	}
}
