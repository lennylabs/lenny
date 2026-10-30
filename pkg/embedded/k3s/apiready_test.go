// SPDX-License-Identifier: MIT

package k3s

import (
	"context"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

// The tests in this file pin the API server readiness wait the launchers
// run after the admin kubeconfig exists. k3s writes the kubeconfig before
// its API server serves and answers 503 "starting" or "apiserver not ready"
// in the interval, so Start must not return on the kubeconfig's presence.
// The fake API server is an httptest TLS server that scripts the k3s
// supervisor's answers.

// fastAPIReadyPoll shortens the probe interval for the test's duration.
func fastAPIReadyPoll(t *testing.T) {
	t.Helper()
	prev := apiReadyPollInterval
	apiReadyPollInterval = 5 * time.Millisecond
	t.Cleanup(func() { apiReadyPollInterval = prev })
}

// writeTestKubeconfig writes a kubeconfig naming srv, trusting its
// certificate, and authenticating with token.
func writeTestKubeconfig(t *testing.T, srv *httptest.Server, token string) string {
	t.Helper()
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
	cfg := clientcmdapi.NewConfig()
	cfg.Clusters["k3s"] = &clientcmdapi.Cluster{Server: srv.URL, CertificateAuthorityData: caPEM}
	cfg.AuthInfos["admin"] = &clientcmdapi.AuthInfo{Token: token}
	cfg.Contexts["k3s"] = &clientcmdapi.Context{Cluster: "k3s", AuthInfo: "admin"}
	cfg.CurrentContext = "k3s"
	path := filepath.Join(t.TempDir(), "kubeconfig.yaml")
	if err := clientcmd.WriteToFile(*cfg, path); err != nil {
		t.Fatalf("write kubeconfig: %v", err)
	}
	return path
}

// scriptedReadyz serves /readyz from a script of (status, body) answers,
// repeating the last answer once the script is exhausted. It records the
// probe count and whether every probe carried the kubeconfig's token.
type scriptedReadyz struct {
	answers  []readyzAnswer
	probes   atomic.Int32
	badAuth  atomic.Int32
	otherURL atomic.Int32
}

type readyzAnswer struct {
	status int
	body   string
}

func (s *scriptedReadyz) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/readyz" {
		s.otherURL.Add(1)
		http.NotFound(w, r)
		return
	}
	if r.Header.Get("Authorization") != "Bearer test-token" {
		s.badAuth.Add(1)
	}
	n := int(s.probes.Add(1)) - 1
	if n >= len(s.answers) {
		n = len(s.answers) - 1
	}
	w.WriteHeader(s.answers[n].status)
	_, _ = w.Write([]byte(s.answers[n].body))
}

// spec: §17.4 (lenny up installs the CRDs against the embedded k3s once it
// is up). The wait keeps probing through the k3s supervisor's 503
// "starting" and "apiserver not ready" answers and returns once /readyz
// answers 200, authenticating with the kubeconfig's credentials.
func TestWaitAPIServerReadyRetriesThroughK3sStartup_spec_17_4(t *testing.T) {
	fastAPIReadyPoll(t)
	h := &scriptedReadyz{answers: []readyzAnswer{
		{http.StatusServiceUnavailable, "starting"},
		{http.StatusServiceUnavailable, "apiserver not ready"},
		{http.StatusServiceUnavailable, "apiserver not ready"},
		{http.StatusOK, "ok"},
	}}
	srv := httptest.NewTLSServer(h)
	defer srv.Close()
	path := writeTestKubeconfig(t, srv, "test-token")

	if err := waitAPIServerReady(context.Background(), path, time.Now().Add(10*time.Second)); err != nil {
		t.Fatalf("waitAPIServerReady = %v, want nil once /readyz answers 200", err)
	}
	if got := h.probes.Load(); got != 4 {
		t.Errorf("probes = %d, want 4 (three 503 answers, then 200)", got)
	}
	if got := h.badAuth.Load(); got != 0 {
		t.Errorf("%d probes lacked the kubeconfig bearer token", got)
	}
	if got := h.otherURL.Load(); got != 0 {
		t.Errorf("%d requests went to a path other than /readyz", got)
	}
}

// spec: §17.4. A server that is ready on the first probe returns at once,
// with a single probe.
func TestWaitAPIServerReadyImmediate_spec_17_4(t *testing.T) {
	fastAPIReadyPoll(t)
	h := &scriptedReadyz{answers: []readyzAnswer{{http.StatusOK, "ok"}}}
	srv := httptest.NewTLSServer(h)
	defer srv.Close()
	path := writeTestKubeconfig(t, srv, "test-token")

	if err := waitAPIServerReady(context.Background(), path, time.Now().Add(10*time.Second)); err != nil {
		t.Fatalf("waitAPIServerReady = %v, want nil", err)
	}
	if got := h.probes.Load(); got != 1 {
		t.Errorf("probes = %d, want 1", got)
	}
}

// spec: §17.4 (the readiness wait is bounded by ReadyTimeout). A server
// that never becomes ready makes the wait return at the deadline with an
// error that carries the last answer, so the operator sees the k3s
// supervisor's reason.
func TestWaitAPIServerReadyTimesOutWithLastAnswer_spec_17_4(t *testing.T) {
	fastAPIReadyPoll(t)
	h := &scriptedReadyz{answers: []readyzAnswer{{http.StatusServiceUnavailable, "apiserver not ready"}}}
	srv := httptest.NewTLSServer(h)
	defer srv.Close()
	path := writeTestKubeconfig(t, srv, "test-token")

	start := time.Now()
	err := waitAPIServerReady(context.Background(), path, start.Add(100*time.Millisecond))
	if err == nil {
		t.Fatal("waitAPIServerReady against a never-ready server = nil, want a deadline error")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("wait ran %v past a 100ms deadline", elapsed)
	}
	for _, want := range []string{"not ready", "HTTP 503", "apiserver not ready"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not contain %q", err, want)
		}
	}
	if h.probes.Load() < 2 {
		t.Errorf("probes = %d, want the wait to retry before the deadline", h.probes.Load())
	}
}

// spec: §17.4. A deadline already in the past still makes exactly one
// probe, so a ready server is not reported unready because the kubeconfig
// wait used up the budget.
func TestWaitAPIServerReadyPastDeadlineProbesOnce_spec_17_4(t *testing.T) {
	fastAPIReadyPoll(t)
	h := &scriptedReadyz{answers: []readyzAnswer{{http.StatusOK, "ok"}}}
	srv := httptest.NewTLSServer(h)
	defer srv.Close()
	path := writeTestKubeconfig(t, srv, "test-token")

	if err := waitAPIServerReady(context.Background(), path, time.Now().Add(-time.Second)); err != nil {
		t.Fatalf("waitAPIServerReady with a past deadline against a ready server = %v, want nil", err)
	}
}

// spec: §17.4. Cancelling the context ends the wait with the context's
// error rather than running to the deadline.
func TestWaitAPIServerReadyHonorsCancellation_spec_17_4(t *testing.T) {
	fastAPIReadyPoll(t)
	h := &scriptedReadyz{answers: []readyzAnswer{{http.StatusServiceUnavailable, "starting"}}}
	srv := httptest.NewTLSServer(h)
	defer srv.Close()
	path := writeTestKubeconfig(t, srv, "test-token")

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	err := waitAPIServerReady(ctx, path, time.Now().Add(time.Minute))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("waitAPIServerReady after cancel = %v, want context.Canceled", err)
	}
}

// spec: §17.4. Failures that are normal while k3s bootstraps (the
// kubeconfig not yet readable, the published port refusing connections)
// are retried rather than returned at once; the deadline error names the
// last one.
func TestWaitAPIServerReadyRetriesTransportFailures_spec_17_4(t *testing.T) {
	fastAPIReadyPoll(t)

	t.Run("kubeconfig absent", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "absent.yaml")
		err := waitAPIServerReady(context.Background(), path, time.Now().Add(50*time.Millisecond))
		if err == nil || !strings.Contains(err.Error(), "load kubeconfig") {
			t.Fatalf("waitAPIServerReady with no kubeconfig = %v, want a deadline error naming the kubeconfig load", err)
		}
	})

	t.Run("connection refused", func(t *testing.T) {
		h := &scriptedReadyz{answers: []readyzAnswer{{http.StatusOK, "ok"}}}
		srv := httptest.NewTLSServer(h)
		path := writeTestKubeconfig(t, srv, "test-token")
		srv.Close()
		err := waitAPIServerReady(context.Background(), path, time.Now().Add(50*time.Millisecond))
		if err == nil || !strings.Contains(err.Error(), "GET /readyz") {
			t.Fatalf("waitAPIServerReady against a closed server = %v, want a deadline error naming the probe", err)
		}
	})

	t.Run("kubeconfig appears during the wait", func(t *testing.T) {
		h := &scriptedReadyz{answers: []readyzAnswer{{http.StatusOK, "ok"}}}
		srv := httptest.NewTLSServer(h)
		defer srv.Close()
		src := writeTestKubeconfig(t, srv, "test-token")
		raw, err := os.ReadFile(src)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(t.TempDir(), "late.yaml")
		go func() {
			time.Sleep(30 * time.Millisecond)
			_ = os.WriteFile(path, raw, 0o600)
		}()
		if err := waitAPIServerReady(context.Background(), path, time.Now().Add(10*time.Second)); err != nil {
			t.Fatalf("waitAPIServerReady with a late kubeconfig = %v, want nil", err)
		}
	})

	t.Run("malformed kubeconfig", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "bad.yaml")
		if err := os.WriteFile(path, []byte("{not yaml"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := waitAPIServerReady(context.Background(), path, time.Now().Add(30*time.Millisecond)); err == nil {
			t.Fatal("waitAPIServerReady with a malformed kubeconfig = nil, want an error")
		}
	})
}
