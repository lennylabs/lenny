// SPDX-License-Identifier: MIT

package opsserver_test

import (
	"bytes"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/ops/eventsubscription"
)

// auditLogSink adapts a func value to eventsubscription.AuditSink and
// mirrors the cmd/lenny-ops wiring (events_wiring.go), which logs every
// §25.5 subscription audit event's Details map. Details carries only the
// secret fingerprint, never the plaintext secret (see the AuditEvent doc
// comment in pkg/ops/eventsubscription/audit.go), so routing audit
// events through the same sink the access log uses lets this test
// observe whether the plaintext secret ever reaches "any log line" as
// the spec requires.
type auditLogSink struct{}

func (auditLogSink) Emit(ev eventsubscription.AuditEvent) {
	slog.Info("lenny-ops audit", "type", ev.Type, "details", ev.Details)
}

// TestEventSubscriptionCreateAndRotateNeverLogPlaintextSecret covers the
// §25.5 Webhook Secret Lifecycle Storage step.
//
// spec: §25.5 ("Storage. The plaintext secret is never logged (response
// bodies for this endpoint are explicitly redacted in logs) and never
// returned on read endpoints ...")
//
// diagnosis: install a buffer-backed slog handler as the process default
// (the same default the access-log middleware and an audit sink write
// through) and drive the create and rotate-secret handlers directly
// through the server's real handler chain (withCorrelation +
// withAccessLog, wired by opsserver.New). Assert the whsec_ plaintext
// secret each response reveals never appears anywhere in the captured
// log output, while the secretFingerprint the same response carries -
// which the spec explicitly allows into the subscription_created /
// subscription_secret_rotated audit trail - does appear, so the test
// cannot pass merely because nothing was logged at all.
func TestEventSubscriptionCreateAndRotateNeverLogPlaintextSecret(t *testing.T) {
	srv, svc := newServerWithSubs(t)
	svc.SetAuditSink(auditLogSink{})

	var buf bytes.Buffer
	prev := slog.Default()
	defer slog.SetDefault(prev)
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))

	_, createBody := doJSONReq(t, srv, http.MethodPost, "/v1/admin/event-subscriptions", map[string]any{
		"callbackUrl": "https://acme.example/webhook",
		"types":       []string{"dev.lenny.alert_fired"},
	})
	createSecret, _ := createBody["secret"].(string)
	if !strings.HasPrefix(createSecret, "whsec_") {
		t.Fatalf("create did not return a whsec_ secret: %v", createBody)
	}
	createFingerprint, _ := createBody["secretFingerprint"].(string)
	if createFingerprint == "" {
		t.Fatalf("create response missing secretFingerprint: %v", createBody)
	}
	id, _ := createBody["id"].(string)
	if id == "" {
		t.Fatalf("create response missing id: %v", createBody)
	}

	_, rotateBody := doJSONReq(t, srv, http.MethodPost, "/v1/admin/event-subscriptions/"+id+"/rotate-secret", nil)
	rotateSecret, _ := rotateBody["secret"].(string)
	if !strings.HasPrefix(rotateSecret, "whsec_") {
		t.Fatalf("rotate-secret did not return a whsec_ secret: %v", rotateBody)
	}
	rotateFingerprint, _ := rotateBody["secretFingerprint"].(string)
	if rotateFingerprint == "" || rotateFingerprint == createFingerprint {
		t.Fatalf("rotate-secret secretFingerprint = %q, want a fresh value distinct from %q", rotateFingerprint, createFingerprint)
	}

	logOutput := buf.String()
	if strings.Contains(logOutput, createSecret) {
		t.Errorf("create's plaintext secret %q appeared in the server log:\n%s", createSecret, logOutput)
	}
	if strings.Contains(logOutput, rotateSecret) {
		t.Errorf("rotate-secret's plaintext secret %q appeared in the server log:\n%s", rotateSecret, logOutput)
	}
	if !strings.Contains(logOutput, createFingerprint) {
		t.Errorf("create's secretFingerprint %q did not appear in the server log; the audit sink logs the fingerprint", createFingerprint)
	}
	if !strings.Contains(logOutput, rotateFingerprint) {
		t.Errorf("rotate-secret's secretFingerprint %q did not appear in the server log; the audit sink logs the fingerprint", rotateFingerprint)
	}
}
