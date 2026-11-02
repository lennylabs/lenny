// SPDX-License-Identifier: MIT

//go:build e2e_kind

// Tier-5 e2e Kind test for the §7.1 uploadToken across the chart's gateway
// replicas. One driver is pinned to each of two Ready gateway pods, a session
// is created on the first with a real warm pod behind it, and the upload and
// the finalize run on the second. The session row is the only token state the
// pods share.
package tier5_e2e_kind_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/kind"
	"github.com/lennylabs/lenny/tests/testinfra/sessiondriver"
)

// spec: 7.1 (uploadToken format and security properties, Single-use invalidation), 7.4 (Upload Safety), 10.1.1 (Stateless Replicas and Per-Session Coordination)
// diagnosis: a 401 UPLOAD_TOKEN_INVALID on the second gateway pod means the
// token still verifies against a secret held in the issuing pod, which is the
// failure a cluster still running an image older than row verification shows,
// so compare the deployed lenny-gateway image tag with the tree first. Anything
// other than 410 UPLOAD_TOKEN_CONSUMED on the replay means consumption is not
// derived from the shared row.
func TestUploadTokenCompletesAcrossGatewayPods_spec_7_1(t *testing.T) {
	c := kind.InstallLenny(t)
	a, b, tenant := twoReplicaDrivers(t, c)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	sess, err := a.CreateSession(ctx, tenant, sessiondriver.EchoRuntimeSidecar)
	if err != nil {
		if strings.Contains(err.Error(), "WARM_POOL_EXHAUSTED") || strings.Contains(err.Error(), "POD_CLAIM_FAILED") {
			t.Skipf("blocked: warm pool not ready: %v", err)
		}
		t.Fatalf("create on the first gateway pod: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, stop := context.WithTimeout(context.Background(), time.Minute)
		defer stop()
		if err := a.DeleteSession(cleanupCtx, tenant, sess.ID); err != nil {
			t.Logf("delete session %s: %v", sess.ID, err)
		}
	})
	if sess.UploadToken == "" {
		t.Fatalf("create returned no uploadToken: %+v", sess)
	}

	upload, err := b.Upload(ctx, tenant, sess.ID, sess.UploadToken, []byte("payload"))
	if err != nil {
		t.Fatalf("upload on the second gateway pod: %v", err)
	}
	if upload.Status != http.StatusCreated {
		t.Fatalf("upload on the second gateway pod: status %d code %q body %s", upload.Status, upload.Code, upload.Body)
	}
	finalize, err := b.FinalizeResult(ctx, tenant, sess.ID, sess.UploadToken)
	if err != nil {
		t.Fatalf("finalize on the second gateway pod: %v", err)
	}
	if finalize.Status != http.StatusOK {
		t.Fatalf("finalize on the second gateway pod: status %d code %q body %s", finalize.Status, finalize.Code, finalize.Body)
	}
	replay, err := a.FinalizeResult(ctx, tenant, sess.ID, sess.UploadToken)
	if err != nil {
		t.Fatalf("replay on the first gateway pod: %v", err)
	}
	if replay.Status != http.StatusGone || replay.Code != "UPLOAD_TOKEN_CONSUMED" {
		t.Errorf("replay on the first gateway pod: status %d code %q, want 410 UPLOAD_TOKEN_CONSUMED", replay.Status, replay.Code)
	}
}
