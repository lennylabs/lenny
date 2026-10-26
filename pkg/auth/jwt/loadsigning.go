// SPDX-License-Identifier: MIT

package jwt

import (
	"context"
	"fmt"

	"github.com/lennylabs/lenny/pkg/kms"
)

// LoadSigning is the one construction of a process's §10.2 key set and
// shared signer. It loads the key set once with LoadKeySet and builds
// the remote ES256 signer from the key set's signing entry, so the
// signer's kid and the verifier's kid come from one PublicKey fetch. The
// remote signer is wrapped in a BreakerSigner reporting to obs, which
// may be nil. Any key-set load error is returned, and the caller
// refuses to start: the gateway and the Token Service both call it at
// startup and exit on error.
//
// spec: §10.2 (Authentication) "Signing topology", "Verifier key set and
// rotation"; §4.3.
func LoadSigning(ctx context.Context, provider kms.Provider, aliasToKey map[string]string, obs BreakerObserver) (*KeySet, *BreakerSigner, error) {
	keySet, err := LoadKeySet(ctx, provider, aliasToKey)
	if err != nil {
		return nil, nil, err
	}
	remote, err := NewRemoteSigner(provider, keySet.Signing())
	if err != nil {
		return nil, nil, fmt.Errorf("build remote signer: %w", err)
	}
	return keySet, &BreakerSigner{Inner: remote, Observer: obs}, nil
}
