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
	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/gateway/environment/transcriptstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/translator"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/tests/testinfra/ownerprincipal"
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
// keyed by the turn's token, and counts releases. A release that ends a
// binding counts in releases; a release of a session that holds no binding is
// a no-op, as executor.SessionReleaser states, and counts in noopReleases.
//
// mu also orders deliveries against the session transcript. A REST delivery
// holds it across its history append and its transcript append, and a
// successful Send returns with it held so that the handler's transcript
// record of the turn (through orInvTranscripts.Append) happens before any
// other delivery. The runtime order and the transcript order of every entry
// therefore agree, which is what lets the audit compare REST entries.
type orInvRuntime struct {
	mu          sync.Mutex
	transcripts *transcriptstore.Memory
	// restOrigin maps a REST delivery's content to the session it reached.
	restOrigin map[string]string
	bound      map[string]bool
	history    map[string][]executor.Message
	snapshots  map[int][]executor.Message
	firstSend  map[string][]executor.Message
	receiver   map[int]string
	releases   map[string]int
	// noopReleases counts the releases of a session that held no binding.
	noopReleases map[string]int
	faults       map[int]orInvFault
}

func newORInvRuntime(transcripts *transcriptstore.Memory) *orInvRuntime {
	return &orInvRuntime{
		transcripts: transcripts, restOrigin: map[string]string{},
		bound: map[string]bool{}, history: map[string][]executor.Message{},
		snapshots: map[int][]executor.Message{}, firstSend: map[string][]executor.Message{},
		receiver: map[int]string{}, releases: map[string]int{}, noopReleases: map[string]int{},
		faults: map[int]orInvFault{},
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
// turn token k carried by the last message. A successful Send returns with mu
// held; the handler's transcript record of the turn releases it. Every other
// outcome releases it before returning.
func (m *orInvRuntime) Send(_ context.Context, id string, msgs []executor.Message) (executor.Response, error) {
	runtime.Gosched()
	m.mu.Lock()
	resp, err := m.deliver(id, msgs)
	if err != nil {
		m.mu.Unlock()
	}
	return resp, err
}

// deliver is Send's body, run with mu held.
func (m *orInvRuntime) deliver(id string, msgs []executor.Message) (executor.Response, error) {
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
// appends it to the session's transcript as one step under mu, as a POST
// /v1/sessions/{id}/messages delivery does.
func (m *orInvRuntime) deliverREST(tenantID, id, content string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.bound[id] {
		return
	}
	m.history[id] = append(m.history[id], executor.Message{Role: "user", Content: content})
	m.restOrigin[content] = id
	_, _ = m.transcripts.Append(context.Background(), tenantID, id,
		transcriptstore.Entry{Role: "user", Content: content})
}

// orInvTranscripts is the transcript store the handler records through. Every
// Append it receives is the record of a turn whose successful Send returned
// with the runtime's mu held, so Append releases mu once the entries are
// stored. dropREST makes Get omit REST entries, a rehydrate defect the
// checker must report.
type orInvTranscripts struct {
	*transcriptstore.Memory
	rt       *orInvRuntime
	dropREST atomic.Bool
}

func (s *orInvTranscripts) Append(ctx context.Context, tenantID, sessionID string, entries ...transcriptstore.Entry) (uint64, error) {
	defer s.rt.mu.Unlock()
	return s.Memory.Append(ctx, tenantID, sessionID, entries...)
}

func (s *orInvTranscripts) Get(ctx context.Context, tenantID, sessionID string) ([]transcriptstore.Entry, error) {
	entries, err := s.Memory.Get(ctx, tenantID, sessionID)
	if err != nil || !s.dropREST.Load() {
		return entries, err
	}
	kept := entries[:0:0]
	for _, e := range entries {
		if !strings.HasPrefix(e.Content, "rest-") {
			kept = append(kept, e)
		}
	}
	return kept, nil
}

func (m *orInvRuntime) Close(ctx context.Context, id string) error { return m.Release(ctx, id, "") }

// Release ends the session's binding and counts the call, as a no-op when
// the session holds no binding.
func (m *orInvRuntime) Release(_ context.Context, id string, _ executor.Disposition) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.bound[id] {
		m.noopReleases[id]++
		return nil
	}
	m.releases[id]++
	m.bound[id] = false
	return nil
}

// isBound reports whether the session still holds its binding.
func (m *orInvRuntime) isBound(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.bound[id]
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
		ID: id, TenantID: spec.TenantID, UserID: ownerprincipal.Subject(ctx), RuntimeRef: spec.RuntimeRef,
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
	recorder    *orInvTranscripts
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
	transcripts := transcriptstore.NewMemory()
	rt := newORInvRuntime(transcripts)
	store := &orInvStore{Store: memstore.New(), terminals: map[string]int{}}
	w := &orInvWorld{
		t: t, store: store, transcripts: transcripts, rt: rt,
		recorder: &orInvTranscripts{Memory: transcripts, rt: rt},
		terminal: responsesstub.ReleasingTerminal{Exec: rt},
		tokenOf:  map[string]int{}, sessionOfToken: map[int]string{}, parentOf: map[string]string{},
		erased: map[string]bool{},
	}
	w.handler = translator.NewOpenResponsesHandler(store, rt, translator.OpenResponsesOptions{
		SingleShotBinder: orInvBinder{store: store, rt: rt},
		DefaultRuntime:   orInvRuntimeRef,
		Transcripts:      w.recorder,
		HolderForwarder:  responsesstub.LocalForwarder{},
		Terminal:         w.terminal,
		SessionAccess:    sessionserver.New(store, sessionserver.Options{}),
		DevTenantHeader:  true,
	}).Handler()
	return w
}

// orInvOwner is the principal every driver turn runs under, the owner of
// every session the driver creates.
var orInvOwner = authmw.Principal{Subject: orInvUser}

// orInvAuditor returns the principal of the i-th audit continuation: a
// tenant-admin that may read every session of the tenant and owns none of
// the driver's, so its continuation always runs cold on a session it owns.
//
// spec: §10.2 (Session ownership) rule 4; §15 (Open Responses conversation
// sessions).
func orInvAuditor(i int) authmw.Principal {
	return authmw.Principal{Subject: "auditor-" + strconv.Itoa(i), TenantID: orInvTenant, Roles: []auth.Role{auth.RoleTenantAdmin}}
}

// post sends one turn as principal p and returns the status and the
// response id.
func (w *orInvWorld) post(p authmw.Principal, prev, input string) (int, string) {
	body := map[string]any{"model": orInvRuntimeRef, "input": input}
	if prev != "" {
		body["previous_response_id"] = prev
	}
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(raw))
	req = req.WithContext(authmw.WithPrincipal(req.Context(), p))
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
	code, id := w.post(orInvOwner, prev, "in-"+strconv.Itoa(k))
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

// erase deletes the session's row and transcript, as the §12.8 erasure does.
// When publishLands is set, the drain's terminal transition runs first and
// its CH-BINDRELEASE publish reaches this replica, whose post-terminal path
// releases the binding. Otherwise the publish is lost, which is not
// redelivered, and the row is deleted while this replica still holds the
// binding, so a turn that fails on the missing row is the release that
// remains.
func (w *orInvWorld) erase(id string, publishLands bool) {
	w.mu.Lock()
	w.erased[id] = true
	w.mu.Unlock()
	if publishLands {
		w.terminate(id)
	}
	_ = w.store.Delete(context.Background(), orInvTenant, id)
	_, _ = w.transcripts.DeleteBySession(context.Background(), orInvTenant, id)
}

// rest delivers one REST message to the session and appends it to the
// session's transcript, as a POST /v1/sessions/{id}/messages delivery does.
func (w *orInvWorld) rest(id string) {
	w.mu.Lock()
	erased := w.erased[id]
	w.mu.Unlock()
	if erased {
		return
	}
	w.rt.deliverREST(orInvTenant, id, "rest-"+strconv.FormatInt(w.nextToken.Add(1), 10))
}

// drive runs ops random operations.
func (w *orInvWorld) drive(rng *rand.Rand, ops int) {
	for i := 0; i < ops; i++ {
		fault := orInvNoFault
		switch rng.Intn(10) {
		case 0:
			fault = orInvRefuse
		case 1:
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
				w.erase(sid, rng.Intn(2) == 0)
			}
		}
	}
}

// orInvLineage keeps the turn and REST entries of a history in order,
// dropping only the audit inputs and their answers.
func orInvLineage(msgs []executor.Message) []string {
	var out []string
	for _, m := range msgs {
		if orInvConversational(m.Content) {
			out = append(out, m.Content)
		}
	}
	return out
}

func orInvConversational(content string) bool {
	return strings.HasPrefix(content, "in-") || strings.HasPrefix(content, "out-") ||
		strings.HasPrefix(content, "rest-")
}

// origin returns the session an entry of the lineage was delivered to: the
// session that answered a turn entry, or the session a REST delivery reached.
func (w *orInvWorld) origin(content string) string {
	if strings.HasPrefix(content, "rest-") {
		w.rt.mu.Lock()
		defer w.rt.mu.Unlock()
		return w.rt.restOrigin[content]
	}
	k := orInvToken(content)
	if k < 0 {
		k, _ = strconv.Atoi(strings.TrimPrefix(content, "out-"))
	}
	return w.sessionOfToken[k]
}

// expected returns the conversation context a continuation of id must
// carry: the history the runtime held at the end of id's turn, REST
// deliveries included in delivery order, cut at the newest erased ancestor of
// id's session.
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
	w.rt.mu.Lock()
	snapshot := w.rt.snapshots[w.tokenOf[id]]
	w.rt.mu.Unlock()
	var out []string
	for _, c := range orInvLineage(snapshot) {
		if keep[w.origin(c)] {
			out = append(out, c)
		}
	}
	return out
}

// audit reads each completed response's conversation context through a cold
// continuation by a tenant-admin auditor that may read the session but does
// not own it, and returns every violated invariant.
func (w *orInvWorld) audit() []string {
	out := append([]string(nil), w.violations...)
	for i, id := range w.completed {
		sid, _ := orInvSplit(id)
		code, next := w.post(orInvAuditor(i), id, "chk-"+strconv.Itoa(i))
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
		if sid != "" && w.rt.isBound(sid) {
			out = append(out, fmt.Sprintf("failed turn %d left session %s holding its binding", k, sid))
		}
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
	for sid, n := range w.rt.noopReleases {
		if !w.erased[sid] {
			out = append(out, fmt.Sprintf("session %s with no erasure released %d extra times", sid, n))
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
// runtime held at the end of the turn, REST deliveries included in order
// (cut at an erased ancestor), an erased
// head answered other than 404, a failed turn left its session live or
// holding its executor binding (including a turn that failed on a row an
// erasure deleted after losing its CH-BINDRELEASE publish), a session's
// binding was ended more than once, a session no erasure touched received a
// redundant release, or a session was ended more than once. Replay with
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

// orInvRESTScenario runs a warm turn, a REST delivery, a warm continuation,
// and a second REST delivery on one session, and returns the two response
// ids.
func orInvRESTScenario(t *testing.T, w *orInvWorld) (string, string) {
	t.Helper()
	w.turn("", orInvNoFault)
	first := w.pick(nil, true)
	if first == "" {
		t.Fatalf("first turn did not complete: %v", w.violations)
	}
	sid, _ := orInvSplit(first)
	w.rest(sid)
	w.turn(first, orInvNoFault)
	second := w.pick(nil, true)
	if second == first {
		t.Fatalf("warm continuation did not complete: %v", w.violations)
	}
	w.rest(sid)
	return first, second
}

// spec: §15 (Open Responses conversation sessions), §15.1 (session transcript)
// diagnosis: the invariant checker's expected context dropped or misplaced a
// REST delivery, or it passed a rehydrate that omitted one. A REST entry
// delivered between two turns belongs in the later turn's Seq-bounded prefix
// and out of the earlier one's, and the checker compares REST entries, so a
// rehydrate that drops them is a reported violation. Check orInvLineage and
// expected in this file and rehydrate and prefix in
// pkg/gateway/environment/translator/continuity.go.
func TestOpenResponsesInvariantCheckerComparesRESTDeliveries_spec_15(t *testing.T) {
	t.Run("intact rehydrate matches the runtime history with REST entries", func(t *testing.T) {
		w := newORInvWorld(t)
		first, second := orInvRESTScenario(t, w)
		if got, want := strings.Join(w.expected(first), ","), "in-1,out-1"; got != want {
			t.Errorf("expected(%s) = %s, want %s", first, got, want)
		}
		if got, want := strings.Join(w.expected(second), ","), "in-1,out-1,rest-2,in-3,out-3"; got != want {
			t.Errorf("expected(%s) = %s, want %s", second, got, want)
		}
		for _, v := range w.audit() {
			t.Errorf("intact rehydrate reported: %s", v)
		}
	})
	t.Run("rehydrate that drops a REST entry is a violation", func(t *testing.T) {
		w := newORInvWorld(t)
		_, second := orInvRESTScenario(t, w)
		w.recorder.dropREST.Store(true)
		found := false
		for _, v := range w.audit() {
			if strings.HasPrefix(v, "context of "+second) {
				found = true
			}
		}
		if !found {
			t.Errorf("audit passed a rehydrate of %s that omitted its REST delivery", second)
		}
	})
}
