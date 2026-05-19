// SPDX-License-Identifier: MIT

package registry_digest_test

import (
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/admission/registry_digest"
)

func TestAdmitsWhenRequireDigestDisabled(t *testing.T) {
	// When platform.registry.requireDigest is false, the webhook is a
	// pass-through: tag-only references are accepted because the
	// installation has opted out of digest pinning.
	d := registry_digest.Decide(registry_digest.Request{
		PodName:   "agent-1",
		Namespace: "lenny-agents",
		Images: []string{
			"ghcr.io/lennylabs/lenny-gateway:1.5.0",
			"ghcr.io/lennylabs/echo:latest",
		},
		Config: registry_digest.Config{RequireDigest: false},
	})
	if !d.Allowed {
		t.Errorf("requireDigest=false should admit unchecked, got %+v", d)
	}
	if d.Code != 200 {
		t.Errorf("Code = %d, want 200", d.Code)
	}
}

func TestAdmitsWhenAllImagesAreDigestPinned(t *testing.T) {
	d := registry_digest.Decide(registry_digest.Request{
		PodName:   "agent-1",
		Namespace: "lenny-agents",
		Images: []string{
			"ghcr.io/lennylabs/lenny-gateway@sha256:abcd",
			"ghcr.io/lennylabs/echo@sha256:0123",
		},
		Config: registry_digest.Config{RequireDigest: true},
	})
	if !d.Allowed {
		t.Errorf("digest-pinned pod should be admitted, got %+v", d)
	}
	if len(d.OffendingImages) != 0 {
		t.Errorf("OffendingImages should be empty, got %v", d.OffendingImages)
	}
}

func TestRejectsWhenSingleImageIsTagPinned(t *testing.T) {
	d := registry_digest.Decide(registry_digest.Request{
		PodName:   "agent-1",
		Namespace: "lenny-agents",
		Images: []string{
			"ghcr.io/lennylabs/lenny-gateway@sha256:abcd",
			"ghcr.io/lennylabs/echo:latest", // tag-only
		},
		Config: registry_digest.Config{RequireDigest: true},
	})
	if d.Allowed {
		t.Errorf("a tag-only reference must be rejected when requireDigest is true")
	}
	if d.Code != 403 {
		t.Errorf("Code = %d, want 403", d.Code)
	}
	if !strings.Contains(d.Reason, "POD_IMAGE_DIGEST_REQUIRED") {
		t.Errorf("Reason should cite POD_IMAGE_DIGEST_REQUIRED, got %q", d.Reason)
	}
	if !strings.Contains(d.Reason, "lenny-agents/agent-1") {
		t.Errorf("Reason should include the pod label, got %q", d.Reason)
	}
	if len(d.OffendingImages) != 1 || d.OffendingImages[0] != "ghcr.io/lennylabs/echo:latest" {
		t.Errorf("OffendingImages = %v, want [ghcr.io/lennylabs/echo:latest]", d.OffendingImages)
	}
}

func TestRejectsWhenAllImagesAreTagPinned(t *testing.T) {
	d := registry_digest.Decide(registry_digest.Request{
		PodName:   "agent-1",
		Namespace: "lenny-agents",
		Images: []string{
			"ghcr.io/lennylabs/lenny-gateway:1.5.0",
			"ghcr.io/lennylabs/echo:latest",
		},
		Config: registry_digest.Config{RequireDigest: true},
	})
	if d.Allowed {
		t.Errorf("a pod with no digest-pinned references must be rejected")
	}
	if len(d.OffendingImages) != 2 {
		t.Errorf("OffendingImages should list both refs, got %v", d.OffendingImages)
	}
	// Determinism: the list is sorted.
	if d.OffendingImages[0] >= d.OffendingImages[1] {
		t.Errorf("OffendingImages should be sorted, got %v", d.OffendingImages)
	}
}

func TestDedupesRepeatedOffenders(t *testing.T) {
	// A pod that lists the same tag-only reference on several
	// containers should appear once in OffendingImages.
	d := registry_digest.Decide(registry_digest.Request{
		Images: []string{
			"ghcr.io/lennylabs/echo:latest",
			"ghcr.io/lennylabs/echo:latest",
			"ghcr.io/lennylabs/echo:latest",
		},
		Config: registry_digest.Config{RequireDigest: true},
	})
	if d.Allowed {
		t.Errorf("should be rejected, got %+v", d)
	}
	if len(d.OffendingImages) != 1 {
		t.Errorf("dedupe failed: OffendingImages = %v", d.OffendingImages)
	}
}

func TestIgnoresEmptyImages(t *testing.T) {
	// An admission payload with an unset image entry should not falsely
	// reject — the API server will fill it in or reject elsewhere.
	d := registry_digest.Decide(registry_digest.Request{
		Images: []string{"", "ghcr.io/lennylabs/echo@sha256:abcd", ""},
		Config: registry_digest.Config{RequireDigest: true},
	})
	if !d.Allowed {
		t.Errorf("empty image entries should not falsely reject, got %+v", d)
	}
}

func TestAdmitsZeroImagesUnchecked(t *testing.T) {
	d := registry_digest.Decide(registry_digest.Request{
		Images: nil,
		Config: registry_digest.Config{RequireDigest: true},
	})
	if !d.Allowed {
		t.Errorf("a pod with no images should be admitted (other webhooks catch it)")
	}
}

func TestPodLabelFallsBackToUnnamed(t *testing.T) {
	d := registry_digest.Decide(registry_digest.Request{
		Images: []string{"ghcr.io/lennylabs/echo:tag"},
		Config: registry_digest.Config{RequireDigest: true},
	})
	if d.Allowed {
		t.Fatalf("expected rejection")
	}
	if !strings.Contains(d.Reason, "<unnamed>") {
		t.Errorf("Reason should fall back to <unnamed> when name and namespace are empty, got %q", d.Reason)
	}
}

func TestRejectMessageNamesEveryOffender(t *testing.T) {
	d := registry_digest.Decide(registry_digest.Request{
		Namespace: "lenny-agents",
		PodName:   "agent-1",
		Images: []string{
			"ghcr.io/lennylabs/foo:1",
			"ghcr.io/lennylabs/bar:2",
			"ghcr.io/lennylabs/baz@sha256:abc",
		},
		Config: registry_digest.Config{RequireDigest: true},
	})
	if d.Allowed {
		t.Fatalf("expected rejection")
	}
	for _, img := range []string{"foo:1", "bar:2"} {
		if !strings.Contains(d.Reason, img) {
			t.Errorf("Reason should mention %q, got %q", img, d.Reason)
		}
	}
	if strings.Contains(d.Reason, "baz") {
		t.Errorf("Reason should not mention the digest-pinned baz image, got %q", d.Reason)
	}
}

func TestWhitespaceImageRefsAreTrimmed(t *testing.T) {
	d := registry_digest.Decide(registry_digest.Request{
		Images: []string{
			"  ghcr.io/lennylabs/echo:latest  ",
		},
		Config: registry_digest.Config{RequireDigest: true},
	})
	if d.Allowed {
		t.Fatalf("trimmed tag-only image must reject")
	}
	if len(d.OffendingImages) != 1 || d.OffendingImages[0] != "ghcr.io/lennylabs/echo:latest" {
		t.Errorf("OffendingImages = %v, want [ghcr.io/lennylabs/echo:latest]", d.OffendingImages)
	}
}
