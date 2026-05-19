// SPDX-License-Identifier: MIT

package opsserver_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lennylabs/lenny/pkg/ops/opsserver"
	"github.com/lennylabs/lenny/pkg/releasechannel"
)

// makeReleaseChannelPublisher builds a minimal §25.8 publisher carrying
// one stable manifest signed by a freshly-generated test key.
func makeReleaseChannelPublisher(t *testing.T) *releasechannel.Publisher {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate ed25519 key: %v", err)
	}
	signer, err := releasechannel.NewSigner(releasechannel.Key{
		ID:      "test-key-1",
		Private: priv,
		Public:  pub,
	}, nil)
	if err != nil {
		t.Fatalf("NewSigner: %v", err)
	}
	source := releasechannel.NewStaticSource(map[releasechannel.Channel]releasechannel.Manifest{
		releasechannel.ChannelStable: {
			Version: "1.0.0",
			Images:  map[string]string{"gateway": "lenny-gateway:1.0.0"},
		},
	})
	publisher, err := releasechannel.NewPublisher(releasechannel.PublisherOptions{
		Source: source, Signer: signer,
	})
	if err != nil {
		t.Fatalf("NewPublisher: %v", err)
	}
	return publisher
}

func TestReleaseChannelRouteServesWhenConfigured(t *testing.T) {
	s := opsserver.New(opsserver.Options{
		ReleaseChannel: makeReleaseChannelPublisher(t),
	})
	req := httptest.NewRequest(http.MethodGet, releasechannel.EndpointPath, nil)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if got := w.Header().Get(releasechannel.SignatureHeader); got == "" {
		t.Errorf("response missing %s header", releasechannel.SignatureHeader)
	}
}

func TestReleaseChannelRouteIs404WhenNotConfigured(t *testing.T) {
	// A lenny-ops install without an Ed25519 signing key has no
	// release-channel publisher; the path is unmapped and returns 404
	// rather than serving unsigned responses.
	s := opsserver.New(opsserver.Options{})
	req := httptest.NewRequest(http.MethodGet, releasechannel.EndpointPath, nil)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("status without publisher = %d, want 404", w.Code)
	}
}

func TestReleaseChannelRouteRejectsPost(t *testing.T) {
	s := opsserver.New(opsserver.Options{
		ReleaseChannel: makeReleaseChannelPublisher(t),
	})
	req := httptest.NewRequest(http.MethodPost, releasechannel.EndpointPath, nil)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST status = %d, want 405", w.Code)
	}
}
