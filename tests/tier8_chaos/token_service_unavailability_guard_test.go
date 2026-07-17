// SPDX-License-Identifier: MIT

//go:build chaos

// Tier-8 chaos test: the §4.9 Token Service unavailability guard under a
// sustained Token Service circuit-breaker outage. The guard holds a still-
// valid lease alive across a transient outage by extending its enforced
// deadline once per renewal sweep, resumes normal renewal when the breaker
// recovers, and — under a prolonged outage that exceeds one leaseTTLSeconds of
// cumulative extension — terminates the session at the cap into the §8.8
// expired state (surfaced to clients as a failed task with the expired:lease
// error code) rather than re-minting against the still-open breaker, deleting
// the credential file at the capped deadline so the key does not outlive the
// capped lease.
//
// The breaker-open interval is injected through a scripted credrenewal.Renewer
// that returns credrenewal.ErrRenewInfraUnavailable (the sentinel the gateway
// maps a breaker-open credassign.ErrTokenServiceUnavailable to) for the outage
// window. The cap case drives the real adapter.Server over a real gRPC
// connection so the credential-file deletion at the capped deadline is
// observed on the real enforcement point.
//
// spec: §4.9 line 1470 (Token Service unavailability guard; keeps the session
// alive until recovery; cumulative extension capped at one leaseTTLSeconds;
// terminal teardown at the cap); §8.8 (expired:lease surfacing).
package tier8_chaos_test

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	"github.com/lennylabs/lenny/pkg/adapter"
	"github.com/lennylabs/lenny/pkg/adapter/credfile"
	sessionapi "github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credleasestore"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credrenewal"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/adapterclient"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

const guardProvider = "anthropic_direct"

// guardDirectPayload is a direct-mode lease payload; a direct-mode lease with a
// positive expiry arms the adapter expiry timer the guard re-arms.
// spec: §4.9 line 1149.
const guardDirectPayload = `{"deliveryMode":"direct","materializedConfig":{"apiKey":"sk-ant-x"}}`

// scriptedRenewer injects a Token Service breaker-open outage window into the
// renewal worker: while breakerOpen is set it returns the credrenewal breaker-
// open sentinel (the value credRenewalWiring.Renew maps a breaker-open
// credassign.ErrTokenServiceUnavailable to), otherwise it returns freshLease as
// a successful renewal. It records how many times it was asked to renew so a
// test can prove the worker stops asking once the lease is dropped.
type scriptedRenewer struct {
	mu          sync.Mutex
	breakerOpen bool
	freshLease  credrenewal.Lease
	renewCalls  int
}

func (r *scriptedRenewer) setBreakerOpen(open bool) {
	r.mu.Lock()
	r.breakerOpen = open
	r.mu.Unlock()
}

func (r *scriptedRenewer) calls() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.renewCalls
}

func (r *scriptedRenewer) Renew(_ context.Context, _ credrenewal.Lease) (credrenewal.Lease, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.renewCalls++
	if r.breakerOpen {
		return credrenewal.Lease{}, fmt.Errorf("%w: token service breaker open", credrenewal.ErrRenewInfraUnavailable)
	}
	return r.freshLease, nil
}

// TestGuardBridgesTransientOutageThenRenews proves the guard bridges a Token
// Service outage that spans several renewal sweeps: the still-valid lease is
// extended once per sweep (its enforced deadline advancing by one
// renewBeforeBuffer each time), never exhausted, and once the breaker recovers
// the next sweep renews normally onto a fresh credential with no lingering
// extension state, so the reset budget is available again.
//
// spec: §4.9 (line 1470, keeps the session alive until the Token Service
// recovers)
//
// diagnosis: the guard did not bridge the outage (a still-valid session was
// exhausted mid-outage) or did not resume normal renewal on recovery. The
// worker exhausted the lease on a breaker-open sweep, or the extension count
// did not reset after a successful renewal.
func TestGuardBridgesTransientOutageThenRenews_spec_4_9(t *testing.T) {
	ctx := context.Background()
	base := time.Unix(1_700_000_000, 0).UTC()
	buffer := time.Minute

	fresh := credrenewal.Lease{
		LeaseID:     "cl-fresh",
		SessionID:   "run-bridge",
		ExpiresAt:   base.Add(30 * time.Minute),
		RenewBefore: base.Add(29 * time.Minute),
		LeaseTTL:    time.Hour,
	}
	renewer := &scriptedRenewer{breakerOpen: true, freshLease: fresh}

	var extendDeadlines []time.Time
	var mu sync.Mutex
	var exhausted, renewedCount int
	w := credrenewal.New(renewer, credrenewal.Options{
		OnExtend: func(_ credrenewal.Lease, newExpiresAt time.Time) error {
			mu.Lock()
			extendDeadlines = append(extendDeadlines, newExpiresAt)
			mu.Unlock()
			return nil
		},
		OnExhausted:           func(credrenewal.Lease) { mu.Lock(); exhausted++; mu.Unlock() },
		OnRenewed:             func(credrenewal.Lease) { mu.Lock(); renewedCount++; mu.Unlock() },
		OnExtensionCapReached: func(credrenewal.Lease) {},
	})

	origExpiry := base.Add(5 * time.Minute)
	w.Track(credrenewal.Lease{
		LeaseID:     "cl-bridge",
		SessionID:   "run-bridge",
		ExpiresAt:   origExpiry,
		RenewBefore: origExpiry.Add(-buffer),
		LeaseTTL:    time.Hour, // large TTL: this outage never reaches the cap
	})

	// Three breaker-open sweeps, each due one buffer after the last. Each must
	// extend (not exhaust) and advance the enforced deadline by one buffer.
	const outageSweeps = 3
	for i := 0; i < outageSweeps; i++ {
		sweepNow := origExpiry.Add(-buffer).Add(time.Duration(i) * buffer)
		if renewed := w.Tick(ctx, sweepNow); renewed != 0 {
			t.Fatalf("sweep %d reported %d renewals under a breaker-open outage, want 0", i, renewed)
		}
	}

	mu.Lock()
	if exhausted != 0 {
		mu.Unlock()
		t.Fatal("the guard exhausted a still-valid lease during the outage (the restart loop §4.9 forbids)")
	}
	if len(extendDeadlines) != outageSweeps {
		gotN := len(extendDeadlines)
		mu.Unlock()
		t.Fatalf("OnExtend fired %d times across %d outage sweeps, want one extension per sweep", gotN, outageSweeps)
	}
	// Each extension advances the enforced deadline by exactly one buffer.
	for i := 1; i < len(extendDeadlines); i++ {
		if got := extendDeadlines[i].Sub(extendDeadlines[i-1]); got != buffer {
			d := extendDeadlines
			mu.Unlock()
			t.Fatalf("extension %d advanced the deadline by %s, want one buffer (%s); deadlines=%v", i, got, buffer, d)
		}
	}
	if want := origExpiry.Add(buffer); !extendDeadlines[0].Equal(want) {
		d0 := extendDeadlines[0]
		mu.Unlock()
		t.Fatalf("first extension deadline = %v, want origExpiry+buffer %v", d0, want)
	}
	mu.Unlock()
	if w.Tracked() != 1 {
		t.Fatalf("worker tracks %d leases across the outage, want 1 (held, not dropped)", w.Tracked())
	}

	// The breaker recovers: the next sweep, at the extended lease's new
	// renewBefore (still before its extended expiry), renews normally onto the
	// fresh lease.
	renewer.setBreakerOpen(false)
	recoveryNow := origExpiry.Add((outageSweeps - 1) * buffer)
	if renewed := w.Tick(ctx, recoveryNow); renewed != 1 {
		t.Fatalf("recovery sweep reported %d renewals, want 1 (normal renewal resumes)", renewed)
	}
	mu.Lock()
	if renewedCount != 1 {
		rc := renewedCount
		mu.Unlock()
		t.Fatalf("OnRenewed fired %d times on recovery, want 1", rc)
	}
	mu.Unlock()

	// No lingering extension state: the fresh lease starts with a reset
	// extension budget, so a subsequent breaker-open sweep extends it again
	// rather than treating it as already near the cap. Re-open the breaker and
	// drive one more sweep at the fresh lease's renewBefore; it must extend,
	// not exhaust.
	renewer.setBreakerOpen(true)
	if renewed := w.Tick(ctx, fresh.RenewBefore); renewed != 0 {
		t.Fatalf("post-recovery breaker-open sweep reported %d renewals, want 0", renewed)
	}
	mu.Lock()
	defer mu.Unlock()
	if exhausted != 0 {
		t.Fatal("the fresh lease was exhausted on its first breaker-open sweep: the extension count did not reset after renewal")
	}
	if len(extendDeadlines) != outageSweeps+1 {
		t.Fatalf("OnExtend fired %d times total, want %d (the fresh lease extended once more)", len(extendDeadlines), outageSweeps+1)
	}
}

// recordingTerminator captures the (sessionID, reason) the §4.9 cumulative-
// extension-cap teardown routes to the session terminator, so a test can assert
// the reason is the §8.8 expired:lease FailureReason the MCP adapter surfaces
// verbatim as a failed task's error code.
type recordingCapTerminator struct {
	mu       sync.Mutex
	sessions []string
	reasons  []string
}

func (r *recordingCapTerminator) terminate(sessionID, reason string) {
	r.mu.Lock()
	r.sessions = append(r.sessions, sessionID)
	r.reasons = append(r.reasons, reason)
	r.mu.Unlock()
}

func (r *recordingCapTerminator) snapshot() ([]string, []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.sessions...), append([]string(nil), r.reasons...)
}

// serveGuardAdapter serves a real adapter.Server over an in-memory gRPC
// connection and returns a gateway adapter client and the credentials
// directory the direct-mode credential file is written under.
func serveGuardAdapter(t *testing.T) (*adapterclient.Client, string) {
	t.Helper()
	base := t.TempDir()
	credsDir := filepath.Join(base, "run", "lenny")
	if err := os.MkdirAll(credsDir, 0o755); err != nil {
		t.Fatalf("make credentials dir: %v", err)
	}
	s := adapter.New("guard-chaos")
	s.CredentialsDir = credsDir
	s.WorkspaceBase = filepath.Join(base, "workspace")
	s.SessionsRoot = filepath.Join(base, "sessions")
	s.ArtifactsRoot = filepath.Join(base, "artifacts")

	lis := bufconn.Listen(1 << 20)
	gs := adapter.NewGRPCServer(s)
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)

	cl, err := adapterclient.Dial("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial guard adapter: %v", err)
	}
	t.Cleanup(func() { _ = cl.Close() })
	return cl, credsDir
}

// credentialFileHasProvider reports whether the adapter credential file under
// dir still carries an entry for provider.
func credentialFileHasProvider(t *testing.T, dir, provider string) bool {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, credfile.FileName))
	if os.IsNotExist(err) {
		return false
	}
	if err != nil {
		t.Fatalf("read credential file: %v", err)
	}
	return strings.Contains(string(data), provider)
}

// TestGuardProlongedOutageReachesCapAndTerminates proves a Token Service outage
// held open past one leaseTTLSeconds of cumulative extension reaches the §4.9
// cap: the direct-mode lease is extended up to the cap and then the session is
// terminated into the §8.8 expired state with the expired:lease reason (which
// the MCP adapter surfaces as a failed task with the expired:lease error code),
// with no re-mint against the still-open breaker. The adapter expiry timer
// armed at the capped deadline deletes the credential file when it fires, so
// the key does not outlive the capped lease, and no further renewal is
// attempted after the cap.
//
// spec: §4.9 (line 1470, cumulative extension capped at one leaseTTLSeconds;
// terminal teardown at the cap, no re-mint); §8.8 (expired:lease surfacing)
//
// diagnosis: the cap did not bound the key's usable life (a permanently-open
// breaker extended it without limit) or re-entered the Fallback restart loop at
// the cap. The worker extended past maxExtensions, dropped into the Fallback
// Flow instead of the terminal teardown, or the credential file outlived the
// capped deadline.
func TestGuardProlongedOutageReachesCapAndTerminates_spec_4_9(t *testing.T) {
	adapterCli, credsDir := serveGuardAdapter(t)
	ctx := context.Background()

	start := time.Now()
	buffer := 400 * time.Millisecond
	origExpiry := start.Add(buffer)
	// LeaseTTL = 2*buffer, so maxExtensions = 2: two extensions are allowed,
	// and the third breaker-open sweep reaches the cap.
	leaseTTL := 2 * buffer

	if err := adapterCli.AssignCredentials(ctx, "run-cap", map[string]*adapterv1.CredentialLease{
		guardProvider: {
			LeaseId:         "cl-cap",
			Provider:        guardProvider,
			Payload:         []byte(guardDirectPayload),
			ExpiresAtUnixMs: origExpiry.UnixMilli(),
		},
	}); err != nil {
		t.Fatalf("assign direct lease to adapter: %v", err)
	}

	leases := credleasestore.New()
	if err := leases.Put(credential.Lease{
		LeaseID:      "cl-cap",
		SessionID:    "run-cap",
		Provider:     credential.ProviderAnthropicDirect,
		Source:       credential.SourcePool,
		PoolID:       "claude-prod",
		CredentialID: "key-1",
		DeliveryMode: credential.DeliveryDirect,
		IssuedAt:     start.Add(-30 * time.Minute),
		ExpiresAt:    origExpiry,
		RenewBefore:  origExpiry.Add(-buffer),
	}); err != nil {
		t.Fatalf("seed direct store record: %v", err)
	}

	renewer := &scriptedRenewer{breakerOpen: true} // permanently open
	term := &recordingCapTerminator{}
	var extendCount, exhausted, capReached int
	var mu sync.Mutex
	w := credrenewal.New(renewer, credrenewal.Options{
		OnExtend: func(lease credrenewal.Lease, newExpiresAt time.Time) error {
			mu.Lock()
			extendCount++
			mu.Unlock()
			// Direct-mode enforcement point: re-arm the adapter expiry timer.
			rc, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			return adapterCli.ExtendCredentialLease(rc, lease.SessionID, guardProvider, lease.LeaseID, newExpiresAt)
		},
		OnExhausted: func(credrenewal.Lease) { mu.Lock(); exhausted++; mu.Unlock() },
		OnExtensionCapReached: func(lease credrenewal.Lease) {
			mu.Lock()
			capReached++
			mu.Unlock()
			// The gateway routes the capped session to the terminal expired
			// teardown with the existing expired:lease §8.8 FailureReason.
			term.terminate(lease.SessionID, string(sessionapi.FailureExpiredLease))
		},
	})
	w.Track(credrenewal.Lease{
		LeaseID:     "cl-cap",
		SessionID:   "run-cap",
		ExpiresAt:   origExpiry,
		RenewBefore: origExpiry.Add(-buffer),
		LeaseTTL:    leaseTTL,
	})

	// Drive the sweeps with logical times; the newExpiresAt each extension
	// passes the adapter is a real future instant, so the re-armed adapter
	// timer fires in real wall-clock time at the capped deadline.
	// Sweep 1 at start, sweep 2 at start+buffer, sweep 3 (the cap) at start+2buffer.
	for i := 0; i < 3; i++ {
		sweepNow := start.Add(time.Duration(i) * buffer)
		w.Tick(ctx, sweepNow)
	}

	mu.Lock()
	if extendCount != 2 {
		ec := extendCount
		mu.Unlock()
		t.Fatalf("OnExtend fired %d times, want exactly 2 (maxExtensions for TTL=2*buffer)", ec)
	}
	if capReached != 1 {
		cr := capReached
		mu.Unlock()
		t.Fatalf("OnExtensionCapReached fired %d times, want exactly 1", cr)
	}
	if exhausted != 0 {
		mu.Unlock()
		t.Fatal("the cap dropped the lease into the Fallback Flow (OnExhausted fired): it must terminate without re-minting")
	}
	mu.Unlock()

	if w.Tracked() != 0 {
		t.Fatalf("worker tracks %d leases after the cap, want 0 (lease dropped at the cap)", w.Tracked())
	}

	sessions, reasons := term.snapshot()
	if len(sessions) != 1 || sessions[0] != "run-cap" {
		t.Fatalf("cap teardown terminated sessions=%v, want exactly [run-cap]", sessions)
	}
	if reasons[0] != string(sessionapi.FailureExpiredLease) {
		t.Fatalf("cap teardown reason = %q, want the §8.8 FailureExpiredLease reason", reasons[0])
	}
	if reasons[0] != "expired:lease" {
		t.Fatalf("cap teardown reason = %q, want the §8.8 expired:* prefixed reason expired:lease", reasons[0])
	}

	// No re-mint after the cap: a further sweep asks the renewer for nothing,
	// because the lease was dropped. Record the renewer call count now, drive
	// another sweep, and assert it did not grow.
	callsAfterCap := renewer.calls()
	w.Tick(ctx, start.Add(3*buffer))
	if renewer.calls() != callsAfterCap {
		t.Fatalf("the worker asked the renewer to re-mint after the cap (%d -> %d calls): the cap must not re-enter the Fallback Flow", callsAfterCap, renewer.calls())
	}

	// The credential file survived past the original expiry (the extension
	// moved the enforced deadline), then the adapter timer armed at the capped
	// deadline (origExpiry + 2*buffer) deletes the entry when it fires.
	if !credentialFileHasProvider(t, credsDir, guardProvider) {
		t.Fatal("credential file lost the provider entry before the capped deadline: the extension did not move the enforced deadline")
	}
	cappedDeadline := origExpiry.Add(2 * buffer)
	waitDeadline := cappedDeadline.Add(2 * time.Second)
	for time.Now().Before(waitDeadline) {
		if !credentialFileHasProvider(t, credsDir, guardProvider) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if credentialFileHasProvider(t, credsDir, guardProvider) {
		t.Fatal("credential file still carries the provider entry past the capped deadline: the key outlived the capped lease")
	}
}
