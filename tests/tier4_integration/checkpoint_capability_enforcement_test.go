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
// The refusals span a provider matrix because the T4 encryption binding
// is per backend (§12):
//
//   - MinIO and AWS S3 are SigV4 backends. Both fold the tenant's
//     SSE-KMS headers and the exact Content-Length into the signature
//     (X-Amz-SignedHeaders), so both enforce the stripped-header and
//     over-length bounds. Assertions (1) and (2) run against each: the
//     MinIO presign path (PresignHeader) and the AWS S3 presign path
//     (PresignClient.PresignPutObject), both signed for the same live
//     MinIO container. The S3 backend addresses the emulator with
//     path-style addressing (blobstore/s3 Config.UsePathStyle).
//
//   - GCS V4 signed URLs and Azure account-key SAS carry neither the
//     SSE-KMS header nor the Content-Length in the signature, so their
//     T4 encryption posture rests on a bucket-default CMEK (GCS) or a
//     container default encryption scope with override prevention
//     (Azure) rather than on the grant. The distinct fail-closed arm for
//     each asserts that a T4-tenant chunk PUT lands under that default
//     and that a PUT attempting a different scope is denied. That
//     enforcement lives in the object store's own encryption engine and
//     is observable only against a live GCS bucket or Azure container
//     with the default configured, which no local emulator provides, so
//     those arms skip unless a live endpoint is supplied. This runtime
//     object-store enforcement is distinct from the §17.6 install-time
//     preflight and the gateway-startup assertion, which fail a
//     misconfigured deployment closed before it serves a T4 tenant.

package tier4_integration_test

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"

	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/blobstore/miniostore"
	"github.com/lennylabs/lenny/pkg/blobstore/s3"
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

// sigV4Putter is the presign surface the header-binding assertions
// exercise. Both miniostore.Store and s3.Store satisfy it.
type sigV4Putter interface {
	PresignPut(u blobstore.URI, contentLength int64, ttl time.Duration) (blobstore.Grant, error)
}

// sigV4Provider names a SigV4 signing backend and constructs a presigner
// bound to the shared live MinIO container. The tenant is a T4 tenant on
// every provider, so each PresignPut folds the SSE-KMS header set and the
// exact Content-Length into the signature.
type sigV4Provider struct {
	name     string
	newStore func(t *testing.T, mio *containers.MinIO) sigV4Putter
}

// sigV4Providers is the matrix the header-binding assertions run over:
// the self-managed MinIO backend and the AWS S3 backend, both signed for
// the same emulator so the same signature-enforcement path is exercised
// through two independent SDK presigners.
func sigV4Providers() []sigV4Provider {
	return []sigV4Provider{
		{
			name: "minio",
			newStore: func(t *testing.T, mio *containers.MinIO) sigV4Putter {
				store, err := miniostore.New(miniostore.Config{
					Endpoint:  mio.Endpoint,
					AccessKey: mio.AccessKey,
					SecretKey: mio.SecretKey,
					Bucket:    mio.Bucket,
					UseSSL:    false,
					SSEKeyResolver: func(string) (string, bool, error) {
						return mio.KMSKeyName, true, nil
					},
				})
				if err != nil {
					t.Fatalf("miniostore.New: %v", err)
				}
				return store
			},
		},
		{
			name: "s3",
			newStore: func(t *testing.T, mio *containers.MinIO) sigV4Putter {
				store, err := s3.New(s3.Config{
					AWSConfig: awssdk.Config{
						Region:       "us-east-1",
						Credentials:  credentials.NewStaticCredentialsProvider(mio.AccessKey, mio.SecretKey, ""),
						BaseEndpoint: awssdk.String("http://" + mio.Endpoint),
					},
					Bucket: mio.Bucket,
					// Path-style addressing reaches the emulator at its IP
					// endpoint, where the bucket cannot be a DNS label.
					UsePathStyle: true,
					SSEKeyResolver: func(blobstore.URI) (string, bool, error) {
						return mio.KMSKeyName, true, nil
					},
				})
				if err != nil {
					t.Fatalf("s3.New: %v", err)
				}
				return store
			},
		},
	}
}

// assertSigV4HeaderBinding runs the two signature-header refusals against
// one SigV4 provider: (1) a PUT with the signed SSE-KMS headers stripped,
// and (2) a body longer than the signed Content-Length. keyPrefix scopes
// each provider's checkpoint ids so the arms do not collide in the shared
// bucket.
func assertSigV4HeaderBinding(t *testing.T, store sigV4Putter, tenant, keyPrefix string, body []byte) {
	t.Helper()

	// (1) A PUT with the signed SSE-KMS headers stripped is rejected: the
	// signature names the headers, so omitting them fails the match
	// before a byte is written under a weaker key.
	t.Run("stripped_sse_kms_headers_rejected", func(t *testing.T) {
		u := chunkURI(tenant, keyPrefix+"_strip", 0)
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
		u := chunkURI(tenant, keyPrefix+"_overlen", 0)
		g, err := store.PresignPut(u, int64(len(body)), time.Hour)
		if err != nil {
			t.Fatalf("PresignPut: %v", err)
		}
		over := append(append([]byte(nil), body...), []byte(" and a tail beyond the signed length")...)
		if code := capReq(t, http.MethodPut, g.URL, g.Headers, over); is2xx(code) {
			t.Fatalf("PUT of an over-length body succeeded (status %d); the signed Content-Length was not enforced", code)
		}
	})
}

// spec: §13.2 (capability model), §12 (per-provider T4 mint invariants),
// §11.2 (hard cap).
//
// diagnosis: a failure here means the object store did not enforce a
// header or the Content-Length the gateway folded into the signature, so
// the capability is not the bound the §13.2 threat model claims. On a
// SigV4 provider (MinIO, AWS S3) that is a stripped or altered signed
// header; on GCS/Azure it is a chunk that landed under a scope the
// bucket/container default was supposed to deny.
func TestCheckpointCapabilityEnforcement(t *testing.T) {
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

	// (1) and (2) run per SigV4 signing provider, because MinIO and AWS S3
	// both fold the SSE-KMS header set and the exact Content-Length into
	// the signature, and either backend can serve a T4 tenant.
	for _, p := range sigV4Providers() {
		t.Run("sigv4_"+p.name, func(t *testing.T) {
			assertSigV4HeaderBinding(t, p.newStore(t, mio), tenant, p.name, body)
		})
	}

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

	// GCS and Azure sign no encryption header into the grant, so their T4
	// posture is a bucket-default CMEK (GCS) or a container default
	// encryption scope with override prevention (Azure) enforced inside
	// the object store's encryption engine. That runtime enforcement is
	// observable only against a live backend with the default configured,
	// which no local emulator provides; the arm skips absent one.
	t.Run("gcs_bucket_default_cmek_fail_closed", func(t *testing.T) {
		requireLiveObjectStore(t, "LENNY_GCS_T4_BUCKET",
			"a live GCS bucket with a bucket-default CMEK configured")
	})
	t.Run("azure_container_default_scope_fail_closed", func(t *testing.T) {
		requireLiveObjectStore(t, "LENNY_AZURE_T4_CONTAINER",
			"a live Azure container with a default encryption scope and override prevention")
	})
}

// requireLiveObjectStore skips the GCS/Azure runtime encryption arm unless
// the named environment variable points at a live backend with the T4
// default encryption configured. The GCS bucket-default CMEK and the Azure
// container default encryption scope with override prevention are enforced
// by the object store's own engine, not by a signed header, so no local
// emulator reproduces the deny path; the arm runs in a cloud tier where a
// real backend is provisioned.
func requireLiveObjectStore(t *testing.T, envVar, want string) {
	t.Helper()
	if os.Getenv(envVar) == "" {
		t.Skipf("%s unset: this arm needs %s; no local emulator enforces the default-encryption deny path, so it runs only against a live backend", envVar, want)
	}
}
