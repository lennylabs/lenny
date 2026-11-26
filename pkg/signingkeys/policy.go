// SPDX-License-Identifier: MIT

package signingkeys

import (
	"errors"
	"fmt"
	"time"
)

// PurposeUploadToken is the purpose of the uploadToken HMAC keys.
const PurposeUploadToken = "upload-token"

// AlgorithmHMACSHA256 names the HMAC-SHA256 algorithm of a symmetric key.
const AlgorithmHMACSHA256 = "HMAC-SHA256"

// AliasUploadTokenSigning is the platform KMS alias that wraps the
// upload-token secrets.
//
// spec: §7.1 (Normal Flow), §12.3 (Postgres HA Requirements)
const AliasUploadTokenSigning = "platform:upload-token-signing"

// hmacSecretSize is the byte length of an HMAC-SHA256 secret: 256 bits.
const hmacSecretSize = 32

// Policy is the rule set of one purpose: its algorithm, the KMS alias that
// wraps its secrets, the secret length, how often its active key rotates,
// and how long a replaced key keeps verifying after the next key becomes
// active.
type Policy struct {
	Purpose          string
	Algorithm        string
	KEKAlias         string
	SecretSize       int
	RotationInterval time.Duration
	VerifyFor        time.Duration
}

// UploadTokenPolicy returns the upload-token purpose's policy with the
// given rotation interval, overlap window, and token lifetime
// (maxCreatedStateTimeoutSeconds). A replaced key verifies for
// UploadTokenVerificationTime of the overlap window and the token
// lifetime, so the verification time follows both configured values.
//
// spec: §7.1 (Normal Flow)
func UploadTokenPolicy(rotation, overlap, tokenLifetime time.Duration) Policy {
	return Policy{
		Purpose:          PurposeUploadToken,
		Algorithm:        AlgorithmHMACSHA256,
		KEKAlias:         AliasUploadTokenSigning,
		SecretSize:       hmacSecretSize,
		RotationInterval: rotation,
		VerifyFor:        UploadTokenVerificationTime(overlap, tokenLifetime),
	}
}

// UploadTokenVerificationTime returns how long a replaced upload-token key
// keeps verifying after the next key becomes active: the stale bound plus
// the longer of the overlap window and the token lifetime. The stale bound
// of an upload-token ring is the overlap window (the ring's
// Options.StaleAfter), so it is overlap + max(overlap, tokenLifetime),
// 10 minutes at the defaults of 5 minutes each.
//
// A replica whose last successful reload came just before a peer published
// the next key keeps signing with the replaced key until that reload is as
// old as the stale bound, which is never later than the next key's
// activation plus the stale bound, and the token it signs then is valid for
// the token lifetime. Every token any replica signs with the replaced key
// therefore expires before the key stops verifying, and a token lifetime
// longer than the overlap window needs no separate refusal.
//
// spec: §7.1 (Normal Flow)
func UploadTokenVerificationTime(overlap, tokenLifetime time.Duration) time.Duration {
	return overlap + max(overlap, tokenLifetime)
}

// validate reports a policy that cannot drive a Ring.
func (p Policy) validate() error {
	switch {
	case p.Purpose == "":
		return errors.New("signingkeys: policy has no purpose")
	case p.Algorithm != AlgorithmHMACSHA256:
		return fmt.Errorf("signingkeys: purpose %q: unsupported algorithm %q", p.Purpose, p.Algorithm)
	case p.KEKAlias == "":
		return fmt.Errorf("signingkeys: purpose %q has no KEK alias", p.Purpose)
	case p.SecretSize <= 0:
		return fmt.Errorf("signingkeys: purpose %q has no secret size", p.Purpose)
	case p.RotationInterval <= 0:
		return fmt.Errorf("signingkeys: purpose %q: rotation interval must be positive", p.Purpose)
	case p.VerifyFor <= 0:
		return fmt.Errorf("signingkeys: purpose %q: verification time must be positive", p.Purpose)
	}
	return nil
}
