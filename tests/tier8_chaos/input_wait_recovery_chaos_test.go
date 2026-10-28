// SPDX-License-Identifier: MIT

//go:build chaos

// Tier-8 chaos tests for the lenny/request_input row writes and the
// connection recovery, against real gateway processes and Postgres. Each
// case parks a session in input_required through a lenny/request_input call
// over a gateway's GatewayControl listener, presenting the pod's agent
// SPIFFE identity, then breaks the waiting replica or its Postgres and
// asserts that the row converges out of input_required instead of staying
// stranded on a wait no registry holds.
package tier8_chaos_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/common/seqname"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/watchdog"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/pgstore"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/gateway"
	"github.com/lennylabs/lenny/tests/testinfra/gatewaypki"
	"github.com/lennylabs/lenny/tests/testinfra/inputwaitrig"
	"github.com/lennylabs/lenny/tests/testinfra/ports"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
	"github.com/lennylabs/lenny/tests/testinfra/tcpfault"
)

const (
	iwChaosTenant  = "acme"
	iwChaosSession = "00000000-0000-4000-8000-0000000007a1"
	iwChaosPod     = "pod-a"
)

// iwChaosEnv is one Postgres, an optional fault proxy in front of it, and
// the PKI the gateways and the pod share.
type iwChaosEnv struct {
	pg    *containers.Postgres
	proxy *tcpfault.Proxy
	dsn   string
	pki   *gatewaypki.Material
	store sessionstore.Store
}

func newIWChaosEnv(t *testing.T, proxied bool) *iwChaosEnv {
	t.Helper()
	gateway.SkipUnlessAvailable(t)
	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: filepath.Join(schematest.RepoRoot(t), "migrations"),
	})
	e := &iwChaosEnv{pg: pg, dsn: pg.DSN, pki: gatewaypki.Mint(t), store: pgstore.New(pg.Pool)}
	if proxied {
		e.proxy, e.dsn = tcpfault.StartURL(t, pg.DSN)
	}
	ctx := context.Background()
	if _, err := pg.Pool.Exec(ctx, `INSERT INTO tenants (id, genesis_nonce) VALUES ($1, $2)`, iwChaosTenant, []byte{0x01}); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	if _, err := pg.Pool.Exec(ctx, "CREATE SEQUENCE IF NOT EXISTS "+seqname.AuditSequenceName(iwChaosTenant)+" START WITH 1 INCREMENT BY 1 NO CYCLE"); err != nil {
		t.Fatalf("provision audit sequence: %v", err)
	}
	if err := e.store.Create(ctx, sessionstore.Session{
		ID: iwChaosSession, TenantID: iwChaosTenant, UserID: "alice@acme.com", RuntimeRef: "echo",
		State: session.StateRunning, PodAssignment: iwChaosPod,
	}); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	return e
}

// gatewayReplica is one gateway process and its GatewayControl address.
type gatewayReplica struct {
	proc *gateway.Process
	addr string
}

// start boots a gateway replica with a GatewayControl listener that
// verifies pod client certificates against the shared CA.
func (e *iwChaosEnv) start(t *testing.T) *gatewayReplica {
	t.Helper()
	addr := ports.Reserve(t)
	proc := gateway.StartOpts(t, gateway.Options{Args: []string{
		"--no-environment-policy", "allow-all", "--dev-mode",
		"--postgres-dsn=" + e.dsn, "--postgres-billing-audit-ddl-dsn=" + e.pg.DSN,
		"--grpc-addr=" + addr,
		"--adapter-tls-cert=" + e.pki.CertFile, "--adapter-tls-key=" + e.pki.KeyFile, "--adapter-ca=" + e.pki.CAFile,
	}})
	return &gatewayReplica{proc: proc, addr: addr}
}

// dial opens a pod connection to the replica and returns it once a call
// carrying the session ID has been served, which runs the connection's
// recovery.
func (e *iwChaosEnv) dial(t *testing.T, r *gatewayReplica) adapterv1.GatewayControlClient {
	t.Helper()
	c := inputwaitrig.DialAddr(t, r.addr, e.pki, inputwaitrig.AgentURI(iwChaosPod))
	deadline := time.Now().Add(15 * time.Second)
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		err := inputwaitrig.Ping(ctx, c, iwChaosSession)
		cancel()
		if err == nil {
			return c
		}
		if time.Now().After(deadline) {
			t.Fatalf("GatewayControl on %s never served a call: %v", r.addr, err)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func (e *iwChaosEnv) row(t *testing.T) sessionstore.Session {
	t.Helper()
	row, err := e.store.Get(context.Background(), iwChaosTenant, iwChaosSession)
	if err != nil {
		t.Fatalf("read the session row: %v", err)
	}
	return row
}

// park starts a lenny/request_input call on c and waits for the row to
// reach input_required. The returned cancel ends the call from the pod.
func (e *iwChaosEnv) park(t *testing.T, c adapterv1.GatewayControlClient) (<-chan inputwaitrig.Result, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	done := inputwaitrig.RequestInput(ctx, c, iwChaosSession, "req-chaos")
	e.waitState(t, session.StateInputRequired, 15*time.Second)
	return done, cancel
}

func (e *iwChaosEnv) waitState(t *testing.T, want session.State, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for {
		row := e.row(t)
		if row.State == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("row is %s/%q, want %s within %s", row.State, row.InputWaitID, want, within)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// spec: 7.2 (Session state machine), 10.1.1 (Stateless Replicas and
// Per-Session Coordination), 28.3 (Link register)
// diagnosis: a session parked in input_required on one gateway replica
// stayed input_required after its pod reconnected to another replica. The
// replica that accepted the pod's new LNK-GWCONTROL connection did not run
// the recovery before the pod's first call, did not see that it holds no wait
// the row names, or its write did not reach Postgres.
func TestInputWaitRecoveredOnReconnectToAnotherReplica_spec_7_2(t *testing.T) {
	e := newIWChaosEnv(t, false)
	a := e.start(t)
	e.park(t, e.dial(t, a))
	b := e.start(t)
	e.dial(t, b)
	if row := e.row(t); row.State != session.StateRunning {
		t.Errorf("after the pod's first call on replica B the row is %s/%q, want running", row.State, row.InputWaitID)
	}
}

// spec: 7.2 (Session state machine), 10.1.1 (Stateless Replicas and
// Per-Session Coordination), 28.3 (Link register)
// diagnosis: after the waiting replica was killed with SIGKILL, so no drain
// and no exit write ran, the row stayed input_required on a wait that died
// with the process. The restarted replica did not recover the row on the
// pod's first call over its new connection.
func TestInputWaitRecoveredAfterWaiterKilled_spec_7_2(t *testing.T) {
	e := newIWChaosEnv(t, false)
	a := e.start(t)
	e.park(t, e.dial(t, a))
	a.proc.Kill(t)
	if row := e.row(t); row.State != session.StateInputRequired {
		t.Fatalf("after SIGKILL the row is %s, want input_required (no exit write runs in a killed process)", row.State)
	}
	restarted := e.start(t)
	e.dial(t, restarted)
	if row := e.row(t); row.State != session.StateRunning {
		t.Errorf("after the restarted replica served the pod the row is %s/%q, want running", row.State, row.InputWaitID)
	}
}

// spec: 7.2 (Session state machine), 10.1.1 (Stateless Replicas and
// Per-Session Coordination), 28.3 (Link register)
// diagnosis: the exit write of a wait failed because Postgres was
// unreachable, and the row stayed input_required after Postgres returned.
// Either the failed exit write did not re-arm the replica's connections, so
// the next call on the already recovered connection ran no recovery, or the
// recovery's write did not converge the row.
func TestInputWaitConvergesAfterExitWriteLostToPostgresOutage_spec_7_2(t *testing.T) {
	e := newIWChaosEnv(t, true)
	a := e.start(t)
	c := e.dial(t, a)
	done, cancel := e.park(t, c)
	e.proxy.Cut()
	cancel()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("the cancelled lenny/request_input did not return")
	}
	// The exit write runs on a detached context bounded by the primary read
	// timeout and is not retried; give it that bound before Postgres returns.
	time.Sleep(sessionstore.DefaultPrimaryReadTimeout + time.Second)
	e.proxy.Restore(t)
	if row := e.row(t); row.State != session.StateInputRequired {
		t.Fatalf("after the failed exit write the row is %s, want input_required (the write must not have committed)", row.State)
	}
	deadline := time.Now().Add(30 * time.Second)
	for e.row(t).State != session.StateRunning {
		if time.Now().After(deadline) {
			t.Fatalf("the re-armed connection's calls never converged the row; it is %s", e.row(t).State)
		}
		ctx, cancelPing := context.WithTimeout(context.Background(), 5*time.Second)
		_ = inputwaitrig.Ping(ctx, c, iwChaosSession)
		cancelPing()
		time.Sleep(200 * time.Millisecond)
	}
}

// spec: 7.2 (Session state machine), 10.1.1 (Stateless Replicas and
// Per-Session Coordination), 28.3 (Link register)
// diagnosis: a graceful shutdown (SIGTERM) of a replica holding a pending
// lenny/request_input wait left the row input_required. The gateway-control
// shutdown step did not close the input-wait registry before GracefulStop,
// so the wait did not end and take its exit write while Postgres was still
// open, or GracefulStop waited out the blocked handler.
func TestGracefulShutdownDrainsInputWaits_spec_7_2(t *testing.T) {
	e := newIWChaosEnv(t, false)
	a := e.start(t)
	done, _ := e.park(t, e.dial(t, a))
	a.proc.Terminate(t)
	select {
	case res := <-done:
		if res.Err == nil && !res.Resp.GetIsError() {
			t.Errorf("the pending wait answered %+v, want a cancellation", res.Resp)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the pending lenny/request_input did not end on SIGTERM")
	}
	if row := e.row(t); row.State == session.StateInputRequired {
		// The drain's exit write did not land; the next replica's recovery
		// is then the owner, and it must converge the row.
		e.dial(t, e.start(t))
		if row := e.row(t); row.State != session.StateRunning {
			t.Fatalf("after the drain and a new replica's recovery the row is %s/%q, want running", row.State, row.InputWaitID)
		}
		t.Errorf("the drain left the row input_required for the recovery; the exit write must run before the postgres step closes the pool")
	}
}

// expireByMaxSessionAge runs one watchdog pass over the shared
// Postgres store at a time past a one-minute maxSessionAge cap, with every
// other clock out of reach.
func (e *iwChaosEnv) expireByMaxSessionAge(t *testing.T) {
	t.Helper()
	const far = 100 * 24 * 3600
	now := time.Now().UTC().Add(2 * time.Minute)
	w := watchdog.New(e.store, watchdog.StaticTenants{iwChaosTenant}, watchdog.Config{
		MaxSessionAgeSeconds:           60,
		MaxIdleSeconds:                 far,
		MaxAwaitingClientActionSeconds: far,
		MaxSuspendedPodHoldSeconds:     far,
		MaxResumePendingSeconds:        far,
		MaxResumingSeconds:             far,
	}, func() time.Time { return now })
	if _, err := w.Tick(context.Background(), now); err != nil {
		t.Fatalf("watchdog pass: %v", err)
	}
}

// spec: 6.2 (maxSessionAge timer behavior across states), 7.2 (Session state
// machine), 28.3 (Link register)
// diagnosis: a row left input_required on a wait no registry holds, by a
// SIGKILLed waiter or by an exit write lost to a Postgres outage, with no
// later call of its pod to run the recovery, was not expired by the
// maxSessionAge sweep. Either the sweep does not count active age in
// input_required, does not list the row, or does not stamp expired:deadline,
// so the stale row has no owner that ends it.
func TestStaleInputWaitRowEndsAtMaxSessionAge_spec_6_2(t *testing.T) {
	t.Run("waiter killed", func(t *testing.T) {
		e := newIWChaosEnv(t, false)
		a := e.start(t)
		e.park(t, e.dial(t, a))
		a.proc.Kill(t)
		e.assertStaleThenExpired(t)
	})
	t.Run("exit write lost to a Postgres outage", func(t *testing.T) {
		e := newIWChaosEnv(t, true)
		a := e.start(t)
		done, cancel := e.park(t, e.dial(t, a))
		e.proxy.Cut()
		cancel()
		select {
		case <-done:
		case <-time.After(30 * time.Second):
			t.Fatal("the cancelled lenny/request_input did not return")
		}
		time.Sleep(sessionstore.DefaultPrimaryReadTimeout + time.Second)
		e.proxy.Restore(t)
		e.assertStaleThenExpired(t)
	})
}

// assertStaleThenExpired asserts the row stays input_required while no call
// of the pod arrives, then that the maxSessionAge sweep expires it with
// expired:deadline.
func (e *iwChaosEnv) assertStaleThenExpired(t *testing.T) {
	t.Helper()
	time.Sleep(time.Second)
	if row := e.row(t); row.State != session.StateInputRequired {
		t.Fatalf("with no later call the row is %s/%q, want input_required", row.State, row.InputWaitID)
	}
	e.expireByMaxSessionAge(t)
	row := e.row(t)
	if row.State != session.StateExpired || row.FailureReason != string(session.FailureExpiredDeadline) {
		t.Errorf("after the maxSessionAge sweep the row is %s/%q, want expired/%q", row.State, row.FailureReason, session.FailureExpiredDeadline)
	}
}
