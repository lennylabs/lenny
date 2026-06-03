// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// stubTester is a smokeTester whose run() records invocation and returns a
// canned error, so runSmokeTest's orchestration is tested without a live
// gateway.
type stubTester struct {
	called *bool
	err    error
}

func (s stubTester) run(_ context.Context, _ smokeTarget, _, _ io.Writer) error {
	if s.called != nil {
		*s.called = true
	}
	return s.err
}

// TestRunSmokeTestSkipNoURL asserts a target with no gateway URL is a skip
// (exit 0), not a failure. spec: §24.20 line 299. F-24.20.4.
func TestRunSmokeTestSkipNoURL(t *testing.T) {
	var called bool
	var stdout, stderr bytes.Buffer
	code := runSmokeTest(context.Background(), stubTester{called: &called}, smokeTarget{}, rollbackInfo{}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code: got %d, want 0", code)
	}
	if called {
		t.Error("tester should not run when gatewayURL is empty")
	}
	if !strings.Contains(stdout.String(), "Smoke test: skipped") {
		t.Errorf("stdout should report skip: %s", stdout.String())
	}
}

// TestRunSmokeTestPass asserts a passing tester yields exit 0 and a pass
// line. spec: §24.20 line 299. F-24.20.4.
func TestRunSmokeTestPass(t *testing.T) {
	var called bool
	var stdout, stderr bytes.Buffer
	tgt := smokeTarget{gatewayURL: "https://gw.acme.com", runtime: "chat"}
	code := runSmokeTest(context.Background(), stubTester{called: &called}, tgt, rollbackInfo{}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code: got %d, want 0; stderr=%s", code, stderr.String())
	}
	if !called {
		t.Error("tester should run when gatewayURL is set")
	}
	if !strings.Contains(stdout.String(), "Smoke test: passed") {
		t.Errorf("stdout should report pass: %s", stdout.String())
	}
}

// TestRunSmokeTestFailPrintsRollback asserts a failing tester yields exit 1
// and prints the rollback procedure (helm uninstall). spec: §24.20 line 299
// ("on failure, print the rollback procedure"). F-24.20.4.
func TestRunSmokeTestFailPrintsRollback(t *testing.T) {
	var called bool
	var stdout, stderr bytes.Buffer
	tgt := smokeTarget{gatewayURL: "https://gw.acme.com", runtime: "chat"}
	rb := rollbackInfo{release: "lenny", namespace: "lenny-system"}
	code := runSmokeTest(context.Background(), stubTester{called: &called, err: errors.New("boom")}, tgt, rb, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("exit code: got %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "smoke test failed") {
		t.Errorf("stderr should report failure: %s", stderr.String())
	}
	if !strings.Contains(stderr.String(), "helm uninstall lenny --namespace lenny-system") {
		t.Errorf("stderr should print the rollback command: %s", stderr.String())
	}
}

// TestHTTPSmokeTesterHealthOnly asserts the tester stops after a successful
// /healthz probe when no token is present, reporting the MCP round-trip as
// skipped. spec: §24.20 line 299. F-24.20.4.
func TestHTTPSmokeTesterHealthOnly(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	h := &httpSmokeTester{httpClient: srv.Client()}
	tgt := smokeTarget{gatewayURL: srv.URL, runtime: "chat", healthTimeout: 2 * time.Second, pollInterval: 10 * time.Millisecond}
	var stdout, stderr bytes.Buffer
	if err := h.run(context.Background(), tgt, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(stdout.String(), "healthz is ready") {
		t.Errorf("stdout should report health: %s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "MCP round-trip skipped") {
		t.Errorf("stdout should report MCP skip when no token: %s", stdout.String())
	}
}

// TestHTTPSmokeTesterHealthTimeout asserts a gateway that never reports
// healthy makes the tester fail within the deadline. spec: §24.20 line 299.
// F-24.20.4.
func TestHTTPSmokeTesterHealthTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	h := &httpSmokeTester{httpClient: srv.Client()}
	tgt := smokeTarget{gatewayURL: srv.URL, runtime: "chat", healthTimeout: 60 * time.Millisecond, pollInterval: 10 * time.Millisecond}
	var stdout, stderr bytes.Buffer
	err := h.run(context.Background(), tgt, &stdout, &stderr)
	if err == nil {
		t.Fatal("run should fail when /healthz never returns 2xx")
	}
	if !strings.Contains(err.Error(), "did not become healthy") {
		t.Errorf("error should name the health timeout: %v", err)
	}
}

// TestHTTPSmokeTesterMCPRoundTrip asserts that with a token the tester runs
// the injected session round-trip and surfaces its error wrapped with the
// create_session context. spec: §24.20 line 299. F-24.20.4.
func TestHTTPSmokeTesterMCPRoundTrip(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	t.Run("pass", func(t *testing.T) {
		var ran bool
		h := &httpSmokeTester{
			httpClient: srv.Client(),
			session:    func(context.Context, smokeTarget) error { ran = true; return nil },
		}
		tgt := smokeTarget{gatewayURL: srv.URL, runtime: "chat", token: "tok", healthTimeout: time.Second, pollInterval: 10 * time.Millisecond}
		var stdout, stderr bytes.Buffer
		if err := h.run(context.Background(), tgt, &stdout, &stderr); err != nil {
			t.Fatalf("run: %v", err)
		}
		if !ran {
			t.Error("session hook should run when a token is present")
		}
		if !strings.Contains(stdout.String(), `create_session against "chat" runtime succeeded`) {
			t.Errorf("stdout should report round-trip success: %s", stdout.String())
		}
	})

	t.Run("fail", func(t *testing.T) {
		h := &httpSmokeTester{
			httpClient: srv.Client(),
			session:    func(context.Context, smokeTarget) error { return errors.New("runtime not registered") },
		}
		tgt := smokeTarget{gatewayURL: srv.URL, runtime: "chat", token: "tok", healthTimeout: time.Second, pollInterval: 10 * time.Millisecond}
		var stdout, stderr bytes.Buffer
		err := h.run(context.Background(), tgt, &stdout, &stderr)
		if err == nil {
			t.Fatal("run should fail when the session hook errors")
		}
		if !strings.Contains(err.Error(), "create_session") {
			t.Errorf("error should wrap with create_session context: %v", err)
		}
	})
}

// TestSmokeTargetFromAnswers asserts URL/token resolution from the
// environment and the domain fallback. spec: §24.20 line 299. F-24.20.4.
func TestSmokeTargetFromAnswers(t *testing.T) {
	t.Run("env overrides domain", func(t *testing.T) {
		t.Setenv("LENNY_API_URL", "http://127.0.0.1:8080")
		t.Setenv("LENNY_API_TOKEN", "tok-xyz")
		tgt := smokeTargetFromAnswers(installAnswers{Domain: "gw.acme.com"})
		if tgt.gatewayURL != "http://127.0.0.1:8080" {
			t.Errorf("LENNY_API_URL should win: %q", tgt.gatewayURL)
		}
		if tgt.token != "tok-xyz" {
			t.Errorf("token from env: %q", tgt.token)
		}
		if tgt.runtime != defaultSmokeRuntime {
			t.Errorf("runtime should default to chat: %q", tgt.runtime)
		}
	})

	t.Run("domain fallback", func(t *testing.T) {
		t.Setenv("LENNY_API_URL", "")
		t.Setenv("LENNY_API_TOKEN", "")
		tgt := smokeTargetFromAnswers(installAnswers{Domain: "gw.acme.com"})
		if tgt.gatewayURL != "https://gw.acme.com" {
			t.Errorf("domain should derive https URL: %q", tgt.gatewayURL)
		}
		if tgt.token != "" {
			t.Errorf("token should be empty without env: %q", tgt.token)
		}
	})

	t.Run("no url when neither present", func(t *testing.T) {
		t.Setenv("LENNY_API_URL", "")
		tgt := smokeTargetFromAnswers(installAnswers{})
		if tgt.gatewayURL != "" {
			t.Errorf("gatewayURL should be empty: %q", tgt.gatewayURL)
		}
	})
}
