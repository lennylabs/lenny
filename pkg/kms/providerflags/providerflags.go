// SPDX-License-Identifier: MIT

// Package providerflags wires a binary-level `--kms-provider` selector
// onto the §4 / §17.5 KMS adapter set. Both the gateway and the Token
// Service expose the same flag surface so an operator picks one of
// `local | aws | gcp | azure` per binary, and the cloud adapters
// (which ship as separate `pkg/kms/{aws,gcp,azure}` packages) reach
// the binaries through the resolved kms.Provider returned by Resolve.
//
// spec: §4 (KMS-backed envelope encryption); §17.5
// (cloud-KMS providers must be selectable from chart values; the
// adapter packages exist but the binaries cannot wire them without
// this seam).
package providerflags

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"strings"

	"github.com/lennylabs/lenny/pkg/deployenv"
	"github.com/lennylabs/lenny/pkg/kms"
	kmsaws "github.com/lennylabs/lenny/pkg/kms/aws"
	kmsazure "github.com/lennylabs/lenny/pkg/kms/azure"
	kmsgcp "github.com/lennylabs/lenny/pkg/kms/gcp"
)

// Providers names the supported §17.5 KMS provider selectors.
const (
	ProviderLocal = "local"
	ProviderAWS   = "aws"
	ProviderGCP   = "gcp"
	ProviderAzure = "azure"
)

// Options captures every operator-tunable KMS knob. Only the fields
// the resolved provider needs must be populated; the others stay
// empty.
type Options struct {
	// Provider names the §17.5 KEK seam: local | aws | gcp | azure.
	Provider string

	// Environment is the §17.9.1 Environment value carried by
	// --environment (LENNY_ENV as fallback). The finalizer Bind returns
	// refuses a non-empty value outside the closed set, and
	// deployenv.IsProduction is the only reader that derives production
	// mode from it. In production mode Resolve rejects ProviderLocal: the
	// in-process KEK cannot decrypt a persisted ciphertext from a
	// previous boot.
	// spec: §17.9.1 (Composition Dimensions) "Production mode"; §17.4.
	Environment string

	// MasterKeyFile is the §17.4 file-backed soft-HSM master key path.
	// When set with ProviderLocal, Resolve seeds the Local provider from
	// this persisted key (creating it on first use) instead of a fresh
	// per-process random seed, so envelope-encrypted state survives a
	// restart — the §17.4 Embedded Mode "lenny down preserves state"
	// guarantee. Empty keeps the zero-config random KEK. spec: §17.4 / F-17.4.7.
	MasterKeyFile string

	// AliasToKey is the binary-level alias-to-KMS-key map shared
	// across providers. The §4.9.1 lifecycle path adds tenant
	// aliases through Provider.SetAlias after construction; this map
	// seeds the platform-level aliases (the §4 Token Service signing
	// alias, the §4.9 connector-credentials KEK alias, etc.).
	//
	// The format is `alias=<cloud-key>` repeatable; see ParseAliases.
	AliasToKey map[string]string

	// Provider-specific. AWS resolves region from the standard
	// AWS_REGION env var via the SDK's default chain; the operator
	// passes nothing extra for the common case.
	AWSRegion string

	// AzureVaultURL is the URL of the Azure Key Vault
	// (https://<vault>.vault.azure.net) the provider points at.
	AzureVaultURL string
}

// Resolve constructs the kms.Provider the operator selected. Returns
// ErrLocalForbidden when Environment is production mode and Provider is
// "local" or empty.
//
// spec: §17.9.1 (Composition Dimensions) "Production mode"
func Resolve(ctx context.Context, opts Options) (kms.Provider, error) {
	prov := strings.ToLower(strings.TrimSpace(opts.Provider))
	if prov == "" {
		prov = ProviderLocal
	}
	if prov == ProviderLocal && deployenv.IsProduction(opts.Environment) {
		return nil, ErrLocalForbidden
	}
	if prov != ProviderLocal {
		if err := validateSigningReferences(prov, opts.AliasToKey); err != nil {
			return nil, err
		}
	}
	switch prov {
	case ProviderLocal:
		return resolveLocal(opts)
	case ProviderAWS:
		return resolveAWS(ctx, opts)
	case ProviderGCP:
		return resolveGCP(ctx, opts)
	case ProviderAzure:
		return resolveAzure(opts)
	default:
		return nil, fmt.Errorf("kms/providerflags: unknown --kms-provider %q (want local|aws|gcp|azure)", opts.Provider)
	}
}

// ErrLocalForbidden is returned by Resolve when --kms-provider=local
// is selected with --environment=prod. The in-process random KEK
// cannot survive a restart, so the §4 envelope-encryption claim does
// not hold in production. spec: F-4.3.11 / F-17.5.2.
var ErrLocalForbidden = errors.New(
	"kms/providerflags: --kms-provider=local is forbidden when --environment=prod " +
		"(the in-process random KEK does not survive a restart; pick aws|gcp|azure)",
)

// resolveLocal builds the local provider and passes it the §10.2 JWT
// alias entries of the alias map, from which it derives the ES256 key.
//
// spec: §10.2 (Authentication) "Development"; §17.4.
func resolveLocal(opts Options) (kms.Provider, error) {
	local, err := newLocal(opts.MasterKeyFile)
	if err != nil {
		return nil, err
	}
	for alias, ref := range opts.AliasToKey {
		if kms.IsJWTAlias(alias) {
			local.SetSigningReference(alias, ref)
		}
	}
	return local, nil
}

// newLocal seeds the local provider. §17.4: a file-backed master key
// makes the local KEK and signing key survive a restart and lets every
// process that mounts it share them. Without one the seed is random per
// process, which is the zero-config dev and test posture.
func newLocal(masterKeyFile string) (*kms.Local, error) {
	if masterKeyFile != "" {
		return kms.NewLocalFromKeyFile(masterKeyFile)
	}
	return kms.NewLocalRandom()
}

func resolveAWS(ctx context.Context, opts Options) (kms.Provider, error) {
	cfg, err := loadAWSConfig(ctx, opts.AWSRegion)
	if err != nil {
		return nil, fmt.Errorf("kms/providerflags: load AWS SDK config: %w", err)
	}
	if len(opts.AliasToKey) == 0 {
		return nil, errors.New("kms/providerflags: --kms-provider=aws requires --kms-alias entries")
	}
	return kmsaws.New(kmsaws.Config{AWSConfig: cfg, AliasToKeyID: opts.AliasToKey})
}

func resolveGCP(ctx context.Context, opts Options) (kms.Provider, error) {
	if len(opts.AliasToKey) == 0 {
		return nil, errors.New("kms/providerflags: --kms-provider=gcp requires --kms-alias entries")
	}
	return kmsgcp.New(ctx, kmsgcp.Config{AliasToKeyName: opts.AliasToKey})
}

func resolveAzure(opts Options) (kms.Provider, error) {
	if opts.AzureVaultURL == "" {
		return nil, errors.New("kms/providerflags: --kms-provider=azure requires --kms-azure-vault-url")
	}
	if len(opts.AliasToKey) == 0 {
		return nil, errors.New("kms/providerflags: --kms-provider=azure requires --kms-alias entries (name@version)")
	}
	cred, err := loadAzureCredential()
	if err != nil {
		return nil, fmt.Errorf("kms/providerflags: resolve Azure credential: %w", err)
	}
	aliasToKey := map[string]kmsazure.KeyRef{}
	for alias, ref := range opts.AliasToKey {
		name, version, _ := strings.Cut(ref, "@")
		aliasToKey[alias] = kmsazure.KeyRef{Name: name, Version: version}
	}
	return kmsazure.New(kmsazure.Config{
		VaultURL:   opts.AzureVaultURL,
		Credential: cred,
		AliasToKey: aliasToKey,
	})
}

// Bind registers the canonical `--kms-*` flag surface on fs and
// returns a finalizer the caller invokes after flag.Parse. The finalizer
// validates --environment against the §17.9.1 closed set and parses the
// alias map from its comma-separated string into the
// returned Options.AliasToKey. The chart's gateway-deployment.yaml /
// token-service-deployment.yaml templates render the same flag names
// from `gateway.kms.*` / `tokenService.kms.*` values, so a Helm value
// change reaches the binary without further code edits.
func Bind(fs *flag.FlagSet, env func(string) string, defaults Options) (*Options, func() error) {
	if env == nil {
		env = func(string) string { return "" }
	}
	opts := &Options{}
	def := func(envName, def string) string {
		if v := env(envName); v != "" {
			return v
		}
		return def
	}
	fs.StringVar(&opts.Provider, "kms-provider",
		def("LENNY_KMS_PROVIDER", defaults.Provider),
		"§4 / §17.5 KMS KEK provider selector: local | aws | gcp | azure. "+
			"`local` uses the in-process random KEK (dev only); the cloud "+
			"providers wrap DEKs through the named cloud KMS. "+
			"Rejected when --environment=prod. Override via LENNY_KMS_PROVIDER.")
	fs.StringVar(&opts.Environment, "environment",
		def("LENNY_ENV", defaults.Environment),
		"Deployment environment (local | dev | staging | prod); prod is "+
			"production mode (§17.9.1) and refuses --kms-provider=local. "+
			"Override via LENNY_ENV.")
	fs.StringVar(&opts.MasterKeyFile, "kms-master-key-file",
		def("LENNY_KMS_MASTER_KEY_FILE", defaults.MasterKeyFile),
		"§17.4 file-backed soft-HSM master key path for "+
			"--kms-provider=local. When set, the local KEK seed is loaded "+
			"(or generated 0600 on first use) from this file so "+
			"envelope-encrypted state survives a restart. Empty uses a "+
			"random per-process seed. Override via LENNY_KMS_MASTER_KEY_FILE.")
	fs.StringVar(&opts.AWSRegion, "kms-aws-region",
		def("LENNY_KMS_AWS_REGION", defaults.AWSRegion),
		"AWS region for --kms-provider=aws. Empty falls back to the AWS SDK default chain (AWS_REGION).")
	fs.StringVar(&opts.AzureVaultURL, "kms-azure-vault-url",
		def("LENNY_KMS_AZURE_VAULT_URL", defaults.AzureVaultURL),
		"Azure Key Vault URL for --kms-provider=azure (https://<vault>.vault.azure.net).")
	var aliasesRaw string
	fs.StringVar(&aliasesRaw, "kms-alias",
		def("LENNY_KMS_ALIAS", ""),
		"Comma-separated alias=key pairs the §4 KEK aliases map onto. "+
			"AWS: alias=<key-id|ARN|alias/name>. GCP: alias=<full CryptoKey resource>. "+
			"Azure: alias=<keyName>@<version>. Repeat with commas: "+
			"`platform:jwt-signing=<immutable P-256 key ref>,tenant:acme=alias/lenny/tenant-acme`. "+
			"On a cloud provider, platform:jwt-signing and platform:jwt-verify must name an immutable "+
			"key version (AWS key ID or key ARN, GCP .../cryptoKeyVersions/<n>, Azure <keyName>@<version>).")
	return opts, func() error {
		// spec: §17.9.1 (Composition Dimensions) "Production mode": an
		// off-set value refuses to start rather than run outside
		// production mode silently.
		if err := deployenv.Validate(opts.Environment); err != nil {
			return fmt.Errorf("--environment: %w", err)
		}
		m, err := ParseAliases(aliasesRaw)
		if err != nil {
			return err
		}
		opts.AliasToKey = m
		return nil
	}
}

// ParseAliases turns the comma-separated --kms-alias value the
// operator supplied into a map[alias]key.
func ParseAliases(raw string) (map[string]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return map[string]string{}, nil
	}
	out := map[string]string{}
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		alias, key, ok := strings.Cut(part, "=")
		if !ok || alias == "" || key == "" {
			return nil, fmt.Errorf("kms/providerflags: --kms-alias entry %q must be `alias=key`", part)
		}
		out[strings.TrimSpace(alias)] = strings.TrimSpace(key)
	}
	return out, nil
}
