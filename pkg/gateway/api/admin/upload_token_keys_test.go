// SPDX-License-Identifier: MIT

package admin_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/api/admin"
	"github.com/lennylabs/lenny/pkg/signingkeys"
	"github.com/lennylabs/lenny/pkg/tenancy/tenantstore"
)

// fakeRotator stands for the upload-token key ring: it reports rot or err
// and counts its calls.
type fakeRotator struct {
	rot   signingkeys.Rotation
	err   error
	calls int
}

func (f *fakeRotator) RotateNow(context.Context) (signingkeys.Rotation, error) {
	f.calls++
	return f.rot, f.err
}

func postRotate(t *testing.T, router *admin.Router, wrap func(*http.Request) *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, wrap(httptest.NewRequest(http.MethodPost, "/v1/admin/upload-token-keys/rotate", nil)))
	return rr
}

// TestForcedUploadTokenKeyRotationWritesTheKeyAndAudits pins the route: a
// platform-admin call rotates once, answers 200 with the key identifier,
// slot, and activation instant of the key written (no secret), and emits
// platform.upload_token_key_rotated with the same values and the caller.
//
// spec: §7.1 (uploadToken format and security properties), §15.1 (REST API)
func TestForcedUploadTokenKeyRotationWritesTheKeyAndAudits(t *testing.T) {
	activeFrom := time.Date(2026, 11, 26, 10, 2, 0, 0, time.UTC)
	rot := &fakeRotator{rot: signingkeys.Rotation{KeyID: "upload-token-8-0a1b2c3d", RotationSlot: 8, ActiveFrom: activeFrom}}
	sink := &capturingAuditSink{}
	router := admin.NewRouter(tenantstore.NewMemory(), admin.Options{Audit: sink}).WithUploadTokenKeyRotator(rot)

	rr := postRotate(t, router, withAdminPrincipal)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["keyId"] != "upload-token-8-0a1b2c3d" || body["rotationSlot"] != float64(8) || body["activeFrom"] != "2026-11-26T10:02:00Z" || len(body) != 3 {
		t.Errorf("body = %v", body)
	}
	if rot.calls != 1 {
		t.Errorf("RotateNow calls = %d, want 1", rot.calls)
	}
	if len(sink.events) != 1 {
		t.Fatalf("audit events = %d, want 1", len(sink.events))
	}
	ev := sink.events[0]
	if ev.Type != "platform.upload_token_key_rotated" || ev.ActorSubject != "admin@acme.com" ||
		ev.Detail["key_id"] != "upload-token-8-0a1b2c3d" || ev.Detail["rotation_slot"] != int64(8) ||
		ev.Detail["active_from"] != "2026-11-26T10:02:00Z" {
		t.Errorf("audit event = %+v", ev)
	}
}

// TestForcedUploadTokenKeyRotationRequiresPlatformAdmin pins the role
// gate: a tenant-admin or a user is refused with 403 and nothing rotates.
//
// spec: §15.1 (REST API)
func TestForcedUploadTokenKeyRotationRequiresPlatformAdmin(t *testing.T) {
	rot := &fakeRotator{}
	router := admin.NewRouter(tenantstore.NewMemory(), admin.Options{}).WithUploadTokenKeyRotator(rot)
	for name, wrap := range map[string]func(*http.Request) *http.Request{
		"tenant-admin": withTenantAdminPrincipal,
		"user":         withUserPrincipal,
	} {
		if rr := postRotate(t, router, wrap); rr.Code != http.StatusForbidden {
			t.Errorf("%s: status %d, want 403", name, rr.Code)
		}
	}
	if rot.calls != 0 {
		t.Errorf("RotateNow calls = %d, want 0", rot.calls)
	}
}

// TestForcedUploadTokenKeyRotationFailureAnswers503AndDoesNotAudit pins the
// failure paths: a store failure and contended slots both answer 503
// SERVICE_UNAVAILABLE with Retry-After and emit no audit event.
//
// spec: §7.1 (uploadToken format and security properties), §15.1 (REST API)
func TestForcedUploadTokenKeyRotationFailureAnswers503AndDoesNotAudit(t *testing.T) {
	for name, err := range map[string]error{
		"the store fails":          errors.New("postgres down"),
		"every slot was contended": signingkeys.ErrRotationContended,
	} {
		t.Run(name, func(t *testing.T) {
			sink := &capturingAuditSink{}
			router := admin.NewRouter(tenantstore.NewMemory(), admin.Options{Audit: sink}).
				WithUploadTokenKeyRotator(&fakeRotator{err: err})
			rr := postRotate(t, router, withAdminPrincipal)
			if rr.Code != http.StatusServiceUnavailable || rr.Header().Get("Retry-After") == "" {
				t.Errorf("status %d, Retry-After %q; want 503 with a retry hint", rr.Code, rr.Header().Get("Retry-After"))
			}
			var env struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			_ = json.Unmarshal(rr.Body.Bytes(), &env)
			if env.Error.Code != "SERVICE_UNAVAILABLE" {
				t.Errorf("error code = %q, body %s", env.Error.Code, rr.Body.String())
			}
			if len(sink.events) != 0 {
				t.Errorf("audit events = %+v, want none", sink.events)
			}
		})
	}
}

// TestForcedUploadTokenKeyRotationIsAbsentWithoutARing pins that a router
// with no key ring wired does not serve the route.
//
// spec: §15.1 (REST API)
func TestForcedUploadTokenKeyRotationIsAbsentWithoutARing(t *testing.T) {
	router := admin.NewRouter(tenantstore.NewMemory(), admin.Options{})
	if rr := postRotate(t, router, withAdminPrincipal); rr.Code != http.StatusNotFound && rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("status %d, want 404", rr.Code)
	}
}

// TestTheUploadTokenKeyRingIsAnUploadTokenKeyRotator pins that the ring the
// gateway wires satisfies the route's interface.
//
// spec: §15.1 (REST API)
func TestTheUploadTokenKeyRingIsAnUploadTokenKeyRotator(t *testing.T) {
	var _ admin.UploadTokenKeyRotator = (*signingkeys.Ring)(nil)
}
