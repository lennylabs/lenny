// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration test for the §13.2 presigned-checkpoint capability
// model, driven against a live MinIO container rather than in-memory
// fakes. The gateway mints a single-key, single-method, short-expiry
// capability and hands it to an agent pod. The object store, rather than
// the gateway, enforces every bound the capability claims. This test
// stands in the negative space: it mints a chunk grant and asserts the
// object store refuses every attempt to widen it.
//
// MinIO folds the tenant's SSE-KMS headers and the exact Content-Length
// into the SigV4 signature, so this file exercises the object store's
// SigV4-header enforcement path against a live backend. The scope is
// MinIO only. The AWS S3 backend enforces the same signed headers, but
// pkg/blobstore/s3 addresses buckets virtual-hosted-style and exposes no
// path-style knob, so it cannot reach an S3-compatible emulator at an IP
// endpoint (tests/tier2_component/stores/s3store_test.go documents the
// same limitation); an object-store runtime arm for AWS S3 waits on that
// product config decision. The GCS V4 signed URL and Azure SAS paths
// carry neither the SSE-KMS header nor the Content-Length in the
// signature (§12), so their T4 encryption posture rests on a
// bucket-default CMEK (GCS) or a container default encryption scope with
// override prevention (Azure). That posture is a distinct enforcement
// point verified by the §17.6 install-time preflight check and the
// gateway-startup assertion, not by the object store's SigV4
// verification this file exercises.

package tier4_integration_test

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/blobstore/miniostore"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
)

// checkpointCapabilityTTL is the paired capability TTL
// (checkpointCapabilityTTLSeconds, default 30 s in production). The
// replay-after-expiry assertion uses a short TTL so the test does not
// wait 30 s for the window to close.
const checkpointCapabilityTTL = 2 * time.Second

// chunkURI builds a §10.1 chunked-checkpoint key
// `/{tenant}/checkpoints/{session}/{checkpoint_id}/chunk-{n}.tar`.
func chunkURI(tenant, checkpointID string, index int) blobstore.URI {
	return blobstore.URI{
		TenantID:   tenant,
		ObjectType: blobstore.ObjectTypeCheckpoint,
		SessionID:  "sess_cap",
		PartID:     fmt.Sprintf("%s/chunk-%05d.tar", checkpointID, index),
		TTL:        time.Hour,
	}
}

// capReq issues method against rawURL replaying replayHeaders (the
// grant's signed headers, minus Content-Length which the HTTP client
// derives from the body) and returns the object store's HTTP status.
func capReq(t *testing.T, method, rawURL string, replayHeaders map[string]string, body []byte) int {
	t.Helper()
	var r io.Reader
	if body != nil {
		r = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, rawURL, r)
	if err != nil {
		t.Fatalf("build %s request: %v", method, err)
	}
	for k, v := range replayHeaders {
		// The HTTP client sets Content-Length from the body; replaying a
		// literal value here would mask the over-length assertion.
		if strings.EqualFold(k, "Content-Length") {
			continue
		}
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, rawURL, err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode
}

func is2xx(code int) bool { return code >= 200 && code < 300 }

// spec: §13.2 (capability model), §12 (per-provider T4 mint invariants),
// §11.2 (hard cap).
//
// diagnosis: a failure here means MinIO did not enforce a header or the
// Content-Length the gateway folded into the SigV4 signature, so the
// capability is not the bound the §13.2 threat model claims. The GCS and
// Azure bucket-default CMEK / container default encryption posture is a
// distinct enforcement point verified by the §17.6 preflight check and
// the gateway-startup assertion, not by this object-store SigV4 path.
func TestCheckpointCapabilityEnforcementMinIO(t *testing.T) {
	// A KMS key on the container lets a valid PUT replay the signed
	// SSE-KMS headers and land, so the stripped-header assertion contrasts
	// against a known-good baseline rather than an always-failing one.
	mio := containers.StartMinIO(t, containers.MinIOOptions{KMSKeyName: "checkpoint-key"})

	store, err := miniostore.New(miniostore.Config{
		Endpoint:  mio.Endpoint,
		AccessKey: mio.AccessKey,
		SecretKey: mio.SecretKey,
		Bucket:    mio.Bucket,
		UseSSL:    false,
		// A T4 tenant: every PUT grant folds the SSE-KMS headers and the
		// exact Content-Length into the SigV4 signature.
		SSEKeyResolver: func(string) (string, bool, error) {
			return mio.KMSKeyName, true, nil
		},
	})
	if err != nil {
		t.Fatalf("miniostore.New: %v", err)
	}

	const tenant = "acme"
	body := []byte("checkpoint chunk payload for acme")

	// A valid PUT capability lands only when every signed header and the
	// exact byte length are replayed. This is the baseline the six
	// refusals below contrast against.
	u := chunkURI(tenant, "ck_baseline", 0)
	grant, err := store.PresignPut(u, int64(len(body)), time.Hour)
	if err != nil {
		t.Fatalf("PresignPut baseline: %v", err)
	}
	if code := capReq(t, http.MethodPut, grant.URL, grant.Headers, body); !is2xx(code) {
		t.Fatalf("valid PUT capability was rejected with status %d; the baseline grant does not land", code)
	}

	// (1) A PUT with the signed SSE-KMS headers stripped is rejected: the
	// signature names the headers, so omitting them fails the match
	// before a byte is written under a weaker key.
	t.Run("stripped_sse_kms_headers_rejected", func(t *testing.T) {
		u := chunkURI(tenant, "ck_strip", 0)
		g, err := store.PresignPut(u, int64(len(body)), time.Hour)
		if err != nil {
			t.Fatalf("PresignPut: %v", err)
		}
		if _, ok := g.Headers["X-Amz-Server-Side-Encryption"]; !ok {
			t.Fatalf("grant did not fold an SSE-KMS header for a T4 tenant: %v", g.Headers)
		}
		if code := capReq(t, http.MethodPut, g.URL, nil, body); is2xx(code) {
			t.Fatalf("PUT with SSE-KMS headers stripped succeeded (status %d); the object store did not enforce a signed header", code)
		}
	})

	// (2) A body longer than the signed Content-Length is rejected: the
	// wire Content-Length differs from the one folded into the signature.
	t.Run("over_length_body_rejected", func(t *testing.T) {
		u := chunkURI(tenant, "ck_overlen", 0)
		g, err := store.PresignPut(u, int64(len(body)), time.Hour)
		if err != nil {
			t.Fatalf("PresignPut: %v", err)
		}
		over := append(append([]byte(nil), body...), []byte(" and a tail beyond the signed length")...)
		if code := capReq(t, http.MethodPut, g.URL, g.Headers, over); is2xx(code) {
			t.Fatalf("PUT of an over-length body succeeded (status %d); the signed Content-Length was not enforced", code)
		}
	})

	// (3) The URL with the object key rewritten to another tenant's
	// prefix is rejected: the tenant prefix is bound into the signature.
	t.Run("cross_tenant_key_rewrite_rejected", func(t *testing.T) {
		u := chunkURI(tenant, "ck_rewrite", 0)
		g, err := store.PresignPut(u, int64(len(body)), time.Hour)
		if err != nil {
			t.Fatalf("PresignPut: %v", err)
		}
		parsed, err := url.Parse(g.URL)
		if err != nil {
			t.Fatalf("parse grant URL: %v", err)
		}
		parsed.Path = strings.Replace(parsed.Path, "/"+tenant+"/", "/globex/", 1)
		if !strings.Contains(parsed.Path, "/globex/") {
			t.Fatalf("test could not rewrite the tenant prefix in %q", g.URL)
		}
		if code := capReq(t, http.MethodPut, parsed.String(), g.Headers, body); is2xx(code) {
			t.Fatalf("PUT against a foreign tenant's key succeeded (status %d); the tenant prefix was not bound", code)
		}
	})

	// (4) The URL replayed after checkpointCapabilityTTLSeconds is
	// rejected: the object store enforces the signed expiry.
	t.Run("replay_after_ttl_rejected", func(t *testing.T) {
		u := chunkURI(tenant, "ck_ttl", 0)
		g, err := store.PresignPut(u, int64(len(body)), checkpointCapabilityTTL)
		if err != nil {
			t.Fatalf("PresignPut: %v", err)
		}
		time.Sleep(checkpointCapabilityTTL + time.Second)
		if code := capReq(t, http.MethodPut, g.URL, g.Headers, body); is2xx(code) {
			t.Fatalf("PUT replayed after the capability TTL succeeded (status %d); the signed expiry was not enforced", code)
		}
	})

	// (5) A PUT-signed URL used with GET or DELETE is rejected, and a
	// GET-signed URL used with PUT or against another chunk's key is
	// rejected: the HTTP method and the single key are bound.
	t.Run("method_and_key_confusion_rejected", func(t *testing.T) {
		putU := chunkURI(tenant, "ck_method", 0)
		putGrant, err := store.PresignPut(putU, int64(len(body)), time.Hour)
		if err != nil {
			t.Fatalf("PresignPut: %v", err)
		}
		if code := capReq(t, http.MethodGet, putGrant.URL, nil, nil); is2xx(code) {
			t.Fatalf("PUT-signed URL served a GET (status %d); the method was not bound", code)
		}
		if code := capReq(t, http.MethodDelete, putGrant.URL, nil, nil); is2xx(code) {
			t.Fatalf("PUT-signed URL served a DELETE (status %d); the method was not bound", code)
		}

		// Land a real object so the GET grant has something to read.
		landURI := chunkURI(tenant, "ck_method", 1)
		landGrant, err := store.PresignPut(landURI, int64(len(body)), time.Hour)
		if err != nil {
			t.Fatalf("PresignPut for GET target: %v", err)
		}
		if code := capReq(t, http.MethodPut, landGrant.URL, landGrant.Headers, body); !is2xx(code) {
			t.Fatalf("could not land the GET-target object (status %d)", code)
		}
		getGrant, err := store.PresignGet(landURI, time.Hour)
		if err != nil {
			t.Fatalf("PresignGet: %v", err)
		}
		if code := capReq(t, http.MethodGet, getGrant.URL, getGrant.Headers, nil); !is2xx(code) {
			t.Fatalf("valid GET capability was rejected (status %d)", code)
		}
		if code := capReq(t, http.MethodPut, getGrant.URL, nil, body); is2xx(code) {
			t.Fatalf("GET-signed URL served a PUT (status %d); the method was not bound", code)
		}
		// The GET grant names one key; pointing it at another chunk's key
		// fails the signature.
		parsed, err := url.Parse(getGrant.URL)
		if err != nil {
			t.Fatalf("parse GET grant URL: %v", err)
		}
		parsed.Path = strings.Replace(parsed.Path, "chunk-00001.tar", "chunk-00002.tar", 1)
		if code := capReq(t, http.MethodGet, parsed.String(), getGrant.Headers, nil); is2xx(code) {
			t.Fatalf("GET-signed URL served another chunk's key (status %d); the single key was not bound", code)
		}
	})

	// (6) A per-key second PUT lands only because the key is unique per
	// checkpoint_id, and the gateway's Stat confirm observes the changed
	// size, which is how a divergent re-PUT is detected off the mint path.
	t.Run("second_put_observed_by_stat_confirm", func(t *testing.T) {
		u := chunkURI(tenant, "ck_confirm", 0)
		first := []byte("first body")
		g1, err := store.PresignPut(u, int64(len(first)), time.Hour)
		if err != nil {
			t.Fatalf("PresignPut first: %v", err)
		}
		if code := capReq(t, http.MethodPut, g1.URL, g1.Headers, first); !is2xx(code) {
			t.Fatalf("first PUT rejected (status %d)", code)
		}
		info, err := store.Stat(u)
		if err != nil {
			t.Fatalf("Stat after first PUT: %v", err)
		}
		if info.Size != int64(len(first)) {
			t.Fatalf("Stat size after first PUT = %d, want %d", info.Size, len(first))
		}

		second := []byte("a materially different second body")
		g2, err := store.PresignPut(u, int64(len(second)), time.Hour)
		if err != nil {
			t.Fatalf("PresignPut second: %v", err)
		}
		if code := capReq(t, http.MethodPut, g2.URL, g2.Headers, second); !is2xx(code) {
			t.Fatalf("second PUT to the same key rejected (status %d)", code)
		}
		info2, err := store.Stat(u)
		if err != nil {
			t.Fatalf("Stat after second PUT: %v", err)
		}
		if info2.Size != int64(len(second)) {
			t.Fatalf("Stat confirm did not observe the second PUT: size = %d, want %d", info2.Size, len(second))
		}
		if info2.Size == info.Size {
			t.Fatalf("the second PUT did not change the confirmed size (%d); the Stat confirm cannot detect a divergent re-PUT", info2.Size)
		}
	})
}
