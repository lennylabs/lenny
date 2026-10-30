// SPDX-License-Identifier: MIT

package kms

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"fmt"
	"sync"

	pkgkms "github.com/lennylabs/lenny/pkg/kms"
)

// signingState is the stub's §10.2 signing model: one random P-256 key
// per alias, minted on first use, plus fault injection and call counts.
// It has its own lock so a sign never nests inside Stub.mu.
type signingState struct {
	mu           sync.Mutex
	keys         map[string]*ecdsa.PrivateKey
	throttled    bool
	signErr      error
	publicKeyErr error
	signCalls    int
	pubCalls     int
}

func newSigningState() signingState {
	return signingState{keys: make(map[string]*ecdsa.PrivateKey)}
}

// SetSignThrottled makes every Sign return an error wrapping
// pkg/kms.ErrThrottled while v is true, modelling a provider quota.
func (s *Stub) SetSignThrottled(v bool) {
	s.signing.mu.Lock()
	defer s.signing.mu.Unlock()
	s.signing.throttled = v
}

// SetSignError makes every Sign return err (a hard failure) until it is
// cleared with nil. Throttling takes precedence when both are set.
func (s *Stub) SetSignError(err error) {
	s.signing.mu.Lock()
	defer s.signing.mu.Unlock()
	s.signing.signErr = err
}

// SetPublicKeyError makes every PublicKey return err until it is cleared
// with nil, modelling a key set that cannot load.
func (s *Stub) SetPublicKeyError(err error) {
	s.signing.mu.Lock()
	defer s.signing.mu.Unlock()
	s.signing.publicKeyErr = err
}

// SignCalls reports how many Sign calls reached the stub, including
// refused ones.
func (s *Stub) SignCalls() int {
	s.signing.mu.Lock()
	defer s.signing.mu.Unlock()
	return s.signing.signCalls
}

// PublicKeyCalls reports how many PublicKey calls reached the stub.
func (s *Stub) PublicKeyCalls() int {
	s.signing.mu.Lock()
	defer s.signing.mu.Unlock()
	return s.signing.pubCalls
}

// signingKey returns alias's key, minting it on first use.
func (s *Stub) signingKey(alias string) (*ecdsa.PrivateKey, error) {
	s.signing.mu.Lock()
	defer s.signing.mu.Unlock()
	if key, ok := s.signing.keys[alias]; ok {
		return key, nil
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("kms stub: generate signing key: %w", err)
	}
	s.signing.keys[alias] = key
	return key, nil
}

// signFault counts a Sign call and returns the injected fault, if any.
func (s *Stub) signFault() error {
	s.signing.mu.Lock()
	defer s.signing.mu.Unlock()
	s.signing.signCalls++
	if s.signing.throttled {
		return fmt.Errorf("kms stub: sign: %w", pkgkms.ErrThrottled)
	}
	return s.signing.signErr
}

// publicKeyFault counts a PublicKey call and returns the injected fault.
func (s *Stub) publicKeyFault() error {
	s.signing.mu.Lock()
	defer s.signing.mu.Unlock()
	s.signing.pubCalls++
	return s.signing.publicKeyErr
}

// Sign returns the ES256 r||s signature of digest under alias's key. The
// stub's alias faults (SetUnavailable, DenyAlias, DeleteAlias) apply, as
// do the sign faults above.
func (p *Provider) Sign(ctx context.Context, alias string, digest []byte) ([]byte, error) {
	if err := pkgkms.CheckDigest(digest); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := p.stub.signFault(); err != nil {
		return nil, err
	}
	if err := p.stub.checkFaults(alias); err != nil {
		return nil, err
	}
	key, err := p.stub.signingKey(alias)
	if err != nil {
		return nil, err
	}
	der, err := ecdsa.SignASN1(rand.Reader, key, digest)
	if err != nil {
		return nil, fmt.Errorf("kms stub: sign: %w", err)
	}
	return pkgkms.SignatureFromASN1(der)
}

// PublicKey returns the P-256 public key of alias's key.
func (p *Provider) PublicKey(ctx context.Context, alias string) (*ecdsa.PublicKey, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := p.stub.publicKeyFault(); err != nil {
		return nil, err
	}
	if err := p.stub.checkFaults(alias); err != nil {
		return nil, err
	}
	key, err := p.stub.signingKey(alias)
	if err != nil {
		return nil, err
	}
	return &key.PublicKey, nil
}
