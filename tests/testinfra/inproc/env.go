// SPDX-License-Identifier: MIT

package inproc

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/tests/testinfra/fakekube"
	runtimestub "github.com/lennylabs/lenny/tests/testinfra/stubs/runtime"
)

// Config configures an Env.
type Config struct {
	// GatewayAddr is the loopback address the in-process gateway
	// binds to. Empty means an automatically-assigned free port.
	GatewayAddr string

	// AdapterLatency is the artificial delay the runtime stub adds
	// to each tool call. Zero means respond immediately.
	AdapterLatency time.Duration

	// AdapterErrorRate is the fraction of tool calls the runtime
	// stub returns as errors. Zero means never error.
	AdapterErrorRate float64

	// WatchLag is the delay between an object mutation in fakekube
	// and the corresponding watch event firing.
	WatchLag time.Duration

	// SlotCounterMaxConcurrent is the §5.2 cap the slot counter
	// enforces. Zero means use the default of 4.
	SlotCounterMaxConcurrent int

	// AdmissionPerRuntimePerMinute is the §11.1 per-runtime
	// requests-per-minute admission limit the gateway enforces against
	// the embedded Redis. Zero means the harness default, which is set
	// high enough that load profiles are not rejected.
	AdmissionPerRuntimePerMinute int
}

// Env is a running in-process Lenny environment scoped to one
// scenario. Multiple scenarios run their own Env in parallel; the
// Env types do not share state.
type Env struct {
	config Config

	mu      sync.Mutex
	started bool
	redis   *miniredis.Miniredis
	rdb     *redis.Client
	fakeAPI *fakekube.Surface
	adapter *runtimestub.Stub
	gw      *gateway

	gatewayURL string
}

// New returns an Env configured but not yet started.
func New(c Config) *Env {
	return &Env{config: c}
}

// Start brings the environment up. It is an error to call Start more
// than once on the same Env.
//
// Starts miniredis, the fakekube surface, the runtime stub, and the
// in-process Lenny gateway. The gateway binds to a loopback port
// reachable via GatewayURL(); session-lifecycle scenarios drive it
// over HTTP.
func (e *Env) Start(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.started {
		return errors.New("inproc: Env already started")
	}
	mr, err := miniredis.Run()
	if err != nil {
		return err
	}
	e.redis = mr
	e.rdb = redis.NewClient(&redis.Options{Addr: mr.Addr()})
	e.fakeAPI = fakekube.New()
	e.fakeAPI.SetWatchLag(e.config.WatchLag)
	e.adapter = runtimestub.New(runtimestub.Config{
		ResponseLatency: e.config.AdapterLatency,
		ErrorRate:       e.config.AdapterErrorRate,
	})
	e.gw = newGateway(e.rdb, e.config)
	url, err := e.gw.start()
	if err != nil {
		return err
	}
	e.gatewayURL = url
	e.started = true
	return nil
}

// Stop tears the environment's network surfaces down. The internal
// state (session count, idempotency hit counter, fake-K8s objects)
// stays readable so scenario Assert paths can observe the final
// values after the loadgen driver invokes Teardown.
func (e *Env) Stop(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.started {
		return nil
	}
	if e.gw != nil {
		_ = e.gw.stop(ctx)
		// Keep e.gw set so SessionCount / IdempotencyHits remain
		// readable; the gateway listener is no longer serving but
		// its state is frozen for assertions.
		e.gatewayURL = ""
	}
	if e.rdb != nil {
		_ = e.rdb.Close()
		e.rdb = nil
	}
	if e.redis != nil {
		e.redis.Close()
		e.redis = nil
	}
	e.started = false
	return nil
}

// SessionCount returns how many sessions the gateway created over the
// run. A §15.1 DELETE moves a session to `cancelled` rather than
// removing it, so the count does not fall as sessions terminate. Used
// by scenarios that assert lifecycle invariants.
func (e *Env) SessionCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.gw == nil {
		return 0
	}
	return e.gw.sessionCount()
}

// IdempotencyHits returns the count of replay-through-cache hits the
// gateway observed. Used by scenarios that assert idempotency under
// load.
func (e *Env) IdempotencyHits() int64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.gw == nil {
		return 0
	}
	return e.gw.idempotencyHits()
}

// RedisAddr returns the loopback address the embedded miniredis is
// listening on. Empty before Start() or after Stop().
func (e *Env) RedisAddr() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.redis == nil {
		return ""
	}
	return e.redis.Addr()
}

// FakeKube returns the fake Kubernetes API surface. nil before Start()
// or after Stop().
func (e *Env) FakeKube() *fakekube.Surface {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.fakeAPI
}

// Adapter returns the runtime stub. nil before Start() or after Stop().
func (e *Env) Adapter() *runtimestub.Stub {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.adapter
}

// GatewayURL returns the loopback URL of the in-process gateway,
// or "" before Start() / after Stop().
func (e *Env) GatewayURL() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.gatewayURL
}
