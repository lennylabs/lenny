// SPDX-License-Identifier: MIT

package releasechannel_test

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/releasechannel"
)

func sampleManifest() releasechannel.Manifest {
	return releasechannel.Manifest{
		Version: "1.5.0",
		Images: map[string]string{
			"gateway":     "lenny-gateway:1.5.0",
			"ops":         "lenny-ops:1.5.0",
			"controllers": "lenny-controllers:1.5.0",
			"backup":      "lenny-backup:1.5.0",
		},
		Digests: map[string]string{
			"gateway":     "sha256:a1b2c3",
			"ops":         "sha256:d4e5f6",
			"controllers": "sha256:g7h8i9",
			"backup":      "sha256:j0k1l2",
		},
		MinUpgradeFrom: "1.3.0",
		SchemaVersion:  42,
		CRDVersion:     "v1beta2",
		ReleaseNotes:   "https://github.com/lennylabs/lenny/releases/tag/v1.5.0",
	}
}

func buildPublisher(t *testing.T) (*releasechannel.Publisher, *releasechannel.Signer) {
	t.Helper()
	k := generateKey(t, "key-1")
	signer, err := releasechannel.NewSigner(k, nil)
	if err != nil {
		t.Fatalf("NewSigner: %v", err)
	}
	source := releasechannel.NewStaticSource(map[releasechannel.Channel]releasechannel.Manifest{
		releasechannel.ChannelStable: sampleManifest(),
	})
	pub, err := releasechannel.NewPublisher(releasechannel.PublisherOptions{
		Source: source, Signer: signer,
	})
	if err != nil {
		t.Fatalf("NewPublisher: %v", err)
	}
	return pub, signer
}

func TestPublisherServesSignedStableManifest(t *testing.T) {
	pub, signer := buildPublisher(t)

	req := httptest.NewRequest(http.MethodGet, "/v1/latest", nil)
	w := httptest.NewRecorder()
	pub.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if got := w.Header().Get("Content-Type"); got != releasechannel.PublishContentType {
		t.Errorf("Content-Type = %q, want %q", got, releasechannel.PublishContentType)
	}
	sigHeader := w.Header().Get(releasechannel.SignatureHeader)
	if sigHeader == "" {
		t.Fatalf("response missing %s", releasechannel.SignatureHeader)
	}

	body, _ := io.ReadAll(w.Body)
	verifier, _ := releasechannel.NewVerifier(signer.PublicKeySet())
	got, err := releasechannel.VerifyResponse(body, sigHeader, verifier)
	if err != nil {
		t.Fatalf("VerifyResponse: %v", err)
	}
	if got.Version != "1.5.0" {
		t.Errorf("Version = %q, want 1.5.0", got.Version)
	}
	if got.CRDVersion != "v1beta2" {
		t.Errorf("CRDVersion = %q, want v1beta2", got.CRDVersion)
	}
}

// spec: §25.8 line 3410 — currentVersion below minUpgradeFrom is refused.
func TestPublisherRefusesBelowMinUpgradeFrom(t *testing.T) {
	pub, _ := buildPublisher(t) // sampleManifest has minUpgradeFrom 1.3.0
	req := httptest.NewRequest(http.MethodGet, "/v1/latest?currentVersion=1.2.0", nil)
	w := httptest.NewRecorder()
	pub.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (current below minUpgradeFrom)", w.Code)
	}
}

// spec: §25.8 line 3410 — currentVersion at or above minUpgradeFrom is served.
func TestPublisherServesAtOrAboveMinUpgradeFrom(t *testing.T) {
	pub, _ := buildPublisher(t)
	for _, cur := range []string{"1.3.0", "1.4.3", "2.0.0"} {
		req := httptest.NewRequest(http.MethodGet, "/v1/latest?currentVersion="+cur, nil)
		w := httptest.NewRecorder()
		pub.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("currentVersion=%s status = %d, want 200", cur, w.Code)
		}
	}
}

// spec: §25.8 — an absent currentVersion is unfiltered (the filter is opt-in).
func TestPublisherNoCurrentVersionUnfiltered(t *testing.T) {
	pub, _ := buildPublisher(t)
	req := httptest.NewRequest(http.MethodGet, "/v1/latest", nil)
	w := httptest.NewRecorder()
	pub.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (no currentVersion → unfiltered)", w.Code)
	}
}

// spec: §25.8 — a release with no minUpgradeFrom floor advertises to any current.
func TestPublisherNoFloorAdvertisesToAny(t *testing.T) {
	k := generateKey(t, "key-1")
	signer, _ := releasechannel.NewSigner(k, nil)
	source := releasechannel.NewStaticSource(map[releasechannel.Channel]releasechannel.Manifest{
		releasechannel.ChannelStable: {Version: "1.5.0"}, // no MinUpgradeFrom
	})
	pub, _ := releasechannel.NewPublisher(releasechannel.PublisherOptions{Source: source, Signer: signer})
	req := httptest.NewRequest(http.MethodGet, "/v1/latest?currentVersion=0.1.0", nil)
	w := httptest.NewRecorder()
	pub.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (no floor)", w.Code)
	}
}

func TestPublisherHonorsBetaChannel(t *testing.T) {
	k := generateKey(t, "key-1")
	signer, _ := releasechannel.NewSigner(k, nil)
	source := releasechannel.NewStaticSource(map[releasechannel.Channel]releasechannel.Manifest{
		releasechannel.ChannelStable: {Version: "1.5.0"},
		releasechannel.ChannelBeta:   {Version: "1.6.0-beta.1"},
	})
	pub, _ := releasechannel.NewPublisher(releasechannel.PublisherOptions{
		Source: source, Signer: signer,
	})

	req := httptest.NewRequest(http.MethodGet, "/v1/latest?channel=beta", nil)
	w := httptest.NewRecorder()
	pub.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("beta status = %d, want 200", w.Code)
	}
	var m releasechannel.Manifest
	_ = json.Unmarshal(w.Body.Bytes(), &m)
	if m.Version != "1.6.0-beta.1" {
		t.Errorf("beta Version = %q, want 1.6.0-beta.1", m.Version)
	}
}

func TestPublisherRejectsUnknownChannel(t *testing.T) {
	pub, _ := buildPublisher(t)
	req := httptest.NewRequest(http.MethodGet, "/v1/latest?channel=nightly", nil)
	w := httptest.NewRecorder()
	pub.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status for unknown channel = %d, want 400", w.Code)
	}
}

func TestPublisherReturns404ForUnpublishedChannel(t *testing.T) {
	k := generateKey(t, "key-1")
	signer, _ := releasechannel.NewSigner(k, nil)
	source := releasechannel.NewStaticSource(map[releasechannel.Channel]releasechannel.Manifest{
		// stable absent, beta absent
	})
	pub, _ := releasechannel.NewPublisher(releasechannel.PublisherOptions{
		Source: source, Signer: signer,
	})
	w := httptest.NewRecorder()
	pub.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/latest", nil))
	if w.Code != http.StatusNotFound {
		t.Errorf("status for empty channel = %d, want 404", w.Code)
	}
}

func TestPublisherRejectsNonGet(t *testing.T) {
	pub, _ := buildPublisher(t)
	req := httptest.NewRequest(http.MethodPost, "/v1/latest", strings.NewReader(`{}`))
	w := httptest.NewRecorder()
	pub.ServeHTTP(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST status = %d, want 405", w.Code)
	}
}

// errSource always fails Latest. Used to test the SLO-grade error
// surface on Source outages.
type errSource struct{}

func (errSource) Latest(_ releasechannel.Channel) (releasechannel.Manifest, error) {
	return releasechannel.Manifest{}, errors.New("postgres unreachable")
}

func TestPublisherReturns503OnSourceError(t *testing.T) {
	k := generateKey(t, "key-1")
	signer, _ := releasechannel.NewSigner(k, nil)
	pub, _ := releasechannel.NewPublisher(releasechannel.PublisherOptions{
		Source: errSource{}, Signer: signer,
	})
	w := httptest.NewRecorder()
	pub.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/latest", nil))
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("source-error status = %d, want 503", w.Code)
	}
}

func TestNewPublisherRequiresSigner(t *testing.T) {
	src := releasechannel.NewStaticSource(nil)
	if _, err := releasechannel.NewPublisher(releasechannel.PublisherOptions{
		Source: src, Signer: nil,
	}); err == nil {
		t.Errorf("NewPublisher should reject a nil Signer (§25.8 requires signed responses)")
	}
}

func TestNewPublisherRequiresSource(t *testing.T) {
	k := generateKey(t, "key-1")
	signer, _ := releasechannel.NewSigner(k, nil)
	if _, err := releasechannel.NewPublisher(releasechannel.PublisherOptions{
		Source: nil, Signer: signer,
	}); err == nil {
		t.Errorf("NewPublisher should reject a nil Source")
	}
}

func TestParseChannelDefaultsToStable(t *testing.T) {
	c, err := releasechannel.ParseChannel("")
	if err != nil {
		t.Fatalf("ParseChannel(\"\"): %v", err)
	}
	if c != releasechannel.ChannelStable {
		t.Errorf("ParseChannel(\"\") = %q, want stable", c)
	}
}

func TestStaticSourceReturnsErrManifestNotFound(t *testing.T) {
	src := releasechannel.NewStaticSource(nil)
	_, err := src.Latest(releasechannel.ChannelStable)
	if !errors.Is(err, releasechannel.ErrManifestNotFound) {
		t.Errorf("Latest on empty source = %v, want ErrManifestNotFound", err)
	}
}

func TestVerifyResponseRoundTrip(t *testing.T) {
	pub, signer := buildPublisher(t)
	w := httptest.NewRecorder()
	pub.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/latest", nil))
	body := w.Body.Bytes()
	sigHeader := w.Header().Get(releasechannel.SignatureHeader)

	verifier, _ := releasechannel.NewVerifier(signer.PublicKeySet())
	manifest, err := releasechannel.VerifyResponse(body, sigHeader, verifier)
	if err != nil {
		t.Fatalf("VerifyResponse: %v", err)
	}
	if manifest.Version != "1.5.0" {
		t.Errorf("decoded manifest Version = %q, want 1.5.0", manifest.Version)
	}
}
