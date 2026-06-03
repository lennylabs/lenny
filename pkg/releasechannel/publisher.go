// SPDX-License-Identifier: MIT

package releasechannel

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
)

// AvailabilitySLO is the §25.8 monthly availability target for the
// release-channel endpoint operated by the Lenny project. Operators
// running a re-signed mirror set their own SLO. The constant is
// documented here so the lenny-ops operability surface can read it
// without importing a magic number.
//
// §25.8 wording: "The releases.lenny.dev service is operated by the
// Lenny project with a target of 99.9% monthly availability."
const AvailabilitySLO = "99.9%"

// EndpointPath is the §25.8 path the publisher serves at. lenny-ops
// registers it on its HTTP surface. The path is fixed by the spec
// (`platform.upgradeChannel`, default `https://releases.lenny.dev/v1/-
// latest`); the publisher honors the same trailing segment so an
// internal mirror is a drop-in alternative to the canonical channel.
const EndpointPath = "/v1/latest"

// PublishContentType is the response Content-Type the publisher emits.
// JSON with the explicit charset matches the spec's "GET-only HTTP
// endpoint returning the JSON structure above" without leaving the
// charset ambiguous.
const PublishContentType = "application/json; charset=utf-8"

// PublisherOptions configures a Publisher.
type PublisherOptions struct {
	// Source is the manifest store the publisher reads from. Required.
	Source Source
	// Signer is the Ed25519 signer the publisher uses to compute the
	// X-Lenny-Release-Signature header. Required: the spec disallows
	// serving the manifest unsigned.
	Signer *Signer
}

// Publisher serves the §25.8 signed manifest. It is an http.Handler so
// the lenny-ops opsserver can mount it onto its own mux.
type Publisher struct {
	source Source
	signer *Signer
}

// NewPublisher builds a Publisher from opts. Both Source and Signer
// are required; an absent Signer would force the publisher to serve
// unsigned manifests, which §25.8 forbids.
func NewPublisher(opts PublisherOptions) (*Publisher, error) {
	if opts.Source == nil {
		return nil, errors.New("releasechannel: PublisherOptions.Source is required")
	}
	if opts.Signer == nil {
		return nil, errors.New("releasechannel: PublisherOptions.Signer is required (§25.8 requires every response carry an Ed25519 signature)")
	}
	return &Publisher{source: opts.Source, signer: opts.Signer}, nil
}

// ServeHTTP implements §25.8 GET /v1/latest. It honors the
// `?channel=stable|beta` query parameter (an unknown channel returns
// 400) and the `?currentVersion=` personalized filter: when the caller
// supplies a current version below the advertised release's
// `minUpgradeFrom` prerequisite, the publisher refuses to advertise the
// newer release and returns 404, the same "no upgrade available to you"
// signal the upgrade-check client treats as not-upgradeable. A channel
// with no advertised release returns 404. A signing or encoding failure
// returns 503. Successful responses carry the canonical JSON manifest
// and the X-Lenny-Release-Signature header.
func (p *Publisher) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed,
			"release-channel publisher is GET-only (§25.8)")
		return
	}

	channel, err := ParseChannel(r.URL.Query().Get("channel"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	manifest, err := p.source.Latest(channel)
	if err != nil {
		if errors.Is(err, ErrManifestNotFound) {
			writeError(w, http.StatusNotFound,
				fmt.Sprintf("no manifest published for channel %q", channel))
			return
		}
		writeError(w, http.StatusServiceUnavailable,
			"manifest source: "+err.Error())
		return
	}

	// §25.8 line 3410: refuse to advertise a release the caller cannot
	// upgrade to directly because its current version is below the
	// release's hard minUpgradeFrom prerequisite.
	if currentVersion := r.URL.Query().Get("currentVersion"); !manifest.MeetsMinUpgradeFrom(currentVersion) {
		writeError(w, http.StatusNotFound, fmt.Sprintf(
			"release %s requires a current version of at least %s; %s is below the prerequisite",
			manifest.Version, manifest.MinUpgradeFrom, currentVersion))
		return
	}

	// Marshal the manifest deterministically: the same input produces
	// the same bytes so cached signatures remain valid and clients can
	// compare bodies byte-for-byte.
	body, err := json.Marshal(manifest)
	if err != nil {
		writeError(w, http.StatusInternalServerError,
			"encode manifest: "+err.Error())
		return
	}

	env, err := p.signer.Sign(body)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable,
			"sign manifest: "+err.Error())
		return
	}

	w.Header().Set("Content-Type", PublishContentType)
	w.Header().Set(SignatureHeader, env.Marshal())
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// errorEnvelope is the body shape served on every non-2xx response.
// The envelope is intentionally small: clients consume the response
// via the operability surface, which translates it into a §25.2
// canonical error. Here the publisher is at the wire edge and emits a
// JSON body that the lenny-ops layer can wrap.
type errorEnvelope struct {
	Error string `json:"error"`
}

func writeError(w http.ResponseWriter, status int, msg string) {
	body, _ := json.Marshal(errorEnvelope{Error: msg})
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// VerifyResponse is a helper for tests and operability tooling: it
// takes the response body and the X-Lenny-Release-Signature header
// value, parses the envelope, and verifies it under the supplied
// Verifier. Returns the decoded Manifest on success.
//
// Consumers (lenny-ctl, the upgrade-check client) carry their own
// implementation that ties verification into their HTTP plumbing; this
// helper exists so the test suite does not duplicate the parse-then-
// verify logic per test.
func VerifyResponse(body []byte, signatureHeader string, v *Verifier) (Manifest, error) {
	env, err := ParseSignature(signatureHeader)
	if err != nil {
		return Manifest{}, fmt.Errorf("parse signature: %w", err)
	}
	if err := v.Verify(env, body); err != nil {
		return Manifest{}, fmt.Errorf("verify signature: %w", err)
	}
	// canonicalize body through json.Unmarshal: a verifier that has
	// passed must by definition have received the same byte sequence
	// the publisher signed, so the decode is purely structural.
	var m Manifest
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return Manifest{}, fmt.Errorf("decode manifest: %w", err)
	}
	return m, nil
}
