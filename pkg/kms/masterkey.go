// SPDX-License-Identifier: MIT

package kms

import (
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// LoadOrCreateMasterKey returns the §17.4 file-backed soft-HSM master
// key at path, generating a fresh DEKSize-byte random seed on first use.
// The seed is the root from which a Local provider derives every KEK, so
// a persisted file lets envelope-encrypted state survive a process
// restart — the §17.4 "lenny down without --purge preserves state"
// guarantee. The key file is written 0600 under a 0700 parent directory.
//
// A pre-existing file shorter than DEKSize bytes is a fatal
// misconfiguration: returning an error rather than padding it prevents a
// truncated or corrupted key from silently weakening the KEK. A read
// error other than "not found" is likewise surfaced rather than masked
// by minting a fresh key, which would orphan every ciphertext written
// under the old key.
//
// spec: §17.4 line 163 — embedded soft-HSM with a file-backed master
// key at ~/.lenny/kms/master.key.
func LoadOrCreateMasterKey(path string) ([]byte, error) {
	if path == "" {
		return nil, errors.New("kms: master-key path is empty")
	}
	seed, err := os.ReadFile(path)
	if err == nil {
		if len(seed) < DEKSize {
			return nil, fmt.Errorf("kms: master key %q is %d bytes; need at least %d (delete it to regenerate, "+
				"but every ciphertext written under it becomes unreadable)", path, len(seed), DEKSize)
		}
		return seed, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("kms: read master key %q: %w", path, err)
	}
	if mkErr := os.MkdirAll(filepath.Dir(path), 0o700); mkErr != nil {
		return nil, fmt.Errorf("kms: create master-key directory: %w", mkErr)
	}
	fresh := make([]byte, DEKSize)
	if _, rerr := rand.Read(fresh); rerr != nil {
		return nil, fmt.Errorf("kms: generate master key: %w", rerr)
	}
	// O_EXCL so a concurrent creator (two stack processes racing on first
	// boot) does not clobber an already-written key; on that race we fall
	// back to reading the winner's key.
	f, oerr := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if oerr != nil {
		if errors.Is(oerr, os.ErrExist) {
			seed, rerr := os.ReadFile(path)
			if rerr != nil {
				return nil, fmt.Errorf("kms: read master key %q after create race: %w", path, rerr)
			}
			if len(seed) < DEKSize {
				return nil, fmt.Errorf("kms: master key %q is %d bytes; need at least %d", path, len(seed), DEKSize)
			}
			return seed, nil
		}
		return nil, fmt.Errorf("kms: create master key %q: %w", path, oerr)
	}
	if _, werr := f.Write(fresh); werr != nil {
		_ = f.Close()
		return nil, fmt.Errorf("kms: write master key %q: %w", path, werr)
	}
	if cerr := f.Close(); cerr != nil {
		return nil, fmt.Errorf("kms: close master key %q: %w", path, cerr)
	}
	return fresh, nil
}

// NewLocalFromKeyFile builds a Local provider whose root seed is the
// persisted file-backed master key at path, creating the key on first
// use. Two boots from the same key file derive identical KEKs, so a
// WrappedDEK written before a restart unwraps after it — the persistence
// property NewLocalRandom lacks.
//
// spec: §17.4 line 163 — the embedded KMS is a file-backed soft-HSM.
func NewLocalFromKeyFile(path string) (*Local, error) {
	seed, err := LoadOrCreateMasterKey(path)
	if err != nil {
		return nil, err
	}
	return NewLocal(seed)
}
