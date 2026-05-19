// SPDX-License-Identifier: MIT

// Package registry_digest implements the pure-decision logic of the
// lenny-registry-digest ValidatingAdmissionWebhook per spec §13.1 and
// §18.33. The webhook is deployed with failurePolicy: Fail and runs in
// front of every CREATE on Pod resources in agent namespaces.
//
// The rule. When `platform.registry.requireDigest: true`, every
// container image reference on an agent pod must be digest-pinned
// (carry an `@sha256:...` digest). §13.1 calls this out as the
// admission-time enforcement layered atop the Phase 14 release-pipeline
// cosign work: §25.8's air-gap guidance requires `requireDigest: true`
// because a mirror that preserves tags but not digests would otherwise
// allow a re-tagged image to be admitted.
//
// When `requireDigest` is false the webhook admits every pod regardless
// of its image references; the deployer's tag-based references remain
// usable for development. The flag is a per-installation Helm value
// surfaced through `platform.registry.requireDigest`.
//
// The check is fail-closed: a pod the webhook cannot decode is rejected,
// matching the cosign-verify webhook pattern. Every in-pod container
// — init, regular, and ephemeral — is evaluated; a single non-digest
// reference rejects the whole pod with a list of the offending
// references so the operator can see every image to fix.
//
// The decision logic is split from the webhook HTTP/AdmissionReview
// transport so it can be unit-tested without the controller-runtime
// stack.
package registry_digest

import (
	"fmt"
	"sort"
	"strings"
)

// RejectCode labels every denial this webhook returns. A Kubernetes
// admission denial carries a free-text status message; this constant
// is the webhook's machine-readable label for log and alert matching.
// Mirroring the §13.1 admission-rejection convention (the cosign-verify
// webhook uses IMAGE_SIGNATURE_INVALID; the host-sharing prohibition
// uses POD_SPEC_HOST_SHARING_FORBIDDEN), this gate emits
// POD_IMAGE_DIGEST_REQUIRED.
const RejectCode = "POD_IMAGE_DIGEST_REQUIRED"

// digestMarker is the substring an OCI image reference must contain to
// satisfy the digest-pinned requirement. §13.1 uses sha256 exclusively
// in v1; other digest algorithms are not recognised here. The check is
// the same string-prefix test pkg/common/registry uses for the
// ImageResolver enforcement on the resolution side.
const digestMarker = "@sha256:"

// Config is the deployer's registry-digest enforcement policy.
type Config struct {
	// RequireDigest gates the whole check. When false, Decide admits
	// every pod regardless of its image references. The flag is
	// surfaced through `platform.registry.requireDigest` in the Helm
	// chart and propagated to the webhook via a startup argument.
	RequireDigest bool
}

// Request is the input to Decide.
type Request struct {
	// PodName and Namespace identify the pod for the rejection
	// message. Optional — Decide falls back to "<unnamed>" when both
	// are empty.
	PodName   string
	Namespace string

	// Images is the full set of container image references on the
	// pod: init containers, regular containers, and ephemeral
	// containers. The webhook adapter flattens all three into this
	// slice. Empty entries are ignored — an empty image string can
	// only mean the API server has not yet defaulted the image, which
	// a later admission stage will catch.
	Images []string

	// Config is the deployment's digest-pinning policy.
	Config Config
}

// Decision is the admission outcome.
type Decision struct {
	// Allowed is true when every image reference satisfies the
	// digest-pinning requirement (or when RequireDigest is false).
	Allowed bool
	// Reason is the rejection message relayed to the offending client
	// when Allowed is false; empty when Allowed is true.
	Reason string
	// Code mirrors the HTTP status the webhook surfaces: 200 on
	// allow, 403 on rejection.
	Code int
	// OffendingImages lists every image reference that lacked an
	// @sha256: digest, in sorted order so a pod listing the same
	// non-digest reference on several containers is reported once
	// and the rejection message is deterministic. Empty when
	// Allowed is true.
	OffendingImages []string
}

// Decide applies the §13.1 platform.registry.requireDigest rule to a
// pod. When RequireDigest is false the pod is admitted unchecked.
// Otherwise every non-empty image is required to carry an `@sha256:`
// digest; a pod with at least one non-digest reference is rejected
// with the full list of offenders.
//
// The check is intentionally local: it does not consult the registry,
// it does not resolve tags, it only inspects the string form of each
// image reference. A digest-pinned reference must include the
// `@sha256:` marker (for example `ghcr.io/lennylabs/lenny-gateway@-
// sha256:abcd...`); a tag-only reference (for example
// `ghcr.io/lennylabs/lenny-gateway:1.5.0`) is rejected.
func Decide(r Request) Decision {
	if !r.Config.RequireDigest {
		return Decision{Allowed: true, Code: 200}
	}
	offenders := nonDigestImages(r.Images)
	if len(offenders) == 0 {
		return Decision{Allowed: true, Code: 200}
	}
	name := podLabel(r.Namespace, r.PodName)
	return Decision{
		Allowed:         false,
		Code:            403,
		OffendingImages: offenders,
		Reason: fmt.Sprintf(
			"%s: pod %s declares container image reference(s) without an @sha256: digest while platform.registry.requireDigest is true: %s",
			RejectCode, name, strings.Join(offenders, ", "),
		),
	}
}

// nonDigestImages returns the distinct image references that lack an
// @sha256: digest, sorted so the rejection message is deterministic
// for a given pod.
func nonDigestImages(images []string) []string {
	seen := make(map[string]struct{}, len(images))
	var out []string
	for _, img := range images {
		img = strings.TrimSpace(img)
		if img == "" {
			continue
		}
		if strings.Contains(img, digestMarker) {
			continue
		}
		if _, dup := seen[img]; dup {
			continue
		}
		seen[img] = struct{}{}
		out = append(out, img)
	}
	sort.Strings(out)
	return out
}

// podLabel renders namespace/name for the rejection message, falling
// back to "<unnamed>" when both are empty (an admission request for a
// pod whose metadata has not been populated by the API server).
func podLabel(namespace, name string) string {
	switch {
	case namespace != "" && name != "":
		return namespace + "/" + name
	case name != "":
		return name
	case namespace != "":
		return namespace + "/<unnamed>"
	default:
		return "<unnamed>"
	}
}
