// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/events"
	"github.com/lennylabs/lenny/pkg/gateway/experimentstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
)

// spec: §10.7 line 833 / §16.1 lines 156-157 — external experiment
// targeting observability. The OFREP evaluation path records the
// lenny_experiment_targeting_duration_seconds histogram and the
// lenny_experiment_targeting_error_total counter through the reporter,
// and emits the §16.6 line 651 experiment.unknown_external_id event.

type targetingResult struct {
	reporter *recordingRejectionReporter
	emitter  *events.Emitter
	store    *memstore.Store
	ofrepURL string
}

// targetingHarness wires a session server with an OFREP endpoint, a
// recording reporter, and an event buffer, then posts one session-create.
func targetingHarness(t *testing.T, ofrepHandler http.HandlerFunc) targetingResult {
	t.Helper()
	ofrepSrv := httptest.NewServer(ofrepHandler)
	t.Cleanup(ofrepSrv.Close)

	exps := experimentstore.NewMemory()
	externalExperiment(t, exps, "exp_ext", "claude-code-v2")
	reporter := &recordingRejectionReporter{}
	emitter := events.NewEmitter(events.NewEventBuffer(0), "targeting-test")
	store := memstore.New()
	srv := sessionserver.New(store, sessionserver.Options{
		Experiments:          exps,
		Tenants:              ofrepTenant(t, ofrepSrv.URL),
		ExperimentRejections: reporter,
		OpsEmitter:           emitter,
		IDFunc:               func() string { return "sess_targeting" },
	})
	rr := postSession(t, srv.Handler(), "/v1/sessions", "alice", "default")
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: status %d, body %s", rr.Code, rr.Body.String())
	}
	return targetingResult{reporter: reporter, emitter: emitter, store: store, ofrepURL: ofrepSrv.URL}
}

func TestExperimentTargetingRecordsDurationWithHostnameProvider_spec_16_1_156(t *testing.T) {
	res := targetingHarness(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"variant":"treatment","reason":"TARGETING_MATCH"}`))
	})
	if len(res.reporter.targetingDurs) != 1 {
		t.Fatalf("targeting duration observations = %d, want 1", len(res.reporter.targetingDurs))
	}
	// §16.1 line 156: for provider:ofrep the metric provider label is the
	// OFREP endpoint hostname, not the literal "ofrep".
	wantHost, _ := url.Parse(res.ofrepURL)
	if got := res.reporter.targetingDurs[0].provider; got != wantHost.Hostname() {
		t.Errorf("provider label = %q, want OFREP hostname %q", got, wantHost.Hostname())
	}
	if res.reporter.targetingDurs[0].seconds < 0 {
		t.Errorf("duration = %v seconds, want >= 0", res.reporter.targetingDurs[0].seconds)
	}
	if len(res.reporter.targetingErrors) != 0 {
		t.Errorf("targeting errors = %d, want 0 on a successful evaluation", len(res.reporter.targetingErrors))
	}
}

func TestExperimentTargetingHTTPErrorClassifiedAsHTTPError_spec_16_1_157(t *testing.T) {
	res := targetingHarness(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{}`))
	})
	if len(res.reporter.targetingErrors) != 1 {
		t.Fatalf("targeting errors = %d, want 1", len(res.reporter.targetingErrors))
	}
	if got := res.reporter.targetingErrors[0].errorType; got != "http_error" {
		t.Errorf("error_type = %q, want http_error", got)
	}
	// §16.1 line 156: latency is recorded for the failed attempt too.
	if len(res.reporter.targetingDurs) != 1 {
		t.Errorf("targeting duration observations = %d, want 1 even on failure", len(res.reporter.targetingDurs))
	}
}

func TestExperimentTargetingProviderErrorCodeUsedAsErrorType_spec_16_1_157(t *testing.T) {
	res := targetingHarness(t, func(w http.ResponseWriter, r *http.Request) {
		// A 200 carrying an OFREP errorCode is a provider-level failure;
		// the errorCode is the bounded error_type label.
		_, _ = w.Write([]byte(`{"errorCode":"FLAG_NOT_FOUND","errorDetails":"no such flag"}`))
	})
	if len(res.reporter.targetingErrors) != 1 {
		t.Fatalf("targeting errors = %d, want 1", len(res.reporter.targetingErrors))
	}
	if got := res.reporter.targetingErrors[0].errorType; got != "FLAG_NOT_FOUND" {
		t.Errorf("error_type = %q, want FLAG_NOT_FOUND", got)
	}
}

func TestExperimentTargetingUnknownExternalIDEmittedAndIgnored_spec_10_7_829(t *testing.T) {
	res := targetingHarness(t, func(w http.ResponseWriter, r *http.Request) {
		// The provider echoes a flag key Lenny never registered.
		_, _ = w.Write([]byte(`{"key":"some_other_flag","variant":"treatment","reason":"TARGETING_MATCH"}`))
	})
	page := res.emitter.Buffer().Query(0, events.EventFilter{
		EventType: string(events.EventExperimentUnknownExternalID),
	}, 0)
	if len(page.Events) != 1 {
		t.Fatalf("unknown_external_id events = %d, want 1", len(page.Events))
	}
	ev := page.Events[0].Event
	if ev.Severity != "info" {
		t.Errorf("severity = %q, want info", ev.Severity)
	}
	var data struct {
		Provider   string `json:"provider"`
		ExternalID string `json:"external_experiment_id"`
	}
	if err := json.Unmarshal(ev.Data, &data); err != nil {
		t.Fatalf("event data: %v", err)
	}
	if data.ExternalID != "some_other_flag" {
		t.Errorf("external_experiment_id = %q, want some_other_flag", data.ExternalID)
	}
	if data.Provider != "ofrep" {
		t.Errorf("provider = %q, want ofrep", data.Provider)
	}
	// §10.7 line 829: an unregistered external experiment is ignored — no
	// targeting error is recorded and the session is not enrolled.
	if len(res.reporter.targetingErrors) != 0 {
		t.Errorf("targeting errors = %d, want 0 — an unknown id is not a targeting failure", len(res.reporter.targetingErrors))
	}
	got, _ := res.store.Get(context.Background(), "default", "sess_targeting")
	if got.ExperimentContext != nil {
		t.Errorf("session enrolled from an unknown external id: %+v", got.ExperimentContext)
	}
}
