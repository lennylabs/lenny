// SPDX-License-Identifier: MIT

package cosign_verify

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
)

// StaticResolver is an ImageDigestResolver backed by a deployer-supplied
// policy file: a JSON map from image reference to the signed digest and
// detached signature for that image. It is the default resolver wired
// into the webhook binary.
//
// Why a policy file rather than a live registry fetch. cosign's keyless
// path and its OCI .sig-artifact fetch both require the
// github.com/sigstore/cosign and github.com/google/go-containerregistry
// modules, whose transitive trees pull a headless-browser library, a
// full Docker client, and the TUF client — a dependency surface
// disproportionate to an admission webhook. §5.2 mandates a fail-closed
// signature gate; it does not mandate where the signature is fetched
// from. The release pipeline (§18.33) already runs cosign at build time
// and signs every Lenny-built image; it emits the policy file as a
// release artifact, and the chart mounts it into the webhook pod. The
// ImageDigestResolver interface keeps a live-registry resolver addable
// later without touching the verifier or the decision logic.
type StaticResolver struct {
	// entries maps an image reference to its signing material.
	entries map[string]SignedDigest
}

// policyFile is the on-disk JSON layout StaticResolver loads. Each key
// is an image reference; each value carries the signed digest and the
// base64-encoded detached signature.
type policyFile struct {
	// Images maps an image reference to its signing material.
	Images map[string]SignedDigest `json:"images"`
}

// NewStaticResolver builds a StaticResolver from an in-memory policy
// map. The webhook binary normally calls LoadStaticResolver to read the
// map from the deployer's mounted policy file; this constructor exists
// for tests and for callers that assemble the map directly.
func NewStaticResolver(entries map[string]SignedDigest) *StaticResolver {
	cp := make(map[string]SignedDigest, len(entries))
	for k, v := range entries {
		cp[k] = v
	}
	return &StaticResolver{entries: cp}
}

// LoadStaticResolver reads the cosign image-signature policy file at
// path and returns a StaticResolver over its entries. The file is the
// JSON object {"images": {"<ref>": {"digest": "...", "signature": "..."}}}.
//
// It returns an error when the file cannot be read or parsed, so a
// missing or corrupt policy file is a startup failure rather than a
// resolver that silently denies every image.
func LoadStaticResolver(path string) (*StaticResolver, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read cosign policy file %s: %w", path, err)
	}
	var pf policyFile
	if err := json.Unmarshal(raw, &pf); err != nil {
		return nil, fmt.Errorf("parse cosign policy file %s: %w", path, err)
	}
	return NewStaticResolver(pf.Images), nil
}

// Resolve implements ImageDigestResolver. It returns the signing
// material recorded for imageRef, or an error when the policy file
// carries no entry for the image. An absent entry rejects the image
// fail-closed: an in-scope image with no recorded signature is treated
// as unsigned.
func (r *StaticResolver) Resolve(_ context.Context, imageRef string) (SignedDigest, error) {
	sd, ok := r.entries[imageRef]
	if !ok {
		return SignedDigest{}, fmt.Errorf("no signature recorded for image %q in the cosign policy file", imageRef)
	}
	return sd, nil
}
