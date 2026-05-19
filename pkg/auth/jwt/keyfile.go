// SPDX-License-Identifier: MIT

package jwt

import (
	"encoding/json"
	"fmt"
	"os"
)

// hmacKeyFile is the on-disk format of a persisted HMAC signing key:
// a key id and a raw secret. encoding/json renders the []byte secret
// as a base64 string. The §17.4 embedded OIDC provider persists its
// signing key in this same format, so the embedded stack can point the
// gateway at that key file and the gateway trusts it as an additional
// bearer verifier.
type hmacKeyFile struct {
	KeyID  string `json:"keyId"`
	Secret []byte `json:"secret"`
}

// LoadHMACKeyFile reads a persisted HMAC signing key from path and
// returns an HMACSigner over it. The returned signer is a Verifier
// (and a Signer) for tokens signed with that key.
//
// The §10.2 production gateway does not call LoadHMACKeyFile: its
// bearer verifier is the Token Service signer. The call exists for the
// §17.4 Embedded Mode gateway, which loads the embedded OIDC
// provider's key through its --bearer-trust-hmac-key-file flag and
// adds the resulting verifier to a MultiVerifier so `lenny token
// print` tokens are accepted alongside Token Service tokens.
//
// LoadHMACKeyFile returns an error when path does not exist, is not
// JSON, or carries an empty key id or secret. The os.ErrNotExist case
// is wrapped, so callers can distinguish a missing file with
// errors.Is.
func LoadHMACKeyFile(path string) (*HMACSigner, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("jwt: read hmac key file %s: %w", path, err)
	}
	var kf hmacKeyFile
	if err := json.Unmarshal(raw, &kf); err != nil {
		return nil, fmt.Errorf("jwt: parse hmac key file %s: %w", path, err)
	}
	if kf.KeyID == "" {
		return nil, fmt.Errorf("jwt: hmac key file %s carries no keyId", path)
	}
	if len(kf.Secret) == 0 {
		return nil, fmt.Errorf("jwt: hmac key file %s carries an empty secret", path)
	}
	return NewHMACSigner(kf.KeyID, kf.Secret), nil
}
