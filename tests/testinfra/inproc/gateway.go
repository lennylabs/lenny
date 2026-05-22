// SPDX-License-Identifier: MIT

package inproc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// gateway is the minimal in-process HTTP listener tier-7a multi-
// component scenarios drive. It exposes a tiny subset of the §15.1
// session lifecycle surface: POST /v1/sessions, GET /v1/sessions/{id},
// DELETE /v1/sessions/{id}. State is in-memory; the listener binds
// to an ephemeral loopback port and is reachable via Env.GatewayURL().
//
// Concurrency invariants the implementation honours:
//   - POST /v1/sessions with an Idempotency-Key replays cached responses.
//   - DELETE on a non-existent session returns 404.
//   - Sessions move through a documented state machine; concurrent
//     transitions hold a per-session mutex so observers never see
//     a partial state.
type gateway struct {
	mu       sync.Mutex
	sessions map[string]*session

	mxAtomic atomic.Int64
	idemHits atomic.Int64

	idem    map[string]idempotentResponse
	idemMu  sync.Mutex

	server   *http.Server
	listener net.Listener
}

type session struct {
	ID       string    `json:"id"`
	Status   string    `json:"status"`
	Created  time.Time `json:"created_at"`
	Runtime  string    `json:"runtime_ref"`
}

type idempotentResponse struct {
	Status int
	Body   []byte
}

func newGateway() *gateway {
	return &gateway{
		sessions: make(map[string]*session),
		idem:     make(map[string]idempotentResponse),
	}
}

// start binds the gateway to a loopback port and returns the resolved
// URL. Idempotent across repeated calls.
func (g *gateway) start() (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.server != nil {
		return "http://" + g.listener.Addr().String(), nil
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/sessions", g.handleSessions)
	mux.HandleFunc("/v1/sessions/", g.handleSessionDetail)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("ok")) })
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	g.listener = ln
	g.server = &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	go g.server.Serve(ln)
	return "http://" + ln.Addr().String(), nil
}

func (g *gateway) stop(ctx context.Context) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.server == nil {
		return nil
	}
	srv := g.server
	g.server = nil
	return srv.Shutdown(ctx)
}

func (g *gateway) handleSessions(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		g.create(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (g *gateway) handleSessionDetail(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/v1/sessions/")
	if id == "" {
		http.NotFound(w, r)
		return
	}
	switch r.Method {
	case http.MethodGet:
		g.get(w, id)
	case http.MethodDelete:
		g.delete(w, id)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

type createRequest struct {
	RuntimeRef string `json:"runtimeRef"`
	UserID     string `json:"userId"`
}

func (g *gateway) create(w http.ResponseWriter, r *http.Request) {
	idemKey := r.Header.Get("Idempotency-Key")
	if idemKey != "" {
		// Hold the lock across the full check + create + insert
		// sequence. Without this window N concurrent POSTs with the
		// same key all observe "no cached entry" and each creates a
		// distinct session, violating §11.5.
		g.idemMu.Lock()
		defer g.idemMu.Unlock()
		if cached, ok := g.idem[idemKey]; ok {
			g.idemHits.Add(1)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(cached.Status)
			_, _ = w.Write(cached.Body)
			return
		}
	}
	var req createRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	id := fmt.Sprintf("sess-%d", time.Now().UnixNano())
	sess := &session{
		ID:      id,
		Status:  "running",
		Created: time.Now().UTC(),
		Runtime: req.RuntimeRef,
	}
	// Marshal the response body before storing the pointer in the
	// session map. After the map insert, concurrent delete handlers
	// may mutate sess.Status; marshaling here against a struct only
	// this goroutine references is race-free.
	body, _ := json.Marshal(sess)
	g.mu.Lock()
	g.sessions[id] = sess
	g.mu.Unlock()
	if idemKey != "" {
		// idemMu held by the defer above; safe to mutate here.
		g.idem[idemKey] = idempotentResponse{Status: http.StatusCreated, Body: body}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_, _ = w.Write(body)
}

func (g *gateway) get(w http.ResponseWriter, id string) {
	// Copy the session under the lock so the JSON serialisation
	// after the unlock cannot race with a concurrent delete writing
	// to sess.Status.
	g.mu.Lock()
	sess, ok := g.sessions[id]
	var copy session
	if ok {
		copy = *sess
	}
	g.mu.Unlock()
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	body, _ := json.Marshal(copy)
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(body)
}

func (g *gateway) delete(w http.ResponseWriter, id string) {
	g.mu.Lock()
	sess, ok := g.sessions[id]
	if !ok {
		g.mu.Unlock()
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	sess.Status = "terminated"
	g.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

// SessionCount returns the live session count.
func (g *gateway) sessionCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.sessions)
}

// IdempotencyHits returns how many cached replays occurred.
func (g *gateway) idempotencyHits() int64 {
	return g.idemHits.Load()
}

// silence the unused atomic field linter if a scenario does not call sessionCount.
var _ = errors.New
