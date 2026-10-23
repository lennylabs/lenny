// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/adapter"
	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

// TestServiceModeSuspendedSessionResumesToRunning_spec_7_3 asserts that a
// suspended service-mode session, which holds no pod, is answered
// resume_pending by POST /resume and that the resume driver then commits it
// to running: the claimless restore runs the driver's post-claim hook before
// it returns, so the resuming incarnation exists before the commit. The
// attempt counts one success and no failure, and the row's coordination and
// recovery generations each advance by one.
//
// spec: §7.3 (Resume driver; Retry and Resume), §5.2 (Pool Configuration and
// Execution Modes), §16.1 (Metrics)
func TestServiceModeSuspendedSessionResumesToRunning_spec_7_3(t *testing.T) {
	t.Parallel()
	adapterSrv := adapter.New("adapter-test")
	adapterSrv.WorkspaceBase = t.TempDir()
	adapterSrv.Runtime = &podBindRuntime{}
	cluster := podBindClient(
		t,
		podBindWarmPool("svc-pool", "svc-tmpl"),
		podBindServiceTemplate("svc-tmpl", "svc-runtime", string(isolation.ProfileSandboxed)),
		podBindIdleSandbox("svc-sbx-1", "svc-pool", "10.244.3.9"),
	)
	counter := &attemptCounter{}
	store := memstore.New()
	srv := sessionserver.New(store, sessionserver.Options{
		DevTenantHeader:         true,
		IDFunc:                  func() string { return "sess-svc-1" },
		DefaultIsolationProfile: isolation.ProfileSandboxed,
		PodBinder:               podBindBinder(cluster, podBindAdapterDialer(t, adapterSrv)),
		PodRegistry:             podsession.NewRegistry(),
		AgentNamespace:          podTestNS,
		Pools:                   podBindServicePool(t, "svc-pool", "svc-runtime", 8),
		IncSessionResumeAttempt: counter.record,
	})
	t.Cleanup(srv.Close)

	body, _ := json.Marshal(sessionserver.CreateAndStartRequest{RuntimeRef: "svc-runtime", UserID: "alice@acme.com"})
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/start", bytes.NewReader(body))
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("service-mode start: status = %d, want 201; body=%s", rr.Code, rr.Body.String())
	}
	before, err := store.Update(context.Background(), "acme", "sess-svc-1", func(r *sessionstore.Session) error {
		r.State = session.StateSuspended
		return nil
	})
	if err != nil {
		t.Fatalf("suspend: %v", err)
	}
	if before.PodAssignment != "" {
		t.Fatalf("service-mode row names pod %q, want none", before.PodAssignment)
	}

	rr = resumeAndDrive(t, srv, "sess-svc-1")
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), string(session.StateResumePending)) {
		t.Fatalf("POST /resume = %d %s, want 200 resume_pending", rr.Code, rr.Body.String())
	}
	got, err := store.Get(context.Background(), "acme", "sess-svc-1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.State != session.StateRunning {
		t.Fatalf("state after the resume driver = %s, want running", got.State)
	}
	if got.CoordinationGeneration != before.CoordinationGeneration+1 {
		t.Errorf("coordination generation = %d, want %d (one resuming incarnation)",
			got.CoordinationGeneration, before.CoordinationGeneration+1)
	}
	if got.RecoveryGeneration != before.RecoveryGeneration+1 {
		t.Errorf("recovery generation = %d, want %d (one commit)", got.RecoveryGeneration, before.RecoveryGeneration+1)
	}
	entries := counter.list()
	if len(entries) != 1 || !strings.HasSuffix(entries[0], ":success") {
		t.Errorf("resume attempts recorded %v, want exactly one success", entries)
	}
}
