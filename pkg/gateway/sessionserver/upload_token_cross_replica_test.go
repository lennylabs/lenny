// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"context"
	"encoding/json"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/uploadtoken"
)

const crossReplicaTenant = "acme"

// newReplica returns a Server with its own issuer and verifier over the shared
// store and blob store. The two replicas share nothing else, which is the
// condition a process boundary imposes on them.
func newReplica(t *testing.T, store sessionstore.Store, blobs blobstore.Store, maxBytes int64) http.Handler {
	t.Helper()
	srv := sessionserver.New(store, sessionserver.Options{
		DevTenantHeader:          true,
		UploadTokenIssuer:        uploadtoken.NewIssuer(nil),
		UploadTokenVerifier:      uploadtoken.NewVerifier(nil),
		Blobs:                    blobs,
		MaxUploadBytesPerSession: maxBytes,
	})
	return asOwner(srv.Handler())
}

// createOn creates a session through h and returns its id and token.
func createOn(t *testing.T, h http.Handler) (string, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions", strings.NewReader(`{"runtimeRef":"claude-code"}`))
	req.Header.Set("X-Lenny-Tenant-ID", crossReplicaTenant)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: status %d, body %s", rr.Code, rr.Body.String())
	}
	var created struct {
		ID          string `json:"id"`
		UploadToken string `json:"uploadToken"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	return created.ID, created.UploadToken
}

// replicaPost sends POST /v1/sessions/{id}/{action} with the token and ctx.
func replicaPost(ctx context.Context, h http.Handler, id, action, token, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/"+id+"/"+action, strings.NewReader(body)).WithContext(ctx)
	req.Header.Set("X-Lenny-Tenant-ID", crossReplicaTenant)
	if token != "" {
		req.Header.Set("X-Lenny-Upload-Token", token)
	}
	if action == "upload" {
		req.Header.Set("Content-Type", "text/plain")
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

// A token minted on one replica uploads and finalizes on the other, and a
// replay on the issuer answers 410: the row is the only state the replicas
// share.
//
// spec: §7.1 (uploadToken format and security properties), §10.1.1 (Gateway replicas)
func TestTokenMintedOnOneReplicaVerifiesOnThePeer_spec_7_1(t *testing.T) {
	store, blobs := memstore.New(), blobstore.NewMemoryStore(nil)
	a, b := newReplica(t, store, blobs, 0), newReplica(t, store, blobs, 0)
	ctx := context.Background()

	id, tok := createOn(t, a)
	if rr := replicaPost(ctx, b, id, "upload", tok, "payload"); rr.Code != http.StatusCreated {
		t.Fatalf("upload on the peer: status %d, body %s", rr.Code, rr.Body.String())
	}
	if rr := replicaPost(ctx, b, id, "finalize", tok, ""); rr.Code != http.StatusOK {
		t.Fatalf("finalize on the peer: status %d, body %s", rr.Code, rr.Body.String())
	}
	for _, action := range tokenActions {
		rr := replicaPost(ctx, a, id, action, tok, "payload")
		if rr.Code != http.StatusGone || finalizeErrorCode(t, rr) != "UPLOAD_TOKEN_CONSUMED" {
			t.Errorf("%s replay on the issuer: status %d, body %s; want 410 UPLOAD_TOKEN_CONSUMED", action, rr.Code, rr.Body.String())
		}
	}
}

// The §11.1 cumulative cap is counted in each replica's memory, so a session
// reaches it once on each replica.
//
// spec: §11.1 (upload caps), §10.1.1 (Gateway replicas)
func TestUploadByteCapHoldsPerReplica_spec_11_1(t *testing.T) {
	store, blobs := memstore.New(), blobstore.NewMemoryStore(nil)
	a, b := newReplica(t, store, blobs, 10), newReplica(t, store, blobs, 10)
	ctx := context.Background()
	id, tok := createOn(t, a)
	body := "0123456789"

	if rr := replicaPost(ctx, a, id, "upload", tok, body); rr.Code != http.StatusCreated {
		t.Fatalf("upload to the cap on A: status %d, body %s", rr.Code, rr.Body.String())
	}
	if rr := replicaPost(ctx, a, id, "upload", tok, "x"); rr.Code != http.StatusTooManyRequests {
		t.Fatalf("upload past the cap on A: status %d, body %s", rr.Code, rr.Body.String())
	}
	if rr := replicaPost(ctx, b, id, "upload", tok, body); rr.Code != http.StatusCreated {
		t.Fatalf("upload to the cap on B: status %d, body %s", rr.Code, rr.Body.String())
	}
}

// seqClock is the logical clock the interleaving test orders events on.
type seqClock struct{ n atomic.Int64 }

func (c *seqClock) tick() int64 { return c.n.Add(1) }

// requestTraceKey carries a requestTrace on the request context.
type requestTraceKey struct{}

// requestTrace records the logical start time of each primary read one request
// makes, in order.
type requestTrace struct {
	mu    sync.Mutex
	reads []int64
}

func (r *requestTrace) add(seq int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reads = append(r.reads, seq)
}

func (r *requestTrace) snapshot() []int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]int64(nil), r.reads...)
}

// orderingStore stamps every primary read and every committed state change on
// one logical clock. A read is stamped before it starts, so a stamp past a
// commit's stamp proves the read followed the commit.
type orderingStore struct {
	*memstore.Store
	clock seqClock

	mu          sync.Mutex
	readyAt     int64
	readyWrites int
	terminalAt  int64
}

func (o *orderingStore) GetPrimary(ctx context.Context, tenantID, id string) (sessionstore.Session, error) {
	if tr, ok := ctx.Value(requestTraceKey{}).(*requestTrace); ok {
		tr.add(o.clock.tick())
	}
	return o.Store.GetPrimary(ctx, tenantID, id)
}

func (o *orderingStore) Update(ctx context.Context, tenantID, id string, mutate func(*sessionstore.Session) error) (sessionstore.Session, error) {
	var from session.State
	out, err := o.Store.Update(ctx, tenantID, id, func(row *sessionstore.Session) error {
		from = row.State
		return mutate(row)
	})
	if err != nil || from == out.State {
		return out, err
	}
	seq := o.clock.tick()
	o.mu.Lock()
	defer o.mu.Unlock()
	switch {
	case out.State == session.StateReady:
		o.readyAt, o.readyWrites = seq, o.readyWrites+1
	case session.IsTerminal(out.State) && o.terminalAt == 0:
		o.terminalAt = seq
	}
	return out, err
}

func (o *orderingStore) commits() (readyAt int64, readyWrites int, terminalAt int64) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.readyAt, o.readyWrites, o.terminalAt
}

// raceOp is one request of a randomized schedule and its observed result.
type raceOp struct {
	action  string
	replica int
	delay   int
	status  int
	code    string
	reads   []int64
}

// randomSchedule returns a shuffled mix of uploads, finalizes, and optional
// terminates, each bound to a replica and a start delay in scheduler yields.
func randomSchedule(rng *rand.Rand, withTerminate bool) []*raceOp {
	var ops []*raceOp
	add := func(action string, n int) {
		for range n {
			ops = append(ops, &raceOp{action: action, replica: rng.Intn(2), delay: rng.Intn(64)})
		}
	}
	add("upload", 1+rng.Intn(4))
	add("finalize", 1+rng.Intn(3))
	if withTerminate {
		add("terminate", 1)
	}
	rng.Shuffle(len(ops), func(i, j int) { ops[i], ops[j] = ops[j], ops[i] })
	return ops
}

// runSchedule fires every op concurrently and records its result.
func runSchedule(t *testing.T, replicas [2]http.Handler, id, tok string, ops []*raceOp) {
	t.Helper()
	var wg sync.WaitGroup
	for _, op := range ops {
		wg.Add(1)
		go func(op *raceOp) {
			defer wg.Done()
			for range op.delay {
				runtime.Gosched()
			}
			trace := &requestTrace{}
			ctx := context.WithValue(context.Background(), requestTraceKey{}, trace)
			body := ""
			if op.action == "upload" {
				body = "payload"
			}
			rr := replicaPost(ctx, replicas[op.replica], id, op.action, tok, body)
			op.status, op.reads = rr.Code, trace.snapshot()
			if rr.Code >= 400 {
				var env struct {
					Error struct {
						Code string `json:"code"`
					} `json:"error"`
				}
				_ = json.Unmarshal(rr.Body.Bytes(), &env)
				op.code = env.Error.Code
			}
		}(op)
	}
	wg.Wait()
}

// checkScheduleInvariants asserts the §7.1 and §15.1 ordering rules against
// the commit stamps the store recorded.
func checkScheduleInvariants(t *testing.T, seed int64, ops []*raceOp, store *orderingStore, terminateFirst bool) {
	t.Helper()
	readyAt, readyWrites, terminalAt := store.commits()
	finalized := 0
	for _, op := range ops {
		switch op.action {
		case "finalize":
			if op.status == http.StatusOK {
				finalized++
				if len(op.reads) == 0 || readyAt == 0 || op.reads[0] > readyAt {
					t.Errorf("seed %d: finalize answered 200 with admission read %v after the ready commit %d", seed, op.reads, readyAt)
				}
				if terminalAt != 0 && terminalAt < readyAt {
					t.Errorf("seed %d: a ready commit %d followed the terminal commit %d", seed, readyAt, terminalAt)
				}
			}
			if readyAt != 0 && len(op.reads) > 0 && op.reads[0] > readyAt && terminalAt == 0 &&
				(op.status != http.StatusGone || op.code != "UPLOAD_TOKEN_CONSUMED") {
				t.Errorf("seed %d: finalize admitted after the ready commit answered %d %s", seed, op.status, op.code)
			}
		case "upload":
			checkUploadOutcome(t, seed, op, readyAt)
		}
	}
	if finalized > 1 || readyWrites > 1 {
		t.Errorf("seed %d: %d finalizes answered 200 and %d ready commits, want at most one", seed, finalized, readyWrites)
	}
	if terminateFirst && finalized != 0 {
		t.Errorf("seed %d: a session terminated first produced %d successful finalizes", seed, finalized)
	}
}

// checkUploadOutcome asserts an upload's result against the ready commit.
func checkUploadOutcome(t *testing.T, seed int64, op *raceOp, readyAt int64) {
	t.Helper()
	if readyAt == 0 || len(op.reads) == 0 {
		return
	}
	if op.status == http.StatusCreated && op.reads[len(op.reads)-1] > readyAt {
		t.Errorf("seed %d: upload answered 201 with reads %v after the ready commit %d", seed, op.reads, readyAt)
	}
	admittedBefore := op.reads[0] < readyAt
	recheckedAfter := len(op.reads) == 2 && op.reads[1] > readyAt
	if admittedBefore && recheckedAfter && (op.status != http.StatusGone || op.code != "UPLOAD_CHANNEL_CLOSED") {
		t.Errorf("seed %d: upload rechecked after the ready commit answered %d %s, want 410 UPLOAD_CHANNEL_CLOSED", seed, op.status, op.code)
	}
}

// Random interleavings of uploads, finalizes, and terminates across two
// replicas hold the §7.1 rules. Each schedule logs its seed on failure. Run
// under -race.
//
// spec: §7.1 (Single-use invalidation, Check order), §15.1 (finalize precondition), §10.1.1 (Gateway replicas)
func TestRandomizedCrossReplicaInterleavings_spec_7_1(t *testing.T) {
	base := time.Now().UnixNano()
	t.Logf("base seed %d", base)
	const schedules = 150
	for i := range schedules {
		seed := base + int64(i)
		rng := rand.New(rand.NewSource(seed))
		store := &orderingStore{Store: memstore.New()}
		blobs := blobstore.NewMemoryStore(nil)
		replicas := [2]http.Handler{newReplica(t, store, blobs, 0), newReplica(t, store, blobs, 0)}
		id, tok := createOn(t, replicas[0])

		// One schedule in four terminates ahead of the race.
		terminateFirst := rng.Intn(4) == 0
		if terminateFirst {
			if rr := replicaPost(context.Background(), replicas[rng.Intn(2)], id, "terminate", "", ""); rr.Code >= 300 {
				t.Fatalf("seed %d: terminate: status %d, body %s", seed, rr.Code, rr.Body.String())
			}
		}
		ops := randomSchedule(rng, !terminateFirst && rng.Intn(2) == 0)
		runSchedule(t, replicas, id, tok, ops)
		checkScheduleInvariants(t, seed, ops, store, terminateFirst)
	}
}
