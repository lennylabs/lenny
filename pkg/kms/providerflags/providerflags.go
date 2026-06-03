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

	// Environment is "prod" when the binary is running a production
	// install. When non-empty, Resolve rejects ProviderLocal (the
	// in-process random KEK cannot decrypt a persisted ciphertext
	// from a previous boot, so it is not safe in production).
	// spec: F-4.3.11 / F-17.5.2 — reject local in prod.
	Environment string

	// MasterKeyFile is the §17.4 file-backed soft-HSM master key path.
	// When set with ProviderLocal, Resolve seeds the Local provider from
	// this persisted key (creating it on first use) instead of a fresh
	// per-process random seed, so envelope-encrypted state survives a
	// restart — the §17.4 Embedded Mode "lenny down preserves state"
	// guarantee. Empty keeps the zero-config random KEK. spec: §17.4
	// line 163 / F-17.4.7.
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
// ErrLocalForbidden when Environment names a production install and
// Provider is "local".
func Resolve(ctx context.Context, opts Options) (kms.Provider, error) {
	prov := strings.ToLower(strings.TrimSpace(opts.Provider))
	if prov == "" {
		prov = ProviderLocal
	}
	if prov == ProviderLocal && strings.EqualFold(opts.Environment, "prod") {
		return nil, ErrLocalForbidden
	}
	switch prov {
	case ProviderLocal:
		// §17.4 line 163: a file-backed master key makes the local KEK
		// survive a restart (Embedded Mode). Without one the seed is
		// random per process, which is the zero-config dev/test posture.
		if opts.MasterKeyFile != "" {
			return kms.NewLocalFromKeyFile(opts.MasterKeyFile)
		}
		return kms.NewLocalRandom()
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
		"(the in-process random KEK does not survive a restart; pick aws|gcp|azure)")

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
// returns a finalizer the caller invokes after flag.Parse so the
// alias map is parsed from its comma-separated string into the
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
		"Deployment environment (dev | staging | prod). Production refuses "+
			"to start with --kms-provider=local. Override via LENNY_ENV.")
	fs.StringVar(&opts.MasterKeyFile, "kms-master-key-file",
		def("LENNY_KMS_MASTER_KEY_FILE", defaults.MasterKeyFile),
		"§17.4 line 163 file-backed soft-HSM master key path for "+
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
			"`platform:token-service-signing=alias/lenny/token-service,tenant:acme=alias/lenny/tenant-acme`.")
	return opts, func() error {
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
