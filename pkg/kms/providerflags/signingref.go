// SPDX-License-Identifier: MIT

package providerflags

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/lennylabs/lenny/pkg/kms"
)

// ErrRepointableSigningReference is returned by Resolve when a cloud
// provider's platform:jwt-signing or platform:jwt-verify alias maps to a
// reference that can be repointed at different key material. The key set
// computes each key's kid once from the startup fetch, so a reference
// that later names a different key would make every new token fail on
// every verifier.
var ErrRepointableSigningReference = errors.New(
	"kms/providerflags: a platform:jwt-* alias must map to an immutable key-version reference",
)

// gcpKeyVersionName matches a full Cloud KMS CryptoKeyVersion resource
// name, which pins one version of a CryptoKey.
var gcpKeyVersionName = regexp.MustCompile(
	`^projects/[^/]+/locations/[^/]+/keyRings/[^/]+/cryptoKeys/[^/]+/cryptoKeyVersions/[0-9]+$`,
)

// validateSigningReferences rejects a repointable reference on either
// §10.2 JWT alias. It runs for the cloud providers only; the local
// provider derives the key from the reference string itself.
//
// spec: §10.2 (Authentication) "Production".
func validateSigningReferences(provider string, aliasToKey map[string]string) error {
	aliases := make([]string, 0, len(aliasToKey))
	for alias := range aliasToKey {
		if kms.IsJWTAlias(alias) {
			aliases = append(aliases, alias)
		}
	}
	sort.Strings(aliases)
	for _, alias := range aliases {
		ref := aliasToKey[alias]
		if !immutableReference(provider, ref) {
			return fmt.Errorf("%w: --kms-provider=%s maps %s to %q", ErrRepointableSigningReference, provider, alias, ref)
		}
	}
	return nil
}

// immutableReference reports whether ref pins one key version on the
// named provider.
func immutableReference(provider, ref string) bool {
	switch provider {
	case ProviderAWS:
		return awsImmutable(ref)
	case ProviderGCP:
		return gcpKeyVersionName.MatchString(ref)
	case ProviderAzure:
		name, version, ok := strings.Cut(ref, "@")
		return ok && name != "" && version != ""
	}
	return false
}

// awsImmutable accepts an AWS KMS key ID or key ARN. AWS asymmetric keys
// have no version namespace and a key's material never changes, so the
// key itself is the immutable reference. An alias name ("alias/x") or an
// alias ARN ("arn:aws:kms:<region>:<account>:alias/x") can be repointed
// and is rejected.
func awsImmutable(ref string) bool {
	if strings.HasPrefix(ref, "arn:") {
		parts := strings.SplitN(ref, ":", 6)
		return len(parts) == 6 && parts[2] == "kms" &&
			strings.HasPrefix(parts[5], "key/") && len(parts[5]) > len("key/")
	}
	return ref != "" && !strings.ContainsAny(ref, "/:")
}
