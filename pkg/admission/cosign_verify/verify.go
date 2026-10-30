// SPDX-License-Identifier: MIT

// Package cosign_verify implements the pure-decision logic of the
// lenny-cosign-verify ValidatingAdmissionWebhook per spec §5.2 ("Image
// supply chain controls"), §13.1, and §18.5. The webhook is deployed
// with failurePolicy: Fail and intercepts CREATE on Pod resources in
// agent namespaces.
//
// The §5.2 rule. Agent-pod container images must carry a valid
// cosign/Sigstore signature before the pod is admitted. The webhook
// resolves, for every container image on the pod, whether the image is
// in scope (its registry matches the deployer-configured verified
// prefix list) and, if so, requires a signature the configured trust
// anchor accepts. An image whose registry is not in the verified
// prefix list is admitted unchecked: the prefix list is the boundary
// of the deployer's signing program, and §5.2's companion control
// ("only images from deployer-configured trusted registries are
// admitted") is enforced separately by the registry-allowlist gate.
//
// Fail-closed. The decision logic admits an in-scope image only when
// the Verifier returns a nil error. A Verifier error — an absent
// signature, a signature that does not chain to the trust anchor, or a
// backend outage — rejects. A webhook outage rejects via
// failurePolicy: Fail so an unsigned image cannot reach a node while
// the webhook is down. §16.5 alerts on webhook unavailability with
// CosignWebhookUnavailable.
//
// The signature check itself is delegated to a Verifier so the
// cryptographic backend is swappable and the decision logic is
// unit-testable with a fake. The default backend is the keyed ECDSA
// verifier in publickey.go.
//
// The decision logic is split from the webhook HTTP/AdmissionReview
// transport so it can be unit-tested without the controller-runtime
// stack.
package cosign_verify

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// RejectCode labels every denial this webhook returns. A Kubernetes
// admission denial carries a free-text status message rather than a
// §15.1 REST error code; this constant is the webhook's own machine-
// readable label, embedded in that message for log and alert matching.
const RejectCode = "IMAGE_SIGNATURE_INVALID"

// Verifier checks that an image carries a signature the deployment's
// trust anchor accepts. The webhook adapter constructs the concrete
// Verifier from the deployment's verification configuration (a trusted
// public key or a keyless identity policy) at binary startup; tests
// substitute a fake.
//
// Verify returns nil when imageRef carries an acceptable signature and
// a non-nil error otherwise. The error is relayed verbatim into the
// admission rejection message, so it must not leak key material. A
// backend that cannot reach its trust store returns an error: the
// decision logic treats every non-nil error as fail-closed, never
// admitting an image whose signature it could not confirm.
type Verifier interface {
	Verify(ctx context.Context, imageRef string) error
}

// Config is the deployment's image-verification policy.
type Config struct {
	// VerifiedRegistries is the deployer-configured list of
	// image-registry prefixes whose images must carry a signature. An
	// image reference is in scope when it has one of these strings as a
	// prefix. An empty list places no image in scope, which admits
	// every pod unchecked; the webhook adapter rejects an empty list at
	// startup so a misconfiguration cannot silently disable the gate.
	VerifiedRegistries []string
}

// Request is the input to Decide: the container images of one admitted
// pod plus the deployment's verification policy.
type Request struct {
	// PodName + Namespace identify the pod, used in rejection messages.
	PodName   string
	Namespace string

	// Images is the full set of container images on the pod spec:
	// init containers, regular containers, and ephemeral containers.
	// The webhook adapter flattens all three into this slice.
	Images []string

	// Config is the deployment's image-verification policy.
	Config Config
}

// Decision is the admission outcome.
type Decision struct {
	// Allowed is true when every in-scope image on the pod carries an
	// acceptable signature.
	Allowed bool
	// Reason is the rejection message relayed to the offending client
	// when Allowed is false; empty when Allowed is true.
	Reason string
	// Code mirrors the HTTP status the webhook surfaces: 200 on allow,
	// 403 on rejection.
	Code int
}

// Decide applies the §5.2 image-signature admission rule to a pod.
//
// For every container image on the pod it determines whether the image
// is in scope (its registry matches a VerifiedRegistries prefix). An
// in-scope image is passed to the Verifier; a non-nil result rejects
// the whole pod with the §5.2 message. An out-of-scope image is
// admitted unchecked. A pod whose every in-scope image verifies — or
// that carries no in-scope image — is admitted.
//
// The check is fail-closed: any in-scope image the Verifier cannot
// confirm rejects, never admits. Images are evaluated in sorted order
// so the rejection message is deterministic for a given pod.
func Decide(ctx context.Context, v Verifier, r Request) Decision {
	images := dedupeSorted(r.Images)
	for _, img := range images {
		if !inScope(img, r.Config.VerifiedRegistries) {
			continue
		}
		if err := v.Verify(ctx, img); err != nil {
			return reject(fmt.Sprintf(
				"pod %s/%s image %q failed cosign signature verification: %s",
				r.Namespace, r.PodName, img, err.Error(),
			))
		}
	}
	return Decision{Allowed: true, Code: 200}
}

// inScope reports whether imageRef is governed by the signing program:
// its reference has one of the verified-registry prefixes. The match
// is a plain string prefix because an OCI reference begins with its
// registry host and repository path (for example
// "ghcr.io/lennylabs/"), which the deployer configures verbatim.
func inScope(imageRef string, verified []string) bool {
	for _, prefix := range verified {
		if prefix != "" && strings.HasPrefix(imageRef, prefix) {
			return true
		}
	}
	return false
}

// dedupeSorted returns the distinct image references in sorted order so
// a pod that lists the same image on several containers is verified
// once and the rejection message is deterministic.
func dedupeSorted(images []string) []string {
	seen := make(map[string]struct{}, len(images))
	out := make([]string, 0, len(images))
	for _, img := range images {
		if img == "" {
			continue
		}
		if _, ok := seen[img]; ok {
			continue
		}
		seen[img] = struct{}{}
		out = append(out, img)
	}
	sort.Strings(out)
	return out
}

// reject builds a denied Decision carrying the §5.2 rejection code and
// message.
func reject(msg string) Decision {
	return Decision{
		Allowed: false,
		Code:    403,
		Reason:  fmt.Sprintf("%s: %s", RejectCode, msg),
	}
}
