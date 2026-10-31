// SPDX-License-Identifier: MIT

package adapter

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"time"
)

// CheckpointTransport issues the object-store HTTP calls the adapter makes
// against gateway-minted presigned capabilities: the per-chunk PUT on the
// §4.4 Checkpoint stream and the per-chunk GET on the §7.3 Resume restore.
// The adapter holds no standing object-store credential (§13.2); the
// presigned URL and the signed headers the gateway supplies are the only
// authorization it carries. The interface is injected so tests can serve
// chunks without a live object store.
//
// spec: §13.2 — the agent pod receives single-key PUT and single-key GET
// capabilities only and addresses nothing the gateway did not sign.
type CheckpointTransport interface {
	// PutChunk uploads exactly contentLength bytes from body to url,
	// replaying every headers entry verbatim on the request (the gateway
	// folded those header values into the SigV4 signature, so omitting or
	// altering one fails SignatureDoesNotMatch at the object store). It
	// returns the object store's HTTP status and, on a non-2xx response,
	// the object store's error code parsed from the S3-style error body.
	// A transport-level failure (connection refused, TLS error, timeout)
	// returns a non-nil err with httpStatus 0.
	PutChunk(ctx context.Context, url string, headers map[string]string, contentLength int64, body io.Reader) (httpStatus int, errorCode string, err error)
	// GetChunk fetches url, replaying every headers entry verbatim, and
	// returns the response body for the caller to read and close. A
	// non-2xx response returns a non-nil err.
	//
	// Neither method's error carries url: the presigned URL is a bearer
	// capability, and an error string reaches a gRPC status, a span, and
	// the gateway's error chain.
	GetChunk(ctx context.Context, url string, headers map[string]string) (io.ReadCloser, error)
}

// httpCheckpointTransport is the production CheckpointTransport backed by
// an *http.Client. The client trusts the object store's TLS certificate
// through the deployment's CA bundle (--objectstore-ca-bundle), which is
// the trust anchor for a MinIO endpoint served under an internal CA.
type httpCheckpointTransport struct {
	client *http.Client
}

// NewHTTPCheckpointTransport builds the production CheckpointTransport. A
// non-empty caBundlePath adds the object store's issuing CA to the trust
// pool the PUT/GET TLS handshake verifies against; an empty path uses the
// process's system roots.
//
// spec: §13.2 — the pod reaches the object store directly over TLS on the
// checkpoint and resume paths, verified against the deployment CA bundle.
func NewHTTPCheckpointTransport(caBundlePath string) (*httpCheckpointTransport, error) {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	if caBundlePath != "" {
		pem, err := os.ReadFile(caBundlePath)
		if err != nil {
			return nil, fmt.Errorf("read object-store CA bundle %q: %w", caBundlePath, err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("object-store CA bundle %q holds no PEM certificates", caBundlePath)
		}
		tr.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	}
	return &httpCheckpointTransport{
		client: &http.Client{Transport: tr, Timeout: 5 * time.Minute},
	}, nil
}

func (t *httpCheckpointTransport) PutChunk(ctx context.Context, url string, headers map[string]string, contentLength int64, body io.Reader) (int, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, body)
	if err != nil {
		return 0, "", withoutCapability("build checkpoint PUT", err)
	}
	// ContentLength must be exact: the gateway signed it into the
	// capability and the object store rejects any other length.
	req.ContentLength = contentLength
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := t.client.Do(req)
	if err != nil {
		return 0, "", withoutCapability("checkpoint PUT", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		_, _ = io.Copy(io.Discard, resp.Body)
		return resp.StatusCode, "", nil
	}
	return resp.StatusCode, parseObjectStoreErrorCode(resp.Body), nil
}

func (t *httpCheckpointTransport) GetChunk(ctx context.Context, url string, headers map[string]string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, withoutCapability("build checkpoint GET", err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := t.client.Do(req)
	if err != nil {
		return nil, withoutCapability("checkpoint GET", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		code := parseObjectStoreErrorCode(resp.Body)
		_ = resp.Body.Close()
		return nil, fmt.Errorf("checkpoint GET: http %d code %q", resp.StatusCode, code)
	}
	return resp.Body, nil
}

// withoutCapability wraps a request error under op with the presigned URL
// removed. net/http reports a request failure as a *url.Error whose text
// repeats the whole URL, signature query included, so the wrap keeps the
// failed operation and the underlying cause and drops the URL. The cause
// stays wrapped, so errors.Is still finds a context cancellation.
//
// spec: §13.2.3 (Checkpoint Capability Model) — the presigned URL is the
// only authorization the pod holds for the object, so whoever reads it can
// use it until it expires.
func withoutCapability(op string, err error) error {
	var ue *url.Error
	if !errors.As(err, &ue) {
		return fmt.Errorf("%s: %w", op, err)
	}
	return fmt.Errorf("%s: %s: %w", op, ue.Op, ue.Err)
}

// parseObjectStoreErrorCode extracts the <Code> element from an S3-style
// error response body so the adapter can surface the object store's error
// code (e.g. SignatureDoesNotMatch, or a kms:-prefixed rejection) to the
// gateway on the Checkpoint stream. An unparseable body yields an empty
// code; the HTTP status still carries the failure class.
func parseObjectStoreErrorCode(body io.Reader) string {
	var parsed struct {
		Code string `xml:"Code"`
	}
	// Bound the read: an S3 error document is small.
	raw, _ := io.ReadAll(io.LimitReader(body, 64<<10))
	if err := xml.Unmarshal(raw, &parsed); err != nil {
		return ""
	}
	return parsed.Code
}
