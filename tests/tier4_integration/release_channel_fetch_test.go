// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration test: the §25.8 upgrade-check consumer fetches the
// release manifest over HTTP from a configurable release-channel
// endpoint and verifies its Ed25519 X-Lenny-Release-Signature before
// trusting it. The spec (§25.8 "Upgrade Check" and the "Release Channel
// Service Details" subsection) mandates two properties this test pins
// end to end:
//
//   - GET /v1/admin/platform/upgrade-check "queries a configurable
//     release channel endpoint" over HTTP, so the consumer must fetch
//     the manifest from a remote URL rather than read a local file.
//   - "Responses are signed with an Ed25519 signature in a
//     X-Lenny-Release-Signature response header", verified against a
//     trusted public key. A response whose body does not match its
//     signature, or is signed by an untrusted key, is rejected.
//
// The test stands up the real releasechannel.Publisher on an httptest
// server (the same handler lenny-ops serves at GET /v1/latest), points
// the production releasechannel.HTTPSource at it, and drives the whole
// upgradeservice.Checker so the fetch, verify, and version-compare path
// runs as it does in lenny-ops. It then serves a tampered body and a
// wrong-key response to confirm both are refused.
//
// spec: §25.8 (Upgrade Check / Release Channel Service Details:
// endpoint contract and signing).
package tier4_integration_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lennylabs/lenny/pkg/ops/upgradeservice"
	"github.com/lennylabs/lenny/pkg/releasechannel"
)

func rcFetchManifest() releasechannel.Manifest {
	return releasechannel.Manifest{
		Version: "1.6.0",
		Images: map[string]string{
			"gateway":     "lenny-gateway:1.6.0",
			"ops":         "lenny-ops:1.6.0",
			"controllers": "lenny-controllers:1.6.0",
			"backup":      "lenny-backup:1.6.0",
		},
		Digests: map[string]string{
			"gateway":     "sha256:a1b2c3",
			"ops":         "sha256:d4e5f6",
			"controllers": "sha256:g7h8i9",
			"backup":      "sha256:j0k1l2",
		},
		MinUpgradeFrom: "1.4.0",
		SchemaVersion:  43,
		CRDVersion:     "v1beta2",
		ReleaseNotes:   "https://github.com/lennylabs/lenny/releases/tag/v1.6.0",
	}
}

func rcFetchKey(t *testing.T, id string) releasechannel.Key {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate keypair %q: %v", id, err)
	}
	return releasechannel.Key{ID: id, Private: priv, Public: pub}
}

// verifierFor builds a consumer-side Verifier trusting the public half
// of the supplied signing key, mirroring how lenny-ops loads the
// operator's release-channel public key.
func verifierFor(t *testing.T, k releasechannel.Key) *releasechannel.Verifier {
	t.Helper()
	v, err := releasechannel.NewVerifier([]releasechannel.Key{{ID: k.ID, Public: k.Public}})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	return v
}

// publisherServer starts an httptest server running the real §25.8
// release-channel Publisher, signed by key.
func publisherServer(t *testing.T, key releasechannel.Key) *httptest.Server {
	t.Helper()
	signer, err := releasechannel.NewSigner(key, nil)
	if err != nil {
		t.Fatalf("NewSigner: %v", err)
	}
	pub, err := releasechannel.NewPublisher(releasechannel.PublisherOptions{
		Source: releasechannel.NewStaticSource(map[releasechannel.Channel]releasechannel.Manifest{
			releasechannel.ChannelStable: rcFetchManifest(),
		}),
		Signer: signer,
	})
	if err != nil {
		t.Fatalf("NewPublisher: %v", err)
	}
	mux := http.NewServeMux()
	mux.Handle("GET "+releasechannel.EndpointPath, pub)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// TestUpgradeCheckFetchesAndVerifiesSignedManifest drives the whole
// consumer path: the HTTPSource fetches the signed manifest over HTTP,
// verifies the Ed25519 signature, and the Checker reports the advertised
// version as an available upgrade.
//
// diagnosis: a failure means the §25.8 upgrade-check consumer does not
// fetch-and-verify the release manifest over HTTP end to end. Either the
// HTTP fetch, the signature verification, or the version comparison is
// broken; the platform would not learn about a new release from the
// configured channel.
//
// spec: 25.8 (upgrade-check queries a configurable release channel
// endpoint; responses carry an Ed25519 X-Lenny-Release-Signature).
func TestUpgradeCheckFetchesAndVerifiesSignedManifest(t *testing.T) {
	key := rcFetchKey(t, "release-key-1")
	srv := publisherServer(t, key)

	src, err := releasechannel.NewHTTPSource(
		srv.URL+releasechannel.EndpointPath,
		verifierFor(t, key),
		"1.5.0",
		srv.Client(),
	)
	if err != nil {
		t.Fatalf("NewHTTPSource: %v", err)
	}

	checker := upgradeservice.NewChecker(upgradeservice.CheckerOptions{
		Source:         src,
		CurrentVersion: "1.5.0",
		Cache:          upgradeservice.NewMemCheckCache(),
	})

	res, err := checker.Check(context.Background())
	if err != nil {
		t.Fatalf("Check over signed HTTP channel: %v", err)
	}
	if !res.UpgradeAvailable {
		t.Errorf("UpgradeAvailable = false, want true (1.6.0 advertised over 1.5.0 running)")
	}
	if res.AvailableVersion != "1.6.0" {
		t.Errorf("AvailableVersion = %q, want 1.6.0", res.AvailableVersion)
	}
	if res.Manifest.SchemaVersion != 43 {
		t.Errorf("Manifest.SchemaVersion = %d, want 43", res.Manifest.SchemaVersion)
	}
	if res.Cached {
		t.Errorf("Cached = true, want false for a live signed fetch")
	}
}

// TestUpgradeCheckRejectsTamperedManifest confirms a response whose body
// was altered after signing fails verification: the HTTPSource returns
// an error and the Checker does not surface the tampered manifest.
//
// diagnosis: a failure means the §25.8 consumer trusts a release
// manifest whose body does not match its X-Lenny-Release-Signature. A
// man-in-the-middle could redirect the platform to attacker-chosen image
// versions.
//
// spec: 25.8 (responses are signed with an Ed25519 signature in a
// X-Lenny-Release-Signature response header).
func TestUpgradeCheckRejectsTamperedManifest(t *testing.T) {
	key := rcFetchKey(t, "release-key-1")
	signer, err := releasechannel.NewSigner(key, nil)
	if err != nil {
		t.Fatalf("NewSigner: %v", err)
	}
	// Serve a body that does not match the signature: sign the canonical
	// manifest, then flip a byte of the body before writing it.
	body, err := json.Marshal(rcFetchManifest())
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	env, err := signer.Sign(body)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	tampered := append([]byte(nil), body...)
	tampered[len(tampered)-2] ^= 0xff

	mux := http.NewServeMux()
	mux.HandleFunc("GET "+releasechannel.EndpointPath, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", releasechannel.PublishContentType)
		w.Header().Set(releasechannel.SignatureHeader, env.Marshal())
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(tampered)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	src, err := releasechannel.NewHTTPSource(
		srv.URL+releasechannel.EndpointPath,
		verifierFor(t, key),
		"1.5.0",
		srv.Client(),
	)
	if err != nil {
		t.Fatalf("NewHTTPSource: %v", err)
	}
	if _, err := src.Latest(context.Background(), releasechannel.ChannelStable); err == nil {
		t.Fatalf("Latest accepted a tampered manifest, want a verification error")
	}
}

// TestUpgradeCheckRejectsWrongKey confirms a manifest signed by a key the
// consumer does not trust is refused, so an operator that re-signs a
// mirror with their own key must configure the matching public key.
//
// diagnosis: a failure means the §25.8 consumer accepts a manifest signed
// by an untrusted key, defeating the compiled-in-public-key trust anchor.
//
// spec: 25.8 (the Lenny release-channel public key is compiled into
// lenny-ops; operators override via platform.releaseChannel.publicKeyPath).
func TestUpgradeCheckRejectsWrongKey(t *testing.T) {
	serverKey := rcFetchKey(t, "server-key")
	trustedKey := rcFetchKey(t, "trusted-key")
	srv := publisherServer(t, serverKey)

	src, err := releasechannel.NewHTTPSource(
		srv.URL+releasechannel.EndpointPath,
		verifierFor(t, trustedKey),
		"1.5.0",
		srv.Client(),
	)
	if err != nil {
		t.Fatalf("NewHTTPSource: %v", err)
	}
	if _, err := src.Latest(context.Background(), releasechannel.ChannelStable); err == nil {
		t.Fatalf("Latest accepted a manifest signed by an untrusted key, want rejection")
	}
}
