// SPDX-License-Identifier: MIT

package upgradeservice

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// manifestAccept lists the OCI and legacy-Docker manifest media types the
// §25.8 image-pullability check accepts, so a registry serving either
// schema (or a multi-arch index/manifest list) reports the image present.
const manifestAccept = "application/vnd.oci.image.manifest.v1+json, " +
	"application/vnd.oci.image.index.v1+json, " +
	"application/vnd.docker.distribution.manifest.v2+json, " +
	"application/vnd.docker.distribution.manifest.list.v2+json"

// RegistryImagePullChecker is the production §25.8 Phase-1 image-pullability
// gate: it issues a HEAD request to the OCI Distribution API v2 manifest
// endpoint for each resolved image reference, the "crane manifest
// --platform linux/amd64 equivalent" the spec calls for. When the registry
// challenges with a Bearer token (the common case for a token-gated mirror
// or ghcr.io/Docker Hub, even for anonymous pulls of a public image), the
// checker follows the RFC 6750 challenge to fetch an anonymous pull token
// and retries once before concluding the image is unpullable.
//
// spec: §25.8 Phase 1 (line 3500).
type RegistryImagePullChecker struct {
	// Client issues the manifest and token requests. Required; use
	// NewRegistryImagePullChecker to default it.
	Client *http.Client
}

// NewRegistryImagePullChecker returns a checker whose Client is timeout
// (a nil timeout defaults to 10s), so a single unreachable registry cannot
// hang a preflight indefinitely.
func NewRegistryImagePullChecker(timeout time.Duration) *RegistryImagePullChecker {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &RegistryImagePullChecker{Client: &http.Client{Timeout: timeout}}
}

// Pullable implements ImagePullChecker. A definitive registry response
// (200 present, 404/401-after-token-exchange absent) is reported via the ok
// return with no error. A transport-level failure (DNS, dial, TLS, timeout,
// or a failed token exchange) is returned as an error so the preflight
// fails transiently (UPGRADE gate dependency unavailable) rather than
// reporting a spurious missing image.
//
// spec: §25.8 Phase 1 (line 3500): "For each component, issues a HEAD
// request to the registry manifest endpoint (or `crane manifest
// --platform linux/amd64` equivalent). This catches missing mirrors before
// any changes are made."
func (c *RegistryImagePullChecker) Pullable(ctx context.Context, ref string) (bool, string, error) {
	parsed, err := parseImageRef(ref)
	if err != nil {
		return false, err.Error(), nil
	}
	target := manifestURL(parsed)

	resp, err := c.headManifest(ctx, target, "")
	if err != nil {
		return false, "", fmt.Errorf("image pull check %s: %w", ref, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		token, terr := c.exchangeToken(ctx, resp.Header.Get("Www-Authenticate"), parsed)
		if terr != nil {
			return false, "", fmt.Errorf("image pull check %s: token exchange: %w", ref, terr)
		}
		if token != "" {
			resp2, err := c.headManifest(ctx, target, token)
			if err != nil {
				return false, "", fmt.Errorf("image pull check %s: %w", ref, err)
			}
			resp = resp2
			defer resp.Body.Close()
		}
	}

	switch resp.StatusCode {
	case http.StatusOK:
		return true, "", nil
	case http.StatusNotFound:
		return false, fmt.Sprintf("manifest not found (404) for %s", ref), nil
	default:
		return false, fmt.Sprintf("registry returned %d for %s", resp.StatusCode, ref), nil
	}
}

// headManifest issues the manifest HEAD request, optionally bearing token
// as an Authorization: Bearer header.
func (c *RegistryImagePullChecker) headManifest(ctx context.Context, target, token string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, target, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", manifestAccept)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return c.Client.Do(req)
}

// exchangeToken follows an RFC 6750 Bearer challenge to fetch an anonymous
// pull token. An empty return (no error) means challenge was not a Bearer
// challenge the caller should retry with; the original 401 stands.
func (c *RegistryImagePullChecker) exchangeToken(ctx context.Context, challenge string, ref imageRef) (string, error) {
	const prefix = "bearer "
	if len(challenge) < len(prefix) || !strings.EqualFold(challenge[:len(prefix)], prefix) {
		return "", nil
	}
	params := parseAuthParams(challenge[len(prefix):])
	realm := params["realm"]
	if realm == "" {
		return "", nil
	}
	u, err := url.Parse(realm)
	if err != nil {
		return "", fmt.Errorf("parse token realm %q: %w", realm, err)
	}
	q := u.Query()
	if svc := params["service"]; svc != "" {
		q.Set("service", svc)
	}
	scope := params["scope"]
	if scope == "" {
		scope = fmt.Sprintf("repository:%s:pull", ref.repository)
	}
	q.Set("scope", scope)
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", err
	}
	resp, err := c.Client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("token endpoint %s returned %d", u.Host, resp.StatusCode)
	}
	var body struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", fmt.Errorf("decode token response: %w", err)
	}
	if body.Token != "" {
		return body.Token, nil
	}
	return body.AccessToken, nil
}

// parseAuthParams parses the comma-separated key="value" pairs of a
// WWW-Authenticate Bearer challenge (RFC 6750 §3), e.g.
// `realm="https://auth.example.com/token",service="registry.example.com",scope="repository:name:pull"`.
func parseAuthParams(s string) map[string]string {
	out := make(map[string]string)
	for _, part := range strings.Split(s, ",") {
		kv := strings.SplitN(part, "=", 2)
		if len(kv) != 2 {
			continue
		}
		key := strings.TrimSpace(kv[0])
		val := strings.Trim(strings.TrimSpace(kv[1]), `"`)
		if key != "" {
			out[key] = val
		}
	}
	return out
}

// imageRef is a parsed OCI image reference: registry host, repository
// path, and the tag or digest to resolve.
type imageRef struct {
	host       string
	repository string
	reference  string
}

// parseImageRef splits a full image reference (as the §25.8 ImageResolver
// produces, e.g. "ghcr.io/lennylabs/lenny-gateway:1.6.0" or
// "...@sha256:...") into its registry host, repository, and tag/digest.
// The reference defaults to "latest" when neither a tag nor a digest is
// present.
func parseImageRef(ref string) (imageRef, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return imageRef{}, fmt.Errorf("empty image reference")
	}
	slash := strings.Index(ref, "/")
	if slash < 0 {
		return imageRef{}, fmt.Errorf("image reference %q has no registry host", ref)
	}
	host := ref[:slash]
	rest := ref[slash+1:]

	if at := strings.LastIndex(rest, "@"); at >= 0 {
		return imageRef{host: host, repository: rest[:at], reference: rest[at+1:]}, nil
	}
	if colon := strings.LastIndex(rest, ":"); colon >= 0 && !strings.Contains(rest[colon:], "/") {
		return imageRef{host: host, repository: rest[:colon], reference: rest[colon+1:]}, nil
	}
	return imageRef{host: host, repository: rest, reference: "latest"}, nil
}

// manifestURL builds the OCI Distribution API v2 manifest URL for r.
func manifestURL(r imageRef) string {
	return fmt.Sprintf("%s://%s/v2/%s/manifests/%s", schemeFor(r.host), r.host, r.repository, r.reference)
}

// schemeFor picks the transport scheme for a registry host. Production
// registries are addressed over TLS; a loopback host — used by the local
// stub-registry test fixture and by the `kubectl port-forward` break-glass
// workflow (Section 25.10 Bootstrapping) — is addressed over plain HTTP
// since it presents no certificate.
func schemeFor(host string) string {
	h := host
	if i := strings.IndexByte(h, ':'); i >= 0 {
		h = h[:i]
	}
	switch {
	case h == "localhost", h == "127.0.0.1", h == "0.0.0.0", strings.HasSuffix(h, ".local"):
		return "http"
	default:
		return "https"
	}
}
