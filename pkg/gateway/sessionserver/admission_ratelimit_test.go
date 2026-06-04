// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/gateway/podsession"
	rlcounter "github.com/lennylabs/lenny/pkg/gateway/ratelimit"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
)

// spec: §11.1 line 7 — per-runtime and per-pool requests-per-minute
// admission limits, enforced at session creation. F-11.1.2.

// rlRecorder captures the §11.1 rejection-scope and counter-failure
// metrics the admission gate emits.
type rlRecorder struct {
	mu       sync.Mutex
	rejected map[string]int
	failures int
}

func (r *rlRecorder) IncRateLimitRejected(scope string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.rejected == nil {
		r.rejected = map[string]int{}
	}
	r.rejected[scope]++
}

func (r *rlRecorder) IncRateLimitCounterFailure() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.failures++
}

func (r *rlRecorder) snap() (map[string]int, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := make(map[string]int, len(r.rejected))
	for k, v := range r.rejected {
		cp[k] = v
	}
	return cp, r.failures
}

// rlFixedClock pins the per-minute window so every create in a test
// lands in the same window.
func rlFixedClock() time.Time { return time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC) }

// erroringRLCounter always fails so the §11.1 fail-open path is taken.
type erroringRLCounter struct{}

func (erroringRLCounter) Incr(context.Context, string, time.Time) (int, error) {
	return 0, context.DeadlineExceeded
}

// admissionServer builds a session server wired with the §11.1
// per-runtime / per-pool admission gate.
func admissionServer(opts sessionserver.Options) *sessionserver.Server {
	opts.Clock = rlFixedClock
	return sessionserver.New(memstore.New(), opts)
}

func TestPerRuntimeRateLimitRejects_spec_11_1_7(t *testing.T) {
	rec := &rlRecorder{}
	srv := admissionServer(sessionserver.Options{
		AdmissionRateLimitCounter: rlcounter.NewMemory(),
		PerRuntimePerMinute:       2,
		RateLimitMetrics:          rec,
	})
	h := srv.Handler()
	for i := 1; i <= 2; i++ {
		rr := createRequest(t, h, sessionserver.CreateSessionRequest{RuntimeRef: "claude-code"})
		if rr.Code != http.StatusCreated {
			t.Fatalf("create %d under limit: status %d, want 201; body %s", i, rr.Code, rr.Body.String())
		}
	}
	rr := createRequest(t, h, sessionserver.CreateSessionRequest{RuntimeRef: "claude-code"})
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("3rd create: status %d, want 429; body %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "RATE_LIMITED") {
		t.Errorf("rejection should carry RATE_LIMITED: %s", rr.Body.String())
	}
	if rr.Header().Get("Retry-After") == "" {
		t.Error("a 429 admission rejection must carry a Retry-After header")
	}
	rejected, _ := rec.snap()
	if rejected["runtime"] != 1 {
		t.Errorf("runtime-scope rejection counter = %d, want 1", rejected["runtime"])
	}
}

func TestPerRuntimeRateLimitIsPerRuntime_spec_11_1_7(t *testing.T) {
	srv := admissionServer(sessionserver.Options{
		AdmissionRateLimitCounter: rlcounter.NewMemory(),
		PerRuntimePerMinute:       1,
	})
	h := srv.Handler()
	// claude-code exhausts its allowance.
	if rr := createRequest(t, h, sessionserver.CreateSessionRequest{RuntimeRef: "claude-code"}); rr.Code != http.StatusCreated {
		t.Fatalf("claude-code 1st create: status %d, want 201", rr.Code)
	}
	if rr := createRequest(t, h, sessionserver.CreateSessionRequest{RuntimeRef: "claude-code"}); rr.Code != http.StatusTooManyRequests {
		t.Fatalf("claude-code 2nd create: status %d, want 429", rr.Code)
	}
	// A different runtime is unaffected.
	if rr := createRequest(t, h, sessionserver.CreateSessionRequest{RuntimeRef: "codex"}); rr.Code != http.StatusCreated {
		t.Errorf("codex create: status %d, want 201 (per-runtime limits are isolated)", rr.Code)
	}
}

func TestPerRuntimeRateLimitFailsOpen_spec_11_1_7(t *testing.T) {
	rec := &rlRecorder{}
	srv := admissionServer(sessionserver.Options{
		AdmissionRateLimitCounter: erroringRLCounter{},
		PerRuntimePerMinute:       1,
		RateLimitMetrics:          rec,
	})
	h := srv.Handler()
	// §11.1 fail-open: a counter outage must not block admission.
	for i := 1; i <= 5; i++ {
		rr := createRequest(t, h, sessionserver.CreateSessionRequest{RuntimeRef: "claude-code"})
		if rr.Code != http.StatusCreated {
			t.Fatalf("create %d during counter outage: status %d, want 201 (fail open); body %s", i, rr.Code, rr.Body.String())
		}
	}
	if _, failures := rec.snap(); failures == 0 {
		t.Error("a counter outage must bump the counter-failure metric")
	}
}

func TestAdmissionRateLimitNilCounterNoLimit_spec_11_1_7(t *testing.T) {
	// No counter wired: the per-runtime / per-pool scopes are disabled
	// even when a limit is configured.
	srv := admissionServer(sessionserver.Options{PerRuntimePerMinute: 1, PerPoolPerMinute: 1})
	h := srv.Handler()
	for i := 1; i <= 4; i++ {
		rr := createRequest(t, h, sessionserver.CreateSessionRequest{RuntimeRef: "claude-code"})
		if rr.Code != http.StatusCreated {
			t.Fatalf("create %d with no counter: status %d, want 201", i, rr.Code)
		}
	}
}

// spec: §11.1 line 7 — an empty runtimeRef is left to the required-field
// check (the gate skips it rather than consuming a rate-limit slot for a
// request that will be rejected as VALIDATION_ERROR). F-11.1.2.
func TestAdmissionRateLimitSkipsEmptyRuntime_spec_11_1_7(t *testing.T) {
	srv := admissionServer(sessionserver.Options{
		AdmissionRateLimitCounter: rlcounter.NewMemory(),
		PerRuntimePerMinute:       1,
	})
	h := srv.Handler()
	rr := createRequest(t, h, sessionserver.CreateSessionRequest{})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("empty runtimeRef: status %d, want 400 VALIDATION_ERROR; body %s", rr.Code, rr.Body.String())
	}
}

// spec: §11.1 line 7 — the per-pool scope is enforced against the
// resolved warm pool, keyed independently of the per-runtime scope.
// F-11.1.2.
func TestPerPoolRateLimitRejects_spec_11_1_7(t *testing.T) {
	const ns = "lenny-agents"
	s := runtime.NewScheme()
	if err := lennyv1.AddToScheme(s); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}
	pool := &lennyv1.SandboxWarmPool{
		ObjectMeta: metav1.ObjectMeta{Name: "claude-pool", Namespace: ns},
		Spec:       lennyv1.SandboxWarmPoolSpec{TemplateRef: "claude-tmpl", MinWarm: 1, MaxWarm: 5},
	}
	tmpl := &lennyv1.SandboxTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: "claude-tmpl", Namespace: ns},
		Spec:       lennyv1.SandboxTemplateSpec{RuntimeRef: "claude-code", IsolationProfile: "sandboxed"},
	}
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(pool, tmpl).Build()
	binder := &podsession.Binder{Client: c, Namespace: ns}

	rec := &rlRecorder{}
	srv := admissionServer(sessionserver.Options{
		AdmissionRateLimitCounter: rlcounter.NewMemory(),
		PerPoolPerMinute:          2,
		PodBinder:                 binder,
		AgentNamespace:            ns,
		RateLimitMetrics:          rec,
	})
	h := srv.Handler()
	for i := 1; i <= 2; i++ {
		rr := createRequest(t, h, sessionserver.CreateSessionRequest{RuntimeRef: "claude-code"})
		if rr.Code != http.StatusCreated {
			t.Fatalf("create %d under pool limit: status %d, want 201; body %s", i, rr.Code, rr.Body.String())
		}
	}
	rr := createRequest(t, h, sessionserver.CreateSessionRequest{RuntimeRef: "claude-code"})
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("3rd create: status %d, want 429 (pool limit); body %s", rr.Code, rr.Body.String())
	}
	rejected, _ := rec.snap()
	if rejected["pool"] != 1 {
		t.Errorf("pool-scope rejection counter = %d, want 1", rejected["pool"])
	}
}

// spec: §11.1 line 7 — when no pool resolver is wired (the Postgres-only
// posture), the per-pool scope is skipped rather than blocking
// admission. F-11.1.2.
func TestPerPoolRateLimitSkippedWithoutBinder_spec_11_1_7(t *testing.T) {
	srv := admissionServer(sessionserver.Options{
		AdmissionRateLimitCounter: rlcounter.NewMemory(),
		PerPoolPerMinute:          1, // would reject the 2nd create if a pool resolved
	})
	h := srv.Handler()
	for i := 1; i <= 3; i++ {
		rr := createRequest(t, h, sessionserver.CreateSessionRequest{RuntimeRef: "claude-code"})
		if rr.Code != http.StatusCreated {
			t.Fatalf("create %d with no pool resolver: status %d, want 201 (per-pool skipped); body %s", i, rr.Code, rr.Body.String())
		}
	}
}
