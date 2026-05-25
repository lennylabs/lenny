// SPDX-License-Identifier: MIT

package adapter

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/adapter/credfile"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// fakeTimer is one armed timer captured by fakeExpiryClock. The test
// fires it explicitly via fire(); Stop records cancellation.
type fakeTimer struct {
	d    time.Duration
	fire func()

	mu      sync.Mutex
	stopped bool
}

func (f *fakeTimer) Stop() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	was := !f.stopped
	f.stopped = true
	return was
}

func (f *fakeTimer) isStopped() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.stopped
}

// fakeExpiryClock is the §4.9 line 1149 expiry-timer test seam: it
// records every armed timer and reports a fixed wall clock so a test can
// assert the requested delay and fire the timer deterministically.
type fakeExpiryClock struct {
	mu     sync.Mutex
	cur    time.Time
	timers []*fakeTimer
}

func (c *fakeExpiryClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cur
}

func (c *fakeExpiryClock) After(d time.Duration, fn func()) expiryTimerHandle {
	c.mu.Lock()
	defer c.mu.Unlock()
	ft := &fakeTimer{d: d, fire: fn}
	c.timers = append(c.timers, ft)
	return ft
}

func (c *fakeExpiryClock) armed() []*fakeTimer {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]*fakeTimer(nil), c.timers...)
}

func (c *fakeExpiryClock) last() *fakeTimer {
	ts := c.armed()
	if len(ts) == 0 {
		return nil
	}
	return ts[len(ts)-1]
}

func expiryServer(t *testing.T, clk *fakeExpiryClock) *Server {
	t.Helper()
	s := New("expiry-test")
	s.CredentialsDir = t.TempDir()
	s.ExpiryAfterFunc = clk.After
	s.ExpiryNow = clk.Now
	return s
}

const (
	directPayload = `{"deliveryMode":"direct","materializedConfig":{"apiKey":"sk-ant-x"}}`
	proxyPayload  = `{"deliveryMode":"proxy","materializedConfig":{"proxyUrl":"https://p/v1","proxyDialect":"anthropic","leaseToken":"lt-1"}}`
)

func expiryLease(id, provider, payload string, expiresAt time.Time) *adapterv1.CredentialLease {
	l := &adapterv1.CredentialLease{LeaseId: id, Provider: provider, Payload: []byte(payload)}
	if !expiresAt.IsZero() {
		l.ExpiresAtUnixMs = expiresAt.UnixMilli()
	}
	return l
}

func assignOne(t *testing.T, s *Server, session, provider string, lease *adapterv1.CredentialLease) {
	t.Helper()
	if _, err := s.AssignCredentials(context.Background(), &adapterv1.AssignCredentialsRequest{
		SessionId: &adapterv1.SessionId{Value: session},
		Leases:    map[string]*adapterv1.CredentialLease{provider: lease},
	}); err != nil {
		t.Fatalf("AssignCredentials: %v", err)
	}
}

// fileProviders reads the credential file and returns the set of provider
// names present in it.
func fileProviders(t *testing.T, dir string) map[string]bool {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, credfile.FileName))
	if err != nil {
		t.Fatalf("read credential file: %v", err)
	}
	var doc struct {
		Providers []map[string]any `json:"providers"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("decode credential file: %v", err)
	}
	out := map[string]bool{}
	for _, p := range doc.Providers {
		if name, _ := p["provider"].(string); name != "" {
			out[name] = true
		}
	}
	return out
}

// attachControlStream wires a §4.7 control stream so emitted AUTH_EXPIRED
// events are observable, and returns the stream and a cancel func.
func attachControlStream(t *testing.T, s *Server) (*fakeControlStream, func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	stream := newFakeControlStream(ctx)
	go func() { _ = s.LifecycleChannel(stream) }()
	awaitRegistration(t, s)
	return stream, cancel
}

// spec: §4.9 line 1149 — in direct delivery mode the adapter arms a local
// timer at each lease's expiresAt.
func TestDirectLeaseArmsExpiryTimerAtExpiresAt_spec_4_9(t *testing.T) {
	clk := &fakeExpiryClock{cur: time.Unix(1_700_000_000, 0).UTC()}
	s := expiryServer(t, clk)
	exp := clk.cur.Add(time.Hour)
	assignOne(t, s, "sess-1", "anthropic_direct", expiryLease("l1", "anthropic_direct", directPayload, exp))

	armed := clk.armed()
	if len(armed) != 1 {
		t.Fatalf("armed %d timers, want 1", len(armed))
	}
	if got := armed[0].d; got != time.Hour {
		t.Errorf("timer delay = %s, want 1h", got)
	}
	s.mu.Lock()
	tmr, ok := s.expiryTimers["anthropic_direct"]
	s.mu.Unlock()
	if !ok || tmr.leaseID != "l1" {
		t.Errorf("expiryTimers[anthropic_direct] = %+v, want leaseID l1", tmr)
	}
}

// spec: §4.9 line 1149 — when the timer fires without a replacement, the
// adapter deletes the provider's credential-file entry and reports
// AUTH_EXPIRED on the control channel.
func TestExpiryFireDeletesEntryAndReportsAuthExpired_spec_4_9(t *testing.T) {
	clk := &fakeExpiryClock{cur: time.Unix(1_700_000_000, 0).UTC()}
	s := expiryServer(t, clk)
	stream, cancel := attachControlStream(t, s)
	defer cancel()

	assignOne(t, s, "sess-1", "anthropic_direct",
		expiryLease("l1", "anthropic_direct", directPayload, clk.cur.Add(time.Hour)))
	if !fileProviders(t, s.CredentialsDir)["anthropic_direct"] {
		t.Fatal("credential file missing anthropic_direct entry before expiry")
	}

	clk.last().fire()

	if fileProviders(t, s.CredentialsDir)["anthropic_direct"] {
		t.Error("credential file still carries anthropic_direct after expiry")
	}
	ev := recvEvent(t, stream)
	if ev.Type != eventAuthExpired || ev.Provider != "anthropic_direct" || ev.LeaseID != "l1" {
		t.Errorf("control event = %+v, want AUTH_EXPIRED anthropic_direct l1", ev)
	}
	s.mu.Lock()
	_, stillArmed := s.expiryTimers["anthropic_direct"]
	s.mu.Unlock()
	if stillArmed {
		t.Error("expiry timer still tracked after firing")
	}
}

// spec: §4.9 line 1149 — proxy-mode leases get no adapter timer; the
// gateway enforces proxy-request expiry server-side.
func TestProxyLeaseArmsNoExpiryTimer_spec_4_9(t *testing.T) {
	clk := &fakeExpiryClock{cur: time.Unix(1_700_000_000, 0).UTC()}
	s := expiryServer(t, clk)
	assignOne(t, s, "sess-1", "anthropic_direct",
		expiryLease("l1", "anthropic_direct", proxyPayload, clk.cur.Add(time.Hour)))

	if armed := clk.armed(); len(armed) != 0 {
		t.Fatalf("armed %d timers for a proxy lease, want 0", len(armed))
	}
	s.mu.Lock()
	_, ok := s.expiryTimers["anthropic_direct"]
	s.mu.Unlock()
	if ok {
		t.Error("proxy lease tracked an expiry timer")
	}
}

// spec: §4.9 line 1149 — a direct lease with no expiresAt cannot expire,
// so no timer is armed.
func TestDirectLeaseWithoutExpiryArmsNoTimer_spec_4_9(t *testing.T) {
	clk := &fakeExpiryClock{cur: time.Unix(1_700_000_000, 0).UTC()}
	s := expiryServer(t, clk)
	assignOne(t, s, "sess-1", "anthropic_direct",
		expiryLease("l1", "anthropic_direct", directPayload, time.Time{}))
	if armed := clk.armed(); len(armed) != 0 {
		t.Fatalf("armed %d timers for a no-expiry lease, want 0", len(armed))
	}
}

// spec: §4.9 line 1149 — an already-expired direct lease arms a timer with
// a non-positive delay so it fires immediately (time.AfterFunc semantics).
func TestExpiredDirectLeaseArmsImmediateTimer_spec_4_9(t *testing.T) {
	clk := &fakeExpiryClock{cur: time.Unix(1_700_000_000, 0).UTC()}
	s := expiryServer(t, clk)
	assignOne(t, s, "sess-1", "anthropic_direct",
		expiryLease("l1", "anthropic_direct", directPayload, clk.cur.Add(-time.Minute)))
	last := clk.last()
	if last == nil || last.d > 0 {
		t.Fatalf("armed timer delay = %v, want non-positive", last)
	}
}

// spec: §4.9 line 1149 — "without a replacement lease having been
// delivered": a rotation re-arms the timer for the new lease, and the
// stale timer's late fire is a no-op.
func TestRotationReplacesTimerAndStaleFireIsNoop_spec_4_9(t *testing.T) {
	clk := &fakeExpiryClock{cur: time.Unix(1_700_000_000, 0).UTC()}
	s := expiryServer(t, clk)
	stream, cancel := attachControlStream(t, s)
	defer cancel()

	assignOne(t, s, "sess-1", "anthropic_direct",
		expiryLease("l1", "anthropic_direct", directPayload, clk.cur.Add(time.Hour)))
	staleTimer := clk.last()

	if _, err := s.RotateCredentials(context.Background(), &adapterv1.RotateCredentialsRequest{
		SessionId: &adapterv1.SessionId{Value: "sess-1"},
		Leases: map[string]*adapterv1.CredentialLease{
			"anthropic_direct": expiryLease("l2", "anthropic_direct", directPayload, clk.cur.Add(2*time.Hour)),
		},
	}); err != nil {
		t.Fatalf("RotateCredentials: %v", err)
	}

	if !staleTimer.isStopped() {
		t.Error("stale timer was not stopped on rotation")
	}
	s.mu.Lock()
	cur := s.expiryTimers["anthropic_direct"]
	s.mu.Unlock()
	if cur == nil || cur.leaseID != "l2" {
		t.Fatalf("current timer = %+v, want leaseID l2", cur)
	}

	// A late fire of the stale (l1) timer must not delete the entry or
	// emit AUTH_EXPIRED — the replacement l2 is current.
	staleTimer.fire()
	if !fileProviders(t, s.CredentialsDir)["anthropic_direct"] {
		t.Error("stale timer fire removed the replacement lease's entry")
	}

	// Firing the live (l2) timer expires the current lease.
	clk.last().fire()
	ev := recvEvent(t, stream)
	if ev.Type != eventAuthExpired || ev.LeaseID != "l2" {
		t.Errorf("control event = %+v, want AUTH_EXPIRED l2", ev)
	}
}

// spec: §4.9 line 1149 — revoking a provider cancels its expiry timer so
// it cannot fire after the lease is already gone.
func TestRevokeCancelsExpiryTimer_spec_4_9(t *testing.T) {
	clk := &fakeExpiryClock{cur: time.Unix(1_700_000_000, 0).UTC()}
	s := expiryServer(t, clk)
	assignOne(t, s, "sess-1", "anthropic_direct",
		expiryLease("l1", "anthropic_direct", directPayload, clk.cur.Add(time.Hour)))
	timer := clk.last()

	if _, err := s.RevokeCredentials(context.Background(), &adapterv1.RevokeCredentialsRequest{
		SessionId: &adapterv1.SessionId{Value: "sess-1"},
		Providers: []string{"anthropic_direct"},
	}); err != nil {
		t.Fatalf("RevokeCredentials: %v", err)
	}
	if !timer.isStopped() {
		t.Error("revoke did not stop the expiry timer")
	}
	s.mu.Lock()
	_, ok := s.expiryTimers["anthropic_direct"]
	s.mu.Unlock()
	if ok {
		t.Error("revoked provider still tracks an expiry timer")
	}
}

// spec: §4.9 line 1149 — releasing the pod to idle cancels every armed
// expiry timer so a stale lease cannot fire against a finished session.
func TestReleaseSessionCancelsExpiryTimers_spec_4_9(t *testing.T) {
	clk := &fakeExpiryClock{cur: time.Unix(1_700_000_000, 0).UTC()}
	s := expiryServer(t, clk)
	assignOne(t, s, "sess-1", "anthropic_direct",
		expiryLease("l1", "anthropic_direct", directPayload, clk.cur.Add(time.Hour)))
	timer := clk.last()

	s.releaseSession()

	if !timer.isStopped() {
		t.Error("releaseSession did not stop the expiry timer")
	}
	s.mu.Lock()
	n := len(s.expiryTimers)
	s.mu.Unlock()
	if n != 0 {
		t.Errorf("expiryTimers has %d entries after release, want 0", n)
	}
}

// spec: §4.9 line 1149 — independent per-provider timers; expiring one
// provider leaves the other's lease and timer intact.
func TestPerProviderExpiryIsIndependent_spec_4_9(t *testing.T) {
	clk := &fakeExpiryClock{cur: time.Unix(1_700_000_000, 0).UTC()}
	s := expiryServer(t, clk)
	stream, cancel := attachControlStream(t, s)
	defer cancel()

	if _, err := s.AssignCredentials(context.Background(), &adapterv1.AssignCredentialsRequest{
		SessionId: &adapterv1.SessionId{Value: "sess-1"},
		Leases: map[string]*adapterv1.CredentialLease{
			"anthropic_direct": expiryLease("la", "anthropic_direct", directPayload, clk.cur.Add(time.Hour)),
			"aws_bedrock":      expiryLease("lb", "aws_bedrock", directPayload, clk.cur.Add(2*time.Hour)),
		},
	}); err != nil {
		t.Fatalf("AssignCredentials: %v", err)
	}

	// Fire only the anthropic timer.
	for _, ft := range clk.armed() {
		if ft.d == time.Hour {
			ft.fire()
		}
	}
	ev := recvEvent(t, stream)
	if ev.Provider != "anthropic_direct" || ev.LeaseID != "la" {
		t.Errorf("control event = %+v, want AUTH_EXPIRED anthropic_direct la", ev)
	}

	providers := fileProviders(t, s.CredentialsDir)
	if providers["anthropic_direct"] {
		t.Error("anthropic_direct entry survived its expiry")
	}
	if !providers["aws_bedrock"] {
		t.Error("aws_bedrock entry was removed by anthropic_direct expiry")
	}
	s.mu.Lock()
	_, bedrockArmed := s.expiryTimers["aws_bedrock"]
	s.mu.Unlock()
	if !bedrockArmed {
		t.Error("aws_bedrock timer was cancelled by anthropic_direct expiry")
	}
}

func TestLeaseDeliveryMode(t *testing.T) {
	cases := []struct {
		name    string
		payload string
		want    string
	}{
		{"direct", directPayload, "direct"},
		{"proxy", proxyPayload, "proxy"},
		{"empty payload", "", ""},
		{"malformed json", `{not json`, ""},
		{"missing key", `{"materializedConfig":{}}`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := leaseDeliveryMode(&adapterv1.CredentialLease{Payload: []byte(tc.payload)})
			if got != tc.want {
				t.Errorf("leaseDeliveryMode(%q) = %q, want %q", tc.payload, got, tc.want)
			}
		})
	}
}
