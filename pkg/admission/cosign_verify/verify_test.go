// SPDX-License-Identifier: MIT

package cosign_verify

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// fakeVerifier is a test Verifier: it admits any image in its signed
// set and rejects every other image with a fixed error. It records the
// images it was asked to verify so a test can assert that out-of-scope
// images were never passed to it.
type fakeVerifier struct {
	signed  map[string]bool
	checked []string
}

func (f *fakeVerifier) Verify(_ context.Context, imageRef string) error {
	f.checked = append(f.checked, imageRef)
	if f.signed[imageRef] {
		return nil
	}
	return errors.New("no valid signature")
}

const verifiedPrefix = "ghcr.io/lennylabs/"

func TestDecideAdmitsSignedInScopeImage(t *testing.T) {
	v := &fakeVerifier{signed: map[string]bool{
		"ghcr.io/lennylabs/runtime@sha256:aaa": true,
	}}
	d := Decide(context.Background(), v, Request{
		PodName:   "agent-1",
		Namespace: "lenny-agents",
		Images:    []string{"ghcr.io/lennylabs/runtime@sha256:aaa"},
		Config:    Config{VerifiedRegistries: []string{verifiedPrefix}},
	})
	if !d.Allowed {
		t.Fatalf("signed in-scope image should be admitted, got reason %q", d.Reason)
	}
	if d.Code != 200 {
		t.Errorf("Code = %d, want 200", d.Code)
	}
}

func TestDecideDeniesUnsignedInScopeImage(t *testing.T) {
	v := &fakeVerifier{signed: map[string]bool{}}
	d := Decide(context.Background(), v, Request{
		PodName:   "agent-2",
		Namespace: "lenny-agents",
		Images:    []string{"ghcr.io/lennylabs/runtime@sha256:bbb"},
		Config:    Config{VerifiedRegistries: []string{verifiedPrefix}},
	})
	if d.Allowed {
		t.Fatalf("unsigned in-scope image must be denied")
	}
	if d.Code != 403 {
		t.Errorf("Code = %d, want 403", d.Code)
	}
	if !strings.HasPrefix(d.Reason, RejectCode) {
		t.Errorf("rejection should carry %s, got %q", RejectCode, d.Reason)
	}
	if !strings.Contains(d.Reason, "ghcr.io/lennylabs/runtime@sha256:bbb") {
		t.Errorf("rejection should name the offending image, got %q", d.Reason)
	}
}

func TestDecideAdmitsOutOfScopeImageUnchecked(t *testing.T) {
	// An image whose registry is not in the verified-registry list is
	// admitted unchecked and is never passed to the Verifier.
	v := &fakeVerifier{signed: map[string]bool{}}
	d := Decide(context.Background(), v, Request{
		PodName:   "agent-3",
		Namespace: "lenny-agents",
		Images:    []string{"docker.io/library/busybox@sha256:ccc"},
		Config:    Config{VerifiedRegistries: []string{verifiedPrefix}},
	})
	if !d.Allowed {
		t.Fatalf("out-of-scope image should be admitted unchecked, got %q", d.Reason)
	}
	if len(v.checked) != 0 {
		t.Errorf("out-of-scope image must not be passed to the Verifier; checked = %v", v.checked)
	}
}

func TestDecideDeniesPodWhenAnyInScopeImageUnsigned(t *testing.T) {
	// A pod with one signed image and one unsigned image is denied: the
	// unsigned in-scope image rejects the whole pod.
	v := &fakeVerifier{signed: map[string]bool{
		"ghcr.io/lennylabs/adapter@sha256:aaa": true,
	}}
	d := Decide(context.Background(), v, Request{
		PodName:   "agent-4",
		Namespace: "lenny-agents",
		Images: []string{
			"ghcr.io/lennylabs/adapter@sha256:aaa",
			"ghcr.io/lennylabs/runtime@sha256:ddd", // unsigned
		},
		Config: Config{VerifiedRegistries: []string{verifiedPrefix}},
	})
	if d.Allowed {
		t.Fatalf("pod with an unsigned in-scope image must be denied")
	}
	if !strings.Contains(d.Reason, "runtime@sha256:ddd") {
		t.Errorf("rejection should name the unsigned image, got %q", d.Reason)
	}
}

func TestDecideAdmitsPodWithNoInScopeImage(t *testing.T) {
	// A pod whose every image is out of scope is admitted; an empty
	// in-scope set imposes no signature requirement.
	v := &fakeVerifier{signed: map[string]bool{}}
	d := Decide(context.Background(), v, Request{
		PodName:   "agent-5",
		Namespace: "lenny-agents",
		Images: []string{
			"docker.io/library/busybox@sha256:eee",
			"registry.k8s.io/pause@sha256:fff",
		},
		Config: Config{VerifiedRegistries: []string{verifiedPrefix}},
	})
	if !d.Allowed {
		t.Fatalf("pod with no in-scope image should be admitted, got %q", d.Reason)
	}
}

func TestDecideDedupesRepeatedImage(t *testing.T) {
	// A pod that lists the same in-scope image on several containers is
	// verified once.
	v := &fakeVerifier{signed: map[string]bool{
		"ghcr.io/lennylabs/runtime@sha256:aaa": true,
	}}
	d := Decide(context.Background(), v, Request{
		PodName:   "agent-6",
		Namespace: "lenny-agents",
		Images: []string{
			"ghcr.io/lennylabs/runtime@sha256:aaa",
			"ghcr.io/lennylabs/runtime@sha256:aaa",
		},
		Config: Config{VerifiedRegistries: []string{verifiedPrefix}},
	})
	if !d.Allowed {
		t.Fatalf("repeated signed image should be admitted, got %q", d.Reason)
	}
	if len(v.checked) != 1 {
		t.Errorf("repeated image should be verified once, checked = %v", v.checked)
	}
}

func TestDecideMultipleVerifiedRegistries(t *testing.T) {
	// An image is in scope when it matches any one of the configured
	// verified-registry prefixes.
	v := &fakeVerifier{signed: map[string]bool{}}
	d := Decide(context.Background(), v, Request{
		PodName:   "agent-7",
		Namespace: "lenny-agents",
		Images:    []string{"registry.acme.com/agents/runtime@sha256:aaa"},
		Config: Config{VerifiedRegistries: []string{
			"ghcr.io/lennylabs/",
			"registry.acme.com/agents/",
		}},
	})
	if d.Allowed {
		t.Fatalf("image matching the second verified prefix must be in scope and, unsigned, denied")
	}
}
