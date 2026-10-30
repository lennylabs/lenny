// SPDX-License-Identifier: MIT

package releasechannel

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// maxManifestBytes bounds the release-channel response body the consumer
// reads. The §25.8 manifest is a small JSON document; a multi-megabyte
// body is either a misconfigured endpoint or a hostile one, and reading
// it unbounded would let a malicious channel exhaust memory before the
// signature is even checked.
const maxManifestBytes = 1 << 20 // 1 MiB

// defaultFetchTimeout bounds a single release-channel fetch when the
// caller does not supply its own http.Client. The §25.8 upgrade-check
// runs on an hourly cron and its result is cached, so a bounded per-
// request timeout is preferable to an unbounded outbound call
// (code-best-practices: put a timeout on every outbound network call).
const defaultFetchTimeout = 30 * time.Second

// HTTPSource is the §25.8 upgrade-check consumer: it fetches the release
// manifest from a configurable release-channel endpoint over HTTP and
// verifies the Ed25519 X-Lenny-Release-Signature before returning it.
//
// It is the client-side counterpart of Publisher. The Publisher serves
// the signed manifest at GET /v1/latest; HTTPSource is what lenny-ops
// wires behind GET /v1/admin/platform/upgrade-check so an operator
// pointed at releases.lenny.dev (or an internal mirror) learns about a
// newer release. A response whose body does not match its signature, or
// is signed by a key the verifier does not trust, is rejected: the
// upgrade system fails closed rather than acting on an unauthenticated
// manifest.
//
// spec: §25.8 (Upgrade Check "queries a configurable release channel
// endpoint"; Release Channel Service Details: "Responses are signed with
// an Ed25519 signature in a X-Lenny-Release-Signature response header",
// verified against the compiled-in or operator-supplied public key).
type HTTPSource struct {
	baseURL        string
	client         *http.Client
	verifier       *Verifier
	currentVersion string
}

// compile-time assertion that HTTPSource satisfies the Source contract
// the publisher and the upgrade-check Checker consume.
var _ Source = (*HTTPSource)(nil)

// NewHTTPSource builds an HTTPSource that fetches from baseURL (the
// §25.8 platform.upgradeChannel endpoint, for example
// https://releases.lenny.dev/v1/latest) and verifies responses against
// verifier. currentVersion is sent as the ?currentVersion= query
// parameter so the channel can apply its personalized minUpgradeFrom
// filter; an empty currentVersion omits the parameter. A nil client
// falls back to an http.Client with defaultFetchTimeout.
//
// verifier is required: §25.8 mandates that every release-channel
// response carry an Ed25519 signature, so a consumer with no trust
// anchor cannot safely accept any manifest.
func NewHTTPSource(baseURL string, verifier *Verifier, currentVersion string, client *http.Client) (*HTTPSource, error) {
	if strings.TrimSpace(baseURL) == "" {
		return nil, errors.New("releasechannel: HTTP source needs a non-empty channel URL")
	}
	if verifier == nil {
		return nil, errors.New("releasechannel: HTTP source needs a verifier (§25.8 requires every response carry a verifiable Ed25519 signature)")
	}
	if client == nil {
		client = &http.Client{Timeout: defaultFetchTimeout}
	}
	return &HTTPSource{
		baseURL:        baseURL,
		client:         client,
		verifier:       verifier,
		currentVersion: currentVersion,
	}, nil
}

// Latest fetches the manifest for channel from the release-channel
// endpoint and returns it only after its X-Lenny-Release-Signature
// verifies against the trust anchor.
//
// It maps the wire outcomes to the Source contract: HTTP 404 (the
// channel has no advertised release, or the caller's currentVersion is
// below the release's minUpgradeFrom prerequisite) becomes
// ErrManifestNotFound, which the Checker treats as the §25.8 "no upgrade
// available" / cache-fallback signal. A transport error, a non-200
// status, a missing signature header, or a signature that does not
// verify all fail the fetch so the Checker falls back to its cached
// result rather than trusting an unauthenticated manifest.
func (s *HTTPSource) Latest(ctx context.Context, channel Channel) (Manifest, error) {
	u, err := url.Parse(s.baseURL)
	if err != nil {
		return Manifest{}, fmt.Errorf("releasechannel: parse channel URL: %w", err)
	}
	q := u.Query()
	q.Set("channel", string(channel))
	if strings.TrimSpace(s.currentVersion) != "" {
		q.Set("currentVersion", s.currentVersion)
	}
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return Manifest{}, fmt.Errorf("releasechannel: build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return Manifest{}, fmt.Errorf("releasechannel: fetch %s: %w", u.Redacted(), err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxManifestBytes))
	if err != nil {
		return Manifest{}, fmt.Errorf("releasechannel: read response body: %w", err)
	}

	switch resp.StatusCode {
	case http.StatusOK:
		// Fall through to signature verification.
	case http.StatusNotFound:
		// §25.8: the publisher returns 404 when the channel has no
		// advertised release or refuses to advertise one to this
		// currentVersion. Both are the "no upgrade for you" signal.
		return Manifest{}, ErrManifestNotFound
	default:
		return Manifest{}, fmt.Errorf("releasechannel: channel %s returned HTTP %d", u.Redacted(), resp.StatusCode)
	}

	sig := resp.Header.Get(SignatureHeader)
	if sig == "" {
		// §25.8 requires the signature header on every response. A 200
		// without it is refused: fail closed rather than trust an
		// unsigned manifest.
		return Manifest{}, fmt.Errorf("releasechannel: response from %s is missing the %s header", u.Redacted(), SignatureHeader)
	}

	m, err := VerifyResponse(body, sig, s.verifier)
	if err != nil {
		return Manifest{}, fmt.Errorf("releasechannel: verify manifest from %s: %w", u.Redacted(), err)
	}
	return m, nil
}
