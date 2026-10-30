// SPDX-License-Identifier: MIT

//go:build security

// Tier-9 §25.8 release-channel signature-verification probe. The
// upgrade-check consumer fetches the release manifest from a remote
// channel and drives image and schema decisions from it, so an
// unauthenticated or tampered manifest is a direct integrity threat: a
// man-in-the-middle who can substitute the manifest chooses which image
// versions and schema the platform upgrades to. §25.8 defends this by
// signing every response with an Ed25519 signature carried in the
// X-Lenny-Release-Signature header, verified against the compiled-in or
// operator-supplied public key.
//
// This suite pins the fail-closed security posture of the production
// releasechannel.HTTPSource consumer: it accepts a correctly signed
// manifest and refuses every unauthenticated variant, an unsigned
// response, a body altered after signing, and a body signed by a key the
// consumer does not trust.
//
// spec: §25.8 (Release Channel Service Details: "Responses are signed
// with an Ed25519 signature in a X-Lenny-Release-Signature response
// header. The Lenny release-channel public key is compiled into
// lenny-ops.").
package tier9_security_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lennylabs/lenny/pkg/releasechannel"
)

func rcKey(t *testing.T, id string) releasechannel.Key {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate keypair %q: %v", id, err)
	}
	return releasechannel.Key{ID: id, Private: priv, Public: pub}
}

func rcVerifier(t *testing.T, k releasechannel.Key) *releasechannel.Verifier {
	t.Helper()
	v, err := releasechannel.NewVerifier([]releasechannel.Key{{ID: k.ID, Public: k.Public}})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	return v
}

func rcManifest() releasechannel.Manifest {
	return releasechannel.Manifest{
		Version:        "1.6.0",
		Images:         map[string]string{"gateway": "lenny-gateway:1.6.0", "ops": "lenny-ops:1.6.0", "controllers": "lenny-controllers:1.6.0", "backup": "lenny-backup:1.6.0"},
		Digests:        map[string]string{"gateway": "sha256:a1", "ops": "sha256:b2", "controllers": "sha256:c3", "backup": "sha256:d4"},
		MinUpgradeFrom: "1.4.0",
		SchemaVersion:  43,
		CRDVersion:     "v1beta2",
		ReleaseNotes:   "https://github.com/lennylabs/lenny/releases/tag/v1.6.0",
	}
}

// serveRaw starts an httptest server returning a fixed status, signature
// header, and body, so a test can craft the exact adversarial response.
func serveRaw(t *testing.T, status int, sigHeader string, body []byte) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+releasechannel.EndpointPath, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", releasechannel.PublishContentType)
		if sigHeader != "" {
			w.Header().Set(releasechannel.SignatureHeader, sigHeader)
		}
		w.WriteHeader(status)
		_, _ = w.Write(body)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func sourceFor(t *testing.T, url string, v *releasechannel.Verifier, client *http.Client) *releasechannel.HTTPSource {
	t.Helper()
	src, err := releasechannel.NewHTTPSource(url+releasechannel.EndpointPath, v, "1.5.0", client)
	if err != nil {
		t.Fatalf("NewHTTPSource: %v", err)
	}
	return src
}

// TestReleaseChannelAcceptsValidSignature is the positive control: a
// correctly signed manifest verifies and is returned.
//
// diagnosis: a failure means the consumer rejects a legitimately signed
// manifest, breaking upgrade-check for every install.
//
// spec: 25.8 (Ed25519 X-Lenny-Release-Signature verified against the
// trusted public key).
func TestReleaseChannelAcceptsValidSignature(t *testing.T) {
	key := rcKey(t, "release-key-1")
	signer, err := releasechannel.NewSigner(key, nil)
	if err != nil {
		t.Fatalf("NewSigner: %v", err)
	}
	body, err := json.Marshal(rcManifest())
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	env, err := signer.Sign(body)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	srv := serveRaw(t, http.StatusOK, env.Marshal(), body)
	src := sourceFor(t, srv.URL, rcVerifier(t, key), srv.Client())

	m, err := src.Latest(context.Background(), releasechannel.ChannelStable)
	if err != nil {
		t.Fatalf("Latest on a valid signed response: %v", err)
	}
	if m.Version != "1.6.0" {
		t.Errorf("Version = %q, want 1.6.0", m.Version)
	}
}

// TestReleaseChannelRejectsUnsigned confirms a 200 with no
// X-Lenny-Release-Signature header is refused (fail closed): the consumer
// never trusts an unsigned manifest.
//
// diagnosis: a failure means the consumer accepts an unsigned release
// manifest, so a channel or a network attacker that strips the signature
// header can drive the platform's upgrade decisions.
//
// spec: 25.8 (responses are signed; the signature is mandatory).
func TestReleaseChannelRejectsUnsigned(t *testing.T) {
	key := rcKey(t, "release-key-1")
	body, err := json.Marshal(rcManifest())
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	srv := serveRaw(t, http.StatusOK, "", body)
	src := sourceFor(t, srv.URL, rcVerifier(t, key), srv.Client())

	if _, err := src.Latest(context.Background(), releasechannel.ChannelStable); err == nil {
		t.Fatalf("Latest accepted an unsigned response, want rejection")
	}
}

// TestReleaseChannelRejectsTampered confirms a body altered after signing
// fails verification.
//
// diagnosis: a failure means the consumer accepts a manifest whose body
// does not match its signature, defeating integrity protection.
//
// spec: 25.8 (the signature covers the response body).
func TestReleaseChannelRejectsTampered(t *testing.T) {
	key := rcKey(t, "release-key-1")
	signer, err := releasechannel.NewSigner(key, nil)
	if err != nil {
		t.Fatalf("NewSigner: %v", err)
	}
	body, err := json.Marshal(rcManifest())
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	env, err := signer.Sign(body)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	tampered := append([]byte(nil), body...)
	tampered[len(tampered)-2] ^= 0xff
	srv := serveRaw(t, http.StatusOK, env.Marshal(), tampered)
	src := sourceFor(t, srv.URL, rcVerifier(t, key), srv.Client())

	if _, err := src.Latest(context.Background(), releasechannel.ChannelStable); err == nil {
		t.Fatalf("Latest accepted a tampered body, want rejection")
	}
}

// TestReleaseChannelRejectsWrongKey confirms a manifest signed by a key
// outside the consumer's trust set is refused.
//
// diagnosis: a failure means the compiled-in / operator public-key trust
// anchor is not enforced, so a manifest signed by any key is accepted.
//
// spec: 25.8 (verified against the compiled-in or operator-supplied
// public key).
func TestReleaseChannelRejectsWrongKey(t *testing.T) {
	serverKey := rcKey(t, "attacker-key")
	trustedKey := rcKey(t, "trusted-key")
	signer, err := releasechannel.NewSigner(serverKey, nil)
	if err != nil {
		t.Fatalf("NewSigner: %v", err)
	}
	body, err := json.Marshal(rcManifest())
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	env, err := signer.Sign(body)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	srv := serveRaw(t, http.StatusOK, env.Marshal(), body)
	src := sourceFor(t, srv.URL, rcVerifier(t, trustedKey), srv.Client())

	if _, err := src.Latest(context.Background(), releasechannel.ChannelStable); err == nil {
		t.Fatalf("Latest accepted a manifest signed by an untrusted key, want rejection")
	}
}
