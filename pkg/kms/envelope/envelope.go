// SPDX-License-Identifier: MIT

// Package envelope is the §4 AES-256-GCM envelope-encryption helper. It
// encrypts a record under a fresh per-record data-encryption-key (DEK)
// and wraps that DEK with a KMS-backed key-encryption-key (KEK) through
// a kms.Provider. No plaintext DEK is ever persisted.
//
// The §4 model. For each record Seal generates a random 256-bit DEK,
// encrypts the record plaintext with AES-256-GCM under the DEK, asks
// the kms.Provider to wrap the DEK under a KEK alias, and discards the
// plaintext DEK. The stored form is a Sealed value carrying the
// KEK version, the wrapped DEK, the GCM nonce, and the ciphertext. A
// database dump exposes only those four fields. Open reverses the
// process: it asks the provider to unwrap the DEK, decrypts the
// ciphertext, and discards the recovered DEK.
//
// Tamper evidence. AES-GCM is authenticated. Any change to the
// ciphertext, the nonce, or the wrapped DEK makes Open fail rather
// than return wrong plaintext. The KEK alias is bound into the GCM
// additional data, so a Sealed value does not Open under a different
// alias even when both alias KEKs derive from the same seed.
//
// Key rotation. A Sealed value records the KEK version that wrapped
// its DEK, so the §4.9.1 rotation procedure can rotate the KEK while
// existing rows still Open. Reseal re-wraps a value's DEK under the
// current KEK version without changing the record ciphertext, which is
// the per-row step of the §4.9.1 re-encryption job.
package envelope

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/lennylabs/lenny/pkg/kms"
)

// Sealed is the §4 stored form of an envelope-encrypted record. Every
// field is safe to persist: the DEK appears only in its KEK-wrapped
// form, and AES-GCM authenticates Ciphertext and Nonce.
type Sealed struct {
	// KEKVersion is the version of the KEK alias that wrapped the DEK.
	// Open passes it back to the kms.Provider so a row encrypted under
	// a superseded KEK version still decrypts after a §4.9.1 rotation.
	KEKVersion int

	// WrappedDEK is the per-record data-encryption-key encrypted under
	// the KEK. It is opaque; Open hands it back to the kms.Provider
	// verbatim.
	WrappedDEK []byte

	// Nonce is the 12-byte AES-256-GCM nonce for the record
	// ciphertext. It is generated fresh per Seal call.
	Nonce []byte

	// Ciphertext is the record plaintext encrypted with AES-256-GCM
	// under the per-record DEK. It includes the GCM authentication
	// tag.
	Ciphertext []byte
}

// Sentinel errors. Callers compare with errors.Is.
var (
	// ErrCiphertextTampered is returned by Open when AES-GCM
	// authentication fails: the ciphertext, the nonce, or the wrapped
	// DEK was modified, truncated, or produced for a different KEK
	// alias.
	ErrCiphertextTampered = errors.New("envelope: ciphertext failed authentication")

	// ErrMalformed is returned when a Sealed value or its encoded form
	// is structurally invalid before any cryptographic check runs.
	ErrMalformed = errors.New("envelope: malformed sealed value")
)

// nonceSize is the AES-256-GCM nonce length. crypto/cipher's GCM uses
// a 12-byte nonce; the helper depends on that constant so encoding can
// be length-checked without constructing an AEAD.
const nonceSize = 12

// Cipher encrypts and decrypts records under the §4 envelope model. It
// holds a kms.Provider for the KEK operations and an alias naming the
// KEK to use. Construct one Cipher per KEK alias; the credential store
// uses a per-tenant alias and the Token Service uses a platform alias.
//
// Cipher is safe for concurrent use when its kms.Provider is.
type Cipher struct {
	kms   kms.Provider
	alias string
}

// New returns a Cipher that envelope-encrypts under the KEK named by
// alias, using provider for the KEK wrap and unwrap operations. It
// returns an error when provider is nil or alias is empty.
func New(provider kms.Provider, alias string) (*Cipher, error) {
	if provider == nil {
		return nil, errors.New("envelope: nil kms provider")
	}
	if alias == "" {
		return nil, errors.New("envelope: empty KEK alias")
	}
	return &Cipher{kms: provider, alias: alias}, nil
}

// Alias reports the KEK alias this Cipher encrypts under.
func (c *Cipher) Alias() string { return c.alias }

// Seal envelope-encrypts plaintext. It generates a fresh 256-bit DEK,
// encrypts plaintext with AES-256-GCM under the DEK, wraps the DEK
// under the Cipher's KEK alias through the kms.Provider, and returns a
// Sealed value. The plaintext DEK is zeroed before Seal returns; it
// never leaves the call.
//
// Sealing an empty plaintext is allowed: the result is a Sealed value
// with an empty-but-authenticated ciphertext, so an empty stored
// secret round-trips like any other value.
func (c *Cipher) Seal(ctx context.Context, plaintext []byte) (Sealed, error) {
	dek := make([]byte, kms.DEKSize)
	if _, err := rand.Read(dek); err != nil {
		return Sealed{}, fmt.Errorf("envelope: generate DEK: %w", err)
	}
	defer zero(dek)

	gcm, err := newGCM(dek)
	if err != nil {
		return Sealed{}, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return Sealed{}, fmt.Errorf("envelope: generate nonce: %w", err)
	}
	// The KEK alias is the GCM additional data: it binds the record
	// ciphertext to the alias so a ciphertext does not Open under a
	// different alias.
	ct := gcm.Seal(nil, nonce, plaintext, []byte(c.alias))

	wrapped, err := c.kms.WrapDEK(ctx, c.alias, dek)
	if err != nil {
		return Sealed{}, fmt.Errorf("envelope: wrap DEK: %w", err)
	}
	return Sealed{
		KEKVersion: wrapped.KEKVersion,
		WrappedDEK: wrapped.Ciphertext,
		Nonce:      nonce,
		Ciphertext: ct,
	}, nil
}

// Open reverses Seal. It asks the kms.Provider to unwrap the DEK at
// the KEK version recorded on the Sealed value, decrypts the
// ciphertext with AES-256-GCM under the recovered DEK, and returns the
// plaintext. The recovered DEK is zeroed before Open returns.
//
// Open returns ErrCiphertextTampered when AES-GCM authentication
// fails, ErrMalformed when the Sealed value is structurally invalid,
// and a wrapped kms error (kms.ErrUnwrap, kms.ErrUnknownKEKVersion,
// etc.) when the DEK cannot be unwrapped.
func (c *Cipher) Open(ctx context.Context, s Sealed) ([]byte, error) {
	if s.KEKVersion < 1 {
		return nil, fmt.Errorf("%w: KEK version %d", ErrMalformed, s.KEKVersion)
	}
	if len(s.WrappedDEK) == 0 {
		return nil, fmt.Errorf("%w: empty wrapped DEK", ErrMalformed)
	}
	if len(s.Nonce) != nonceSize {
		return nil, fmt.Errorf("%w: nonce is %d bytes, want %d", ErrMalformed, len(s.Nonce), nonceSize)
	}
	dek, err := c.kms.UnwrapDEK(ctx, c.alias, kms.WrappedDEK{
		KEKVersion: s.KEKVersion,
		Ciphertext: s.WrappedDEK,
	})
	if err != nil {
		return nil, fmt.Errorf("envelope: unwrap DEK: %w", err)
	}
	defer zero(dek)

	gcm, err := newGCM(dek)
	if err != nil {
		return nil, err
	}
	pt, err := gcm.Open(nil, s.Nonce, s.Ciphertext, []byte(c.alias))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCiphertextTampered, err)
	}
	return pt, nil
}

// Reseal re-wraps the DEK of an existing Sealed value under the
// current KEK version without re-encrypting the record. It is the
// per-row step of the §4.9.1 re-encryption job: after a KEK rotation
// the job calls Reseal on every row still at a superseded
// KEKVersion. Reseal unwraps the DEK under its recorded version and
// re-wraps it under the current version; the record nonce and
// ciphertext are unchanged. A value already at the current KEK
// version is returned unchanged, which makes the §4.9.1 job
// idempotent.
func (c *Cipher) Reseal(ctx context.Context, s Sealed) (Sealed, error) {
	current, err := c.kms.CurrentKEKVersion(ctx, c.alias)
	if err != nil {
		return Sealed{}, fmt.Errorf("envelope: read current KEK version: %w", err)
	}
	if s.KEKVersion == current {
		return s, nil
	}
	dek, err := c.kms.UnwrapDEK(ctx, c.alias, kms.WrappedDEK{
		KEKVersion: s.KEKVersion,
		Ciphertext: s.WrappedDEK,
	})
	if err != nil {
		return Sealed{}, fmt.Errorf("envelope: unwrap DEK for reseal: %w", err)
	}
	defer zero(dek)

	rewrapped, err := c.kms.WrapDEK(ctx, c.alias, dek)
	if err != nil {
		return Sealed{}, fmt.Errorf("envelope: rewrap DEK: %w", err)
	}
	return Sealed{
		KEKVersion: rewrapped.KEKVersion,
		WrappedDEK: rewrapped.Ciphertext,
		Nonce:      s.Nonce,
		Ciphertext: s.Ciphertext,
	}, nil
}

// Encode serializes a Sealed value into a single self-describing byte
// slice for storage in a single column. The layout is:
//
//	magic   1 byte   0x01 envelope format version
//	kekver  4 bytes  big-endian KEK version
//	wlen    4 bytes  big-endian wrapped-DEK length
//	wrapped wlen     wrapped DEK bytes
//	nonce   12 bytes GCM nonce
//	ct      rest     record ciphertext
//
// A store that prefers discrete columns can persist the Sealed fields
// directly instead; Encode/Decode exist for a single-column layout.
func Encode(s Sealed) ([]byte, error) {
	if s.KEKVersion < 1 || s.KEKVersion > 0x7fffffff {
		return nil, fmt.Errorf("%w: KEK version %d out of range", ErrMalformed, s.KEKVersion)
	}
	if len(s.Nonce) != nonceSize {
		return nil, fmt.Errorf("%w: nonce is %d bytes, want %d", ErrMalformed, len(s.Nonce), nonceSize)
	}
	if len(s.WrappedDEK) == 0 || len(s.WrappedDEK) > 0x7fffffff {
		return nil, fmt.Errorf("%w: wrapped DEK length %d out of range", ErrMalformed, len(s.WrappedDEK))
	}
	out := make([]byte, 0, 1+4+4+len(s.WrappedDEK)+nonceSize+len(s.Ciphertext))
	out = append(out, encodeMagic)
	out = binary.BigEndian.AppendUint32(out, uint32(s.KEKVersion))
	out = binary.BigEndian.AppendUint32(out, uint32(len(s.WrappedDEK)))
	out = append(out, s.WrappedDEK...)
	out = append(out, s.Nonce...)
	out = append(out, s.Ciphertext...)
	return out, nil
}

// Decode reverses Encode. It returns ErrMalformed when buf does not
// match the Encode layout.
func Decode(buf []byte) (Sealed, error) {
	const headerLen = 1 + 4 + 4
	if len(buf) < headerLen {
		return Sealed{}, fmt.Errorf("%w: %d bytes is shorter than the header", ErrMalformed, len(buf))
	}
	if buf[0] != encodeMagic {
		return Sealed{}, fmt.Errorf("%w: bad magic byte 0x%02x", ErrMalformed, buf[0])
	}
	kekVersion := int(binary.BigEndian.Uint32(buf[1:5]))
	wlen := int(binary.BigEndian.Uint32(buf[5:9]))
	if kekVersion < 1 {
		return Sealed{}, fmt.Errorf("%w: KEK version %d", ErrMalformed, kekVersion)
	}
	rest := buf[headerLen:]
	if wlen == 0 {
		return Sealed{}, fmt.Errorf("%w: zero-length wrapped DEK", ErrMalformed)
	}
	if len(rest) < wlen+nonceSize {
		return Sealed{}, fmt.Errorf("%w: %d trailing bytes, need at least %d",
			ErrMalformed, len(rest), wlen+nonceSize)
	}
	wrapped := append([]byte(nil), rest[:wlen]...)
	nonce := append([]byte(nil), rest[wlen:wlen+nonceSize]...)
	ct := append([]byte(nil), rest[wlen+nonceSize:]...)
	return Sealed{
		KEKVersion: kekVersion,
		WrappedDEK: wrapped,
		Nonce:      nonce,
		Ciphertext: ct,
	}, nil
}

// encodeMagic is the first byte of an Encode-produced blob. It is a
// format version: a future envelope format bumps it so Decode can
// reject or branch on an unknown layout.
const encodeMagic = 0x01

// zero overwrites b with zeros. It is best-effort defense in depth for
// plaintext DEK material; Go's garbage collector may still have copied
// the slice, so zeroing is not a hard guarantee.
func zero(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
