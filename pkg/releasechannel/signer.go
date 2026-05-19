// SPDX-License-Identifier: MIT

package releasechannel

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

// SignatureHeader is the §25.8 response header carrying the publisher's
// detached Ed25519 signature. Clients verify the signature against the
// Lenny release-channel public key compiled into lenny-ops, or against
// the operator-held public key from `platform.releaseChannel.publicKey-
// Path` when the channel is a re-signed mirror.
const SignatureHeader = "X-Lenny-Release-Signature"

// SignatureEnvelope is the on-the-wire format of the X-Lenny-Release-
// Signature header value. The envelope carries the algorithm name, the
// signing key's identifier (so a verifier can look up the right
// public key during a rotation overlap), and the base64-encoded
// Ed25519 signature itself.
//
// The serialized form is "v1;keyId=<id>;alg=ed25519;sig=<base64>". §25.8
// states only that the header carries an Ed25519 signature and that the
// public key is compiled into lenny-ops; the envelope's exact tokens
// are not enumerated in the spec. The implementation pins a versioned,
// semi-colon-separated form so a future v2 envelope can coexist with v1
// during an algorithm rollover.
//
// TODO(spec-ambiguity): §25.8 leaves the precise envelope format
// implicit. If a future spec revision pins a canonical format (for
// example COSE_Sign1 or JWS), update Marshal and ParseSignature here
// and re-key consumers. Until then v1 is the format the publisher
// produces and the verifier accepts.
type SignatureEnvelope struct {
	// KeyID identifies the signing key. The publisher's Signer fills
	// it from the active key during rotation.
	KeyID string
	// Signature is the raw Ed25519 signature bytes (64 bytes).
	Signature []byte
}

// envelopeVersion is the version token prefixing every serialized
// envelope. A verifier rejects every other prefix so a forward-
// incompatible envelope cannot reach a v1-aware client unsigned-by-its-
// own-lights.
const envelopeVersion = "v1"

// Marshal returns the envelope's wire form.
func (e SignatureEnvelope) Marshal() string {
	return fmt.Sprintf("%s;keyId=%s;alg=ed25519;sig=%s",
		envelopeVersion, e.KeyID,
		base64.StdEncoding.EncodeToString(e.Signature),
	)
}

// ParseSignature decodes a SignatureEnvelope from its wire form. It is
// the inverse of Marshal; verifiers use it to look the signing key up
// from KeyID before calling ed25519.Verify.
func ParseSignature(s string) (SignatureEnvelope, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return SignatureEnvelope{}, errors.New("releasechannel: empty signature header")
	}
	parts := strings.Split(s, ";")
	if len(parts) < 1 || parts[0] != envelopeVersion {
		return SignatureEnvelope{}, fmt.Errorf("releasechannel: signature envelope must start with %q (got %q)", envelopeVersion, parts[0])
	}
	var env SignatureEnvelope
	for _, p := range parts[1:] {
		k, v, ok := strings.Cut(p, "=")
		if !ok {
			return SignatureEnvelope{}, fmt.Errorf("releasechannel: malformed envelope token %q", p)
		}
		switch k {
		case "keyId":
			env.KeyID = v
		case "alg":
			if v != "ed25519" {
				return SignatureEnvelope{}, fmt.Errorf("releasechannel: unsupported signing algorithm %q", v)
			}
		case "sig":
			sig, err := base64.StdEncoding.DecodeString(v)
			if err != nil {
				return SignatureEnvelope{}, fmt.Errorf("releasechannel: decode signature: %w", err)
			}
			env.Signature = sig
		default:
			// Forward-compatible: ignore unknown tokens so a future
			// minor extension does not break v1 verifiers.
		}
	}
	if env.KeyID == "" {
		return SignatureEnvelope{}, errors.New("releasechannel: signature envelope missing keyId")
	}
	if len(env.Signature) == 0 {
		return SignatureEnvelope{}, errors.New("releasechannel: signature envelope missing sig")
	}
	return env, nil
}

// Key is an Ed25519 signing key plus its operator-assigned identifier.
// The KeyID is opaque to this package: lenny-ops mints a stable
// identifier per key at key-generation time (a UUID, a date stamp, or
// the operator's KMS key name).
type Key struct {
	// ID is the key's stable identifier. It appears in the signature
	// envelope so a verifier can look the public half up during a
	// rotation overlap.
	ID string
	// Private is the Ed25519 private key. Required on the signer side;
	// the verifier side carries only Public.
	Private ed25519.PrivateKey
	// Public is the Ed25519 public key. A verifier-only Key carries
	// just this field.
	Public ed25519.PublicKey
}

// Validate reports an error when the key is missing required fields
// for the role it is being constructed for. signer = true requires a
// non-empty Private half; signer = false requires Public.
func (k Key) Validate(signer bool) error {
	if k.ID == "" {
		return errors.New("releasechannel: key ID is empty")
	}
	if signer {
		if len(k.Private) != ed25519.PrivateKeySize {
			return fmt.Errorf("releasechannel: private key must be %d bytes, got %d", ed25519.PrivateKeySize, len(k.Private))
		}
	}
	pub := k.Public
	if signer && len(pub) == 0 {
		pub = k.Private.Public().(ed25519.PublicKey)
	}
	if len(pub) != ed25519.PublicKeySize {
		return fmt.Errorf("releasechannel: public key must be %d bytes, got %d", ed25519.PublicKeySize, len(pub))
	}
	return nil
}

// Signer produces Ed25519 signatures over the canonical manifest body
// served by the publisher. It honors a rotation-overlap window: at any
// instant exactly one key signs new responses (the Current key) while
// a Previous key, if set, remains usable for verification. The
// rotation overlap is the §25.8 "response-signing key rotation
// procedure with overlap window".
//
// Rotation lifecycle. An operator about to retire a key:
//
//  1. Generates a new keypair and publishes its public half to
//     consumers (the air-gap mirror or the lenny-ops compiled-in
//     pubkey set).
//  2. Calls Rotate, which moves the existing Current key to Previous
//     and adopts the new key as Current. New responses are signed by
//     the new key; consumers still trusting only the previous public
//     key continue to verify cached responses successfully because
//     Previous is still considered valid by the matching Verifier.
//  3. After the overlap window — operator's choice, the spec calls
//     for "long enough that every consumer has refreshed" — calls
//     RetirePrevious to drop the previous key entirely. After that
//     point a response signed by the previous key fails verification.
//
// A Signer with no Current key is non-functional: Sign returns
// ErrNoSigningKey. lenny-ops fails to start when no signing key is
// configured.
type Signer struct {
	current  *Key
	previous *Key
}

// ErrNoSigningKey is returned by Sign when the Signer has no Current
// key. lenny-ops translates this into a 503 on the publisher endpoint
// so the SLO sees the outage rather than the operator silently serving
// unsigned responses.
var ErrNoSigningKey = errors.New("releasechannel: signer has no current key")

// NewSigner constructs a Signer with current as the active signing
// key. previous may be nil to construct a Signer outside a rotation
// window. Both keys are validated for the signer role; a malformed key
// is reported as an error rather than silently rejecting every signing
// attempt later.
func NewSigner(current Key, previous *Key) (*Signer, error) {
	if err := current.Validate(true); err != nil {
		return nil, fmt.Errorf("releasechannel: current key: %w", err)
	}
	s := &Signer{current: &current}
	if previous != nil {
		// The previous key still verifies signatures the publisher
		// emitted before the rotation, but it cannot produce new ones,
		// so its private half is optional.
		if err := previous.Validate(false); err != nil {
			return nil, fmt.Errorf("releasechannel: previous key: %w", err)
		}
		p := *previous
		s.previous = &p
	}
	return s, nil
}

// Sign produces the SignatureEnvelope for body. body is the exact
// bytes the publisher will write to the response — typically the
// canonical JSON encoding of a Manifest. The signature is detached;
// the publisher emits it in the X-Lenny-Release-Signature header so
// the body bytes themselves are unchanged.
func (s *Signer) Sign(body []byte) (SignatureEnvelope, error) {
	if s.current == nil {
		return SignatureEnvelope{}, ErrNoSigningKey
	}
	sig := ed25519.Sign(s.current.Private, body)
	return SignatureEnvelope{
		KeyID:     s.current.ID,
		Signature: sig,
	}, nil
}

// CurrentKeyID returns the identifier of the currently-active signing
// key. lenny-ops surfaces this in operability reports so an operator
// can confirm which key the publisher is using without inspecting the
// header.
func (s *Signer) CurrentKeyID() string {
	if s.current == nil {
		return ""
	}
	return s.current.ID
}

// PreviousKeyID returns the identifier of the rotated-out key, or the
// empty string when no rotation is in progress.
func (s *Signer) PreviousKeyID() string {
	if s.previous == nil {
		return ""
	}
	return s.previous.ID
}

// Rotate begins a rotation overlap window. The Signer's current key
// becomes the previous key and next becomes the new current key.
// Subsequent calls to Sign use next; a matching Verifier accepts
// signatures from either key for as long as previous is set.
//
// Rotating without an existing current key is allowed (it adopts next
// as the current key with no previous), so an operator can bootstrap a
// new install with a single Rotate call.
func (s *Signer) Rotate(next Key) error {
	if err := next.Validate(true); err != nil {
		return fmt.Errorf("releasechannel: rotation target: %w", err)
	}
	if s.current != nil {
		prev := *s.current
		// The verifier side never signs with the previous key, so we
		// drop the private half — it stays in operator custody if a
		// future re-promotion is needed.
		prev.Private = nil
		if prev.Public == nil {
			// Derive the public key from the previous private half so
			// the rotation overlap still verifies signatures the old
			// key produced before Rotate was called.
			prev.Public = s.current.Private.Public().(ed25519.PublicKey)
		}
		s.previous = &prev
	}
	c := next
	s.current = &c
	return nil
}

// RetirePrevious closes the rotation overlap window: the previous key
// is dropped and no longer verifies. Operators call this once the
// rotation window has elapsed and every consumer has refreshed against
// the new key.
//
// Calling RetirePrevious when no rotation is in progress is a no-op.
func (s *Signer) RetirePrevious() {
	s.previous = nil
}

// PublicKeySet returns the public keys a Verifier should trust for
// this Signer at the current moment: the current key plus the
// previous key when a rotation is in progress. Operators publishing
// the channel's trust anchor copy this set into their consumers.
func (s *Signer) PublicKeySet() []Key {
	out := make([]Key, 0, 2)
	if s.current != nil {
		k := *s.current
		k.Private = nil
		if k.Public == nil {
			k.Public = s.current.Private.Public().(ed25519.PublicKey)
		}
		out = append(out, k)
	}
	if s.previous != nil {
		out = append(out, *s.previous)
	}
	return out
}

// Verifier is the consumer-side counterpart of Signer. It accepts a
// SignatureEnvelope plus the signed body and returns nil when the
// signature verifies against one of the trust-anchored public keys.
//
// During a rotation overlap a Verifier built from PublicKeySet
// accepts both the new and the old signing key, so a consumer that has
// cached a response signed before the rotation continues to verify
// successfully until the operator calls RetirePrevious and re-issues
// the public-key bundle. After that point the consumer's bundle no
// longer carries the old key and a stale cached response fails — the
// rotation is then complete.
type Verifier struct {
	keys map[string]ed25519.PublicKey
}

// NewVerifier returns a Verifier trusting the supplied keys. Each key
// is validated for the verifier role (a non-empty Public half) and
// keyed by ID for the envelope lookup.
func NewVerifier(keys []Key) (*Verifier, error) {
	if len(keys) == 0 {
		return nil, errors.New("releasechannel: verifier needs at least one trusted key")
	}
	out := &Verifier{keys: make(map[string]ed25519.PublicKey, len(keys))}
	for i, k := range keys {
		if err := k.Validate(false); err != nil {
			return nil, fmt.Errorf("releasechannel: verifier key %d: %w", i, err)
		}
		pub := k.Public
		if pub == nil {
			pub = k.Private.Public().(ed25519.PublicKey)
		}
		out.keys[k.ID] = pub
	}
	return out, nil
}

// ErrUnknownKey is returned by Verify when the envelope's KeyID is not
// in the verifier's trust set. lenny-ctl translates this into the
// operator-facing "the responding manifest publisher signed with an
// unrecognized key — refresh your trust bundle" message.
var ErrUnknownKey = errors.New("releasechannel: unknown signing key")

// ErrSignatureMismatch is returned by Verify when the envelope's
// signature does not match body under the known public key. This is
// the cryptographic-failure path: a body that was tampered with in
// transit, a signature that was generated against a different body,
// or a body that was signed by a key the verifier does not trust.
var ErrSignatureMismatch = errors.New("releasechannel: signature does not match body")

// Verify reports whether env's signature is valid over body. It looks
// the trust-anchored public key up by env.KeyID and runs
// ed25519.Verify. The function returns nil on success, ErrUnknownKey
// when the keyId is not in the trust set, and ErrSignatureMismatch on
// a cryptographic mismatch.
func (v *Verifier) Verify(env SignatureEnvelope, body []byte) error {
	pub, ok := v.keys[env.KeyID]
	if !ok {
		return fmt.Errorf("%w: keyId=%s", ErrUnknownKey, env.KeyID)
	}
	if !ed25519.Verify(pub, body, env.Signature) {
		return ErrSignatureMismatch
	}
	return nil
}

// TrustsKey reports whether v trusts the named key ID. Used by tests
// and operability reports to introspect the trust set without
// exercising the cryptographic path.
func (v *Verifier) TrustsKey(keyID string) bool {
	_, ok := v.keys[keyID]
	return ok
}
