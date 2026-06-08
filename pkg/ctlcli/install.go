// SPDX-License-Identifier: MIT

package ctlcli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"sigs.k8s.io/yaml"

	"github.com/lennylabs/lenny/pkg/preflight"
	"github.com/lennylabs/lenny/pkg/preflight/infra"
)

// install.go implements `lenny-ctl install` — the §17.6 / §24.20
// installation wizard. The wizard collects deployment parameters
// (release coordinates, target environment, capacity tier, data-store
// connection details, auth mode, agent namespaces, feature flags),
// composes them into a Helm values file layered on a tier preset, and
// either runs `helm install` or emits the values file and the helm
// command for the operator to run.
//
// Two input modes are supported:
//
//   - Interactive: with no --answers, the wizard prompts for each
//     answer on stdin, showing the default in brackets.
//   - Non-interactive: with --answers <path>, the wizard reads a
//     YAML answer file instead of prompting. This path is pure and
//     repeatable, which is what CI and IaC pipelines use.
//
// The values-composition and helm-command logic is identical across
// both modes; only answer collection differs.

// installAnswers is the §17.6 answer-file schema. Every field maps to
// one wizard question. The YAML tags define the on-disk answer-file
// keys; --answers decodes this struct and --save-answers re-encodes
// it, so an interactive run can be captured once and replayed.
type installAnswers struct {
	// Release is the Helm release name and target namespace.
	Release installRelease `json:"release"`
	// Environment is the target environment: local, dev, staging, or
	// prod. composeValues writes it into the rendered values so the
	// lenny.gatewayLogLevel helper drives gateway log verbosity from it
	// (local/dev render LENNY_LOG_LEVEL=debug, staging/prod info), per the
	// §17.9.1 composition dimension. F-17.9.9.
	Environment string `json:"environment"`
	// Tier selects the capacity tier preset: tier1, tier2, or tier3.
	// The wizard layers presets/values-<tier>.yaml under the rendered
	// per-question overrides (§17.8.4).
	Tier string `json:"tier"`
	// Profile is the answer-file base name the wizard auto-suggests
	// from cluster detection (§17.9). It is advisory metadata; the
	// non-interactive path does not load a second file from it. Curated
	// answer files in charts/lenny/answers/ accordingly omit the field;
	// the wizard records it on save (§17.9.2 line 1376) so the captured
	// answer file documents the detection. F-17.9.15.
	Profile string `json:"profile,omitempty"`
	// Domain is the gateway's external DNS name. Empty leaves the
	// chart default in place.
	Domain string `json:"domain,omitempty"`
	// TLS is the gateway TLS strategy: cert-manager or bring-your-own.
	TLS string `json:"tls,omitempty"`
	// TLSIssuer is the cert-manager ClusterIssuer name the gateway
	// Ingress requests its serving certificate from. Honored only when
	// TLS is cert-manager; composeValues stamps it as the
	// cert-manager.io/cluster-issuer annotation. Empty leaves the
	// annotation off so the operator can add it out of band.
	TLSIssuer string `json:"tlsIssuer,omitempty"`
	// Postgres carries the Postgres connection details.
	Postgres installPostgres `json:"postgres"`
	// Redis carries the Redis connection details.
	Redis installRedis `json:"redis"`
	// ObjectStorage carries the object-store connection details.
	ObjectStorage installObjectStorage `json:"objectStorage"`
	// Auth carries the platform auth mode and OIDC issuer details.
	Auth installAuth `json:"auth"`
	// AgentNamespaces is the list of namespaces that hold agent pods
	// (§17.2). Empty leaves the chart default (lenny-agents,
	// lenny-agents-kata).
	AgentNamespaces []string `json:"agentNamespaces,omitempty"`
	// Features gates the optional admission webhooks (§17.2).
	Features installFeatures `json:"features"`
	// ReferenceRuntimes is the §17.6 reference-runtime multi-select. An
	// empty list installs all of §26 (the chart default); a non-empty list
	// names the subset to register via referenceRuntimes.include; the
	// single-element ["none"] sentinel disables the catalog entirely
	// (referenceRuntimes.enabled=false). F-17.6.10.
	ReferenceRuntimes []string `json:"referenceRuntimes,omitempty"`
	// DevMode sets global.devMode. It is valid only for the local
	// environment and must stay false on any multi-tenant cluster.
	DevMode bool `json:"devMode,omitempty"`
	// SpiffeTrustDomain sets the §10.3 (NET-064) global.spiffeTrustDomain,
	// a required chart value with no default. When empty the wizard
	// derives a deployment-unique default from the release identity so a
	// wizard-driven install renders; operators set a globally-unique value
	// (e.g. lenny-<cluster>-<namespace>) so two deployments cannot share a
	// trust domain. F-10.3.4.
	SpiffeTrustDomain string `json:"spiffeTrustDomain,omitempty"`
}

// installRelease is the Helm release name and target namespace.
type installRelease struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
}

// installPostgres carries the Postgres DSN.
type installPostgres struct {
	DSN string `json:"dsn,omitempty"`
}

// installRedis carries the Redis connection URL.
type installRedis struct {
	URL string `json:"url,omitempty"`
}

// installObjectStorage carries the §17.9.3 object-store selection.
// Provider switches the ArtifactStore backend ("minio" | "s3" | "gcs"
// | "azure"); the cloud providers consume Bucket (+ Region for s3, +
// AccountURL for azure) and ignore Endpoint, which is the MinIO API
// address. F-17.5.3.
type installObjectStorage struct {
	Provider   string `json:"provider,omitempty"`
	Endpoint   string `json:"endpoint,omitempty"`
	Bucket     string `json:"bucket,omitempty"`
	Region     string `json:"region,omitempty"`
	AccountURL string `json:"accountUrl,omitempty"`
}

// installAuth carries the platform auth mode and OIDC issuer details.
type installAuth struct {
	// Mode is "oidc" or "dev". "dev" is valid only for the local
	// environment; it sets global.devMode.
	Mode string `json:"mode"`
	// OIDCIssuer and OIDCClientID are required when Mode is "oidc" and
	// the environment is not local.
	OIDCIssuer   string `json:"oidcIssuer,omitempty"`
	OIDCClientID string `json:"oidcClientId,omitempty"`
}

// installFeatures gates the optional admission webhooks (§17.2).
type installFeatures struct {
	LLMProxy       bool `json:"llmProxy,omitempty"`
	DrainReadiness bool `json:"drainReadiness,omitempty"`
	Compliance     bool `json:"compliance,omitempty"`
}

// installConfig is the parsed `lenny-ctl install` flag set.
type installConfig struct {
	answerFile   string
	saveAnswers  string
	outputValues string
	chartDir     string
	releaseName  string
	namespace    string
	kubeContext  string
	nonInteract  bool
	dryRun       bool
	offline      bool
	skipSmoke    bool
}

// knownTiers is the set of capacity tiers the wizard accepts. It
// matches the preset files under charts/lenny/presets/.
var knownTiers = map[string]bool{"tier1": true, "tier2": true, "tier3": true}

// knownEnvironments is the set of target environments (§17.9.1).
var knownEnvironments = map[string]bool{
	"local": true, "dev": true, "staging": true, "prod": true,
}

// knownReferenceRuntimes is the §26 reference-runtime catalog the wizard's
// multi-select validates against. It mirrors charts/lenny/values.yaml
// referenceRuntimes.catalog; an unknown name is rejected so a typo does not
// silently install nothing for that entry. F-17.6.10.
var knownReferenceRuntimes = map[string]bool{
	"claude-code": true, "gemini-cli": true, "codex": true,
	"cursor-cli": true, "chat": true, "langgraph": true,
	"mastra": true, "openai-assistants": true, "crewai": true,
}

// referenceRuntimeNames returns the §26 catalog names, sorted, for the
// wizard prompt's "available" hint.
func referenceRuntimeNames() []string {
	names := make([]string, 0, len(knownReferenceRuntimes))
	for n := range knownReferenceRuntimes {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// tenantIDPattern is the canonical tenant_id format (§10.2). The
// wizard reuses it to validate namespace names defensively.
var tenantIDPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,253}$`)

// cmdInstall dispatches `lenny-ctl install`. It parses the flag set,
// collects answers (from a file or interactively), composes the Helm
// values, and either runs helm or prints the command.
func cmdInstall(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	for _, a := range args {
		if a == "-h" || a == "--help" {
			fmt.Fprintln(stdout, installUsage)
			return 0
		}
	}
	cfg, err := parseInstallFlags(args)
	if err != nil {
		fmt.Fprintf(stderr, "lenny-ctl: %v\n\n%s\n", err, installUsage)
		return 2
	}

	var answers installAnswers
	if cfg.answerFile != "" {
		raw, err := os.ReadFile(cfg.answerFile)
		if err != nil {
			fmt.Fprintf(stderr, "lenny-ctl: read %s: %v\n", cfg.answerFile, err)
			return 1
		}
		answers, err = parseAnswerFile(raw)
		if err != nil {
			fmt.Fprintf(stderr, "lenny-ctl: %s: %v\n", cfg.answerFile, err)
			return 1
		}
	} else {
		if cfg.nonInteract {
			fmt.Fprintln(stderr, "lenny-ctl: --non-interactive requires --answers <path>")
			return 2
		}
		// Detection phase (§17.6 lines 671-697): probe the target cluster
		// and present a summary before any question, so the operator sees
		// what was found and the question phase can apply detection-driven
		// defaults. --offline skips the probes entirely. F-17.6.9.
		det := clusterDetection{skipped: true}
		if !cfg.offline {
			det = newKubectlDetector(cfg.kubeContext).detect(context.Background())
		}
		printDetectionSummary(stdout, det)
		answers = promptAnswers(bufio.NewReader(stdin), stdout, det)
	}

	// CLI flags override the answer file's release coordinates so a
	// single answer file can be reused across releases.
	if cfg.releaseName != "" {
		answers.Release.Name = cfg.releaseName
	}
	if cfg.namespace != "" {
		answers.Release.Namespace = cfg.namespace
	}
	applyAnswerDefaults(&answers)

	if errs := validateAnswers(answers); len(errs) > 0 {
		fmt.Fprintln(stderr, "lenny-ctl: answer file is not valid:")
		for _, e := range errs {
			fmt.Fprintf(stderr, "  - %s\n", e)
		}
		return 1
	}

	if cfg.saveAnswers != "" {
		out, err := yaml.Marshal(answers)
		if err != nil {
			fmt.Fprintf(stderr, "lenny-ctl: encode answers: %v\n", err)
			return 1
		}
		if err := os.WriteFile(cfg.saveAnswers, out, 0o600); err != nil {
			fmt.Fprintf(stderr, "lenny-ctl: write %s: %v\n", cfg.saveAnswers, err)
			return 1
		}
		fmt.Fprintf(stderr, "lenny-ctl: answers written to %s\n", cfg.saveAnswers)
	}

	values, err := composeValues(answers)
	if err != nil {
		fmt.Fprintf(stderr, "lenny-ctl: compose values: %v\n", err)
		return 1
	}

	presetPath := filepath.Join(cfg.chartDir, "presets", "values-"+answers.Tier+".yaml")
	if cfg.outputValues != "" {
		if err := os.WriteFile(cfg.outputValues, values, 0o600); err != nil {
			fmt.Fprintf(stderr, "lenny-ctl: write %s: %v\n", cfg.outputValues, err)
			return 1
		}
		fmt.Fprintf(stderr, "lenny-ctl: composed values written to %s\n", cfg.outputValues)
	}

	// Preview phase (§17.6): show the operator the composed values and
	// the helm command before anything runs. When no --output-values
	// path was given, the preview names a placeholder for the composed
	// values file so the printed command stays accurate; the apply
	// path materializes the values on a temp file.
	previewValuesPath := cfg.outputValues
	if previewValuesPath == "" {
		previewValuesPath = "<composed-values.yaml>"
	}
	previewArgs := helmInstallArgs(answers, cfg.chartDir, presetPath, previewValuesPath)
	fmt.Fprintln(stdout, "# Composed Helm values:")
	_, _ = stdout.Write(values)
	fmt.Fprintln(stdout)
	fmt.Fprintf(stdout, "# Tier preset layered under these values: %s\n", presetPath)
	fmt.Fprintf(stdout, "# Helm command:\n%s\n", "helm "+strings.Join(previewArgs, " "))

	if cfg.dryRun {
		fmt.Fprintln(stderr, "lenny-ctl: --dry-run set; not invoking helm")
		return 0
	}

	// Preflight phase (§17.6 line 696 / §24.20 line 295: detection →
	// question → preview → preflight → helm install). The wizard probes
	// the resolved Postgres/Redis/MinIO backends before mutating the
	// cluster; any hard failure aborts so a misconfigured DSN surfaces
	// here rather than as a CrashLoopBackOff after `helm install`.
	// F-24.2.5 / F-24.20.3.
	if code := runInstallPreflight(context.Background(), installPreflightConfig(answers), infra.RealProbers(), stdout, stderr); code != 0 {
		return code
	}

	// Apply phase (§17.6): run helm install as a subprocess. The
	// composed values are passed on a temp file unless --output-values
	// already wrote them somewhere durable.
	valuesPath := cfg.outputValues
	if valuesPath == "" {
		tmp, err := os.CreateTemp("", "lenny-install-values-*.yaml")
		if err != nil {
			fmt.Fprintf(stderr, "lenny-ctl: temp values file: %v\n", err)
			return 1
		}
		defer os.Remove(tmp.Name())
		if _, err := tmp.Write(values); err != nil {
			_ = tmp.Close()
			fmt.Fprintf(stderr, "lenny-ctl: write temp values: %v\n", err)
			return 1
		}
		_ = tmp.Close()
		valuesPath = tmp.Name()
	}
	helmArgs := helmInstallArgs(answers, cfg.chartDir, presetPath, valuesPath)

	if _, err := exec.LookPath("helm"); err != nil {
		fmt.Fprintln(stderr, "lenny-ctl: helm not found on PATH; install Helm or re-run with --dry-run")
		return 1
	}
	cmd := exec.Command("helm", helmArgs...)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(stderr, "lenny-ctl: helm install failed: %v\n", err)
		return 1
	}
	fmt.Fprintf(stderr, "lenny-ctl: release %q installed in namespace %q\n",
		answers.Release.Name, answers.Release.Namespace)

	// Smoke-test phase (§24.20 line 295/299: ... → helm install → bootstrap
	// seed → smoke test). The wizard probes /healthz and exercises the chat
	// reference runtime so a broken install surfaces here rather than on the
	// operator's first session. --skip-smoke-test opts out. F-24.20.4.
	if cfg.skipSmoke {
		fmt.Fprintln(stdout, "# Smoke test: skipped (--skip-smoke-test)")
		return 0
	}
	rb := rollbackInfo{release: answers.Release.Name, namespace: answers.Release.Namespace}
	return runSmokeTest(context.Background(), &httpSmokeTester{}, smokeTargetFromAnswers(answers), rb, stdout, stderr)
}

// parseInstallFlags parses the `install` flag set.
func parseInstallFlags(args []string) (installConfig, error) {
	cfg := installConfig{chartDir: "charts/lenny"}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		// --answers is the sole §24.20 spelling of the answer-file flag
		// (§24.20 lines 300, 304). F-24.20.7.
		case "--answers":
			if i+1 >= len(args) {
				return cfg, fmt.Errorf("%s requires a path", args[i])
			}
			cfg.answerFile, i = args[i+1], i+1
		case "--save-answers":
			if i+1 >= len(args) {
				return cfg, fmt.Errorf("--save-answers requires a path")
			}
			cfg.saveAnswers, i = args[i+1], i+1
		case "--output-values":
			if i+1 >= len(args) {
				return cfg, fmt.Errorf("--output-values requires a path")
			}
			cfg.outputValues, i = args[i+1], i+1
		case "--chart":
			if i+1 >= len(args) {
				return cfg, fmt.Errorf("--chart requires a path")
			}
			cfg.chartDir, i = args[i+1], i+1
		case "--release":
			if i+1 >= len(args) {
				return cfg, fmt.Errorf("--release requires a name")
			}
			cfg.releaseName, i = args[i+1], i+1
		case "--namespace":
			if i+1 >= len(args) {
				return cfg, fmt.Errorf("--namespace requires a name")
			}
			cfg.namespace, i = args[i+1], i+1
		// --context overrides the kubeconfig context the detection phase
		// probes (§17.6 line 673). It has no effect under --offline or
		// --answers (neither runs detection).
		case "--context":
			if i+1 >= len(args) {
				return cfg, fmt.Errorf("--context requires a name")
			}
			cfg.kubeContext, i = args[i+1], i+1
		case "--non-interactive":
			cfg.nonInteract = true
		case "--dry-run":
			cfg.dryRun = true
		// --offline skips cluster-reachability detection probes
		// (§24.20). The non-interactive path performs no detection, so
		// the flag is accepted and recorded for parity with the
		// interactive path.
		case "--offline":
			cfg.offline = true
		// --skip-smoke-test opts out of the post-install smoke-test phase
		// (§24.20 line 299). F-24.20.4.
		case "--skip-smoke-test":
			cfg.skipSmoke = true
		default:
			return cfg, fmt.Errorf("unknown install flag %q", args[i])
		}
	}
	return cfg, nil
}

// parseAnswerFile decodes a YAML answer file into installAnswers,
// resolving ${VAR} references against the process environment. The
// spec (§17.6) supports environment-variable interpolation for secret
// material so DSNs are never committed in plaintext.
func parseAnswerFile(raw []byte) (installAnswers, error) {
	expanded := expandEnvRefs(string(raw))
	var a installAnswers
	// Strict decode rejects unknown keys so a typo in an answer-file
	// key fails fast rather than being silently ignored.
	if err := yaml.UnmarshalStrict([]byte(expanded), &a); err != nil {
		return installAnswers{}, fmt.Errorf("not a valid answer file: %w", err)
	}
	return a, nil
}

// envRefPattern matches ${VAR} interpolation references.
var envRefPattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// expandEnvRefs replaces every ${VAR} occurrence with the value of the
// environment variable VAR. An unset variable expands to the empty
// string, matching shell expansion semantics.
func expandEnvRefs(s string) string {
	return envRefPattern.ReplaceAllStringFunc(s, func(ref string) string {
		name := envRefPattern.FindStringSubmatch(ref)[1]
		return os.Getenv(name)
	})
}

// applyAnswerDefaults fills unset answer fields with the wizard
// defaults (§17.6 question table) so a sparse answer file still
// produces a complete values file.
func applyAnswerDefaults(a *installAnswers) {
	if a.Release.Name == "" {
		a.Release.Name = "lenny"
	}
	if a.Release.Namespace == "" {
		a.Release.Namespace = "lenny-system"
	}
	if a.Environment == "" {
		a.Environment = "local"
	}
	if a.Tier == "" {
		// Default tier1 for local/dev, tier2 for prod (§17.6).
		if a.Environment == "prod" || a.Environment == "staging" {
			a.Tier = "tier2"
		} else {
			a.Tier = "tier1"
		}
	}
	if a.Auth.Mode == "" {
		if a.Environment == "local" {
			a.Auth.Mode = "dev"
		} else {
			a.Auth.Mode = "oidc"
		}
	}
	// dev auth mode implies global.devMode; it is only ever valid for
	// the local environment, which validateAnswers enforces.
	if a.Auth.Mode == "dev" {
		a.DevMode = true
	}
}

// validateAnswers checks the resolved answers for internal
// consistency. It returns one message per problem; an empty slice
// means the answers are usable.
func validateAnswers(a installAnswers) []string {
	var errs []string
	if !knownEnvironments[a.Environment] {
		errs = append(errs, fmt.Sprintf(
			"environment %q is not one of local, dev, staging, or prod", a.Environment,
		))
	}
	if !knownTiers[a.Tier] {
		errs = append(errs, fmt.Sprintf(
			"tier %q is not one of tier1, tier2, or tier3", a.Tier,
		))
	}
	switch a.Auth.Mode {
	case "oidc":
		// OIDC is required outside the local environment (§17.6
		// question table).
		if a.Environment != "local" {
			if a.Auth.OIDCIssuer == "" {
				errs = append(errs, "auth.mode is oidc but auth.oidcIssuer is empty")
			}
			if a.Auth.OIDCClientID == "" {
				errs = append(errs, "auth.mode is oidc but auth.oidcClientId is empty")
			}
		}
	case "dev":
		if a.Environment != "local" {
			errs = append(errs, fmt.Sprintf(
				"auth.mode dev is only valid for the local environment, not %q", a.Environment,
			))
		}
	default:
		errs = append(errs, fmt.Sprintf(
			"auth.mode %q is not one of oidc or dev", a.Auth.Mode,
		))
	}
	if a.DevMode && a.Environment != "local" {
		errs = append(errs, fmt.Sprintf(
			"devMode is true but the environment is %q; devMode is only valid for local", a.Environment,
		))
	}
	if a.TLS != "" && a.TLS != "cert-manager" && a.TLS != "bring-your-own" {
		errs = append(errs, fmt.Sprintf(
			"tls %q is not one of cert-manager or bring-your-own", a.TLS,
		))
	}
	for _, ns := range a.AgentNamespaces {
		if !tenantIDPattern.MatchString(ns) {
			errs = append(errs, fmt.Sprintf(
				"agent namespace %q is not a valid Kubernetes namespace name", ns,
			))
		}
	}
	// spec: §17.6 lines 688-689 — every selected reference runtime must
	// name a §26 catalog entry. "none" is a valid selection only as the
	// sole entry (it disables the catalog). F-17.6.10.
	for _, rt := range a.ReferenceRuntimes {
		if strings.EqualFold(rt, "none") {
			if len(a.ReferenceRuntimes) != 1 {
				errs = append(errs,
					"referenceRuntimes 'none' cannot be combined with other runtime names")
			}
			continue
		}
		if !knownReferenceRuntimes[rt] {
			errs = append(errs, fmt.Sprintf(
				"referenceRuntimes %q is not a §26 catalog runtime (one of %s)",
				rt, strings.Join(referenceRuntimeNames(), ", ")))
		}
	}
	// spec: §17.9.3 — validate the object-storage selection. A cloud
	// provider (s3 | gcs | azure) requires a bucket; the MinIO/in-memory
	// default (provider empty or "minio") requires an endpoint before a
	// bucket takes effect, since the chart gates LENNY_MINIO_* on
	// `minio.endpoint`. F-17.5.1 / F-17.5.3.
	provider := strings.ToLower(strings.TrimSpace(a.ObjectStorage.Provider))
	isCloud := provider == "s3" || provider == "gcs" || provider == "azure"
	switch {
	case provider != "" && provider != "minio" && !isCloud:
		errs = append(errs, fmt.Sprintf(
			"objectStorage.provider %q is not one of minio|s3|gcs|azure", a.ObjectStorage.Provider))
	case isCloud && a.ObjectStorage.Bucket == "":
		errs = append(errs, fmt.Sprintf(
			"objectStorage.provider=%s requires objectStorage.bucket", provider))
	case provider == "azure" && a.ObjectStorage.AccountURL == "":
		errs = append(errs,
			"objectStorage.provider=azure requires objectStorage.accountUrl (https://<account>.blob.core.windows.net)")
	case !isCloud && a.ObjectStorage.Bucket != "" && a.ObjectStorage.Endpoint == "":
		errs = append(errs,
			"objectStorage.bucket is set but objectStorage.endpoint is empty; "+
				"the chart wires MinIO only when endpoint is supplied (set objectStorage.provider for a cloud backend)")
	}
	return errs
}

// composeValues renders the answer set into a Helm values document.
// deriveSpiffeTrustDomain produces a deployment-unique §10.3 (NET-064)
// trust domain from the release identity when the operator did not set
// one. The result is sanitized to lowercase alphanumerics and hyphens
// so it is a valid SPIFFE trust-domain host component. spec: §10.3
// line 316. F-10.3.4.
func deriveSpiffeTrustDomain(namespace, name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower("lenny-" + namespace + "-" + name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	if d := strings.Trim(b.String(), "-"); d != "" {
		return d
	}
	return "lenny"
}

// ingressTLSSecretName is the per-host serving-certificate Secret the
// composed gateway.ingress points at. cert-manager issues into it; a
// bring-your-own deployer creates it. spec: §17.9.2 / §17.6. F-17.9.6.
const ingressTLSSecretName = "lenny-gateway-ingress-tls"

// composeIngress maps the wizard's domain and TLS answers onto the
// gateway.ingress chart values. It returns nil when neither a domain nor
// a TLS strategy was answered, so a stock render keeps the opt-in
// Ingress off (gateway.ingress.enabled defaults false). spec: §17.9.2
// line 1376 / §17.6. F-17.9.6.
func composeIngress(a installAnswers) map[string]any {
	if a.Domain == "" && a.TLS == "" {
		return nil
	}
	ingress := map[string]any{"enabled": true}
	if a.Domain != "" {
		ingress["host"] = a.Domain
	}
	switch a.TLS {
	case "cert-manager":
		ingress["tls"] = map[string]any{"enabled": true, "secretName": ingressTLSSecretName}
		if a.TLSIssuer != "" {
			ingress["annotations"] = map[string]any{"cert-manager.io/cluster-issuer": a.TLSIssuer}
		}
	case "bring-your-own":
		ingress["tls"] = map[string]any{"enabled": true, "secretName": ingressTLSSecretName}
	}
	return ingress
}

// The result is the per-question override layer; the tier preset is
// layered under it by helm itself via a second -f argument.
func composeValues(a installAnswers) ([]byte, error) {
	values := map[string]any{}

	// global.devMode is set only when dev auth mode was selected.
	global := map[string]any{}
	if a.DevMode {
		global["devMode"] = true
	}
	// §10.3 (NET-064) F-10.3.4: global.spiffeTrustDomain is a required
	// chart value with no default. Use the operator's value or derive a
	// deployment-unique default from the release identity so the install
	// renders; the lenny-preflight Job still rejects a cross-deployment
	// collision.
	trustDomain := a.SpiffeTrustDomain
	if trustDomain == "" {
		trustDomain = deriveSpiffeTrustDomain(a.Release.Namespace, a.Release.Name)
	}
	global["spiffeTrustDomain"] = trustDomain
	if len(global) > 0 {
		values["global"] = global
	}

	// §10.3 lines 365-366 F-10.3.14: auth.oidc.issuerUrl / clientId are
	// required platform keys outside dev mode. Render the collected OIDC
	// registration so a wizard-driven production install passes the
	// gateway startup configuration gate rather than CrashLoopBackOff.
	oidc := map[string]any{}
	if a.Auth.OIDCIssuer != "" {
		oidc["issuerUrl"] = a.Auth.OIDCIssuer
	}
	if a.Auth.OIDCClientID != "" {
		oidc["clientId"] = a.Auth.OIDCClientID
	}
	if len(oidc) > 0 {
		values["auth"] = map[string]any{"oidc": oidc}
	}

	// spec: §17.9.1 line 1350 — the environment dimension drives gateway
	// log verbosity and the environment-keyed chart defaults via the
	// lenny.gatewayLogLevel helper. Write it into the values document so
	// the rendered chart reacts; previously it was echoed only into the
	// header comment. F-17.9.9.
	if a.Environment != "" {
		values["environment"] = a.Environment
	}

	// spec: §17.9.2 line 1376 / §17.6 — the wizard's domain and TLS
	// answers reach the gateway Ingress (`gateway.ingress.*`). Domain
	// turns the opt-in Ingress on and sets its host; the TLS strategy
	// selects the serving-certificate posture: cert-manager stamps the
	// cluster-issuer annotation (when an issuer is named) so cert-manager
	// issues into the per-host Secret, while bring-your-own names the
	// Secret the operator supplies. Both enable TLS termination on the
	// Ingress. Previously these answers were collected and discarded.
	// F-17.9.6.
	if ingress := composeIngress(a); ingress != nil {
		values["gateway"] = map[string]any{"ingress": ingress}
	}

	if a.Postgres.DSN != "" {
		values["postgres"] = map[string]any{"dsn": a.Postgres.DSN}
	}
	if a.Redis.URL != "" {
		values["redis"] = map[string]any{"url": a.Redis.URL}
	}
	// spec: §17.9.3 line 1413 — emit the `minio:` block only when
	// `endpoint` is supplied. The chart's MinIO wiring
	// (`charts/lenny/templates/{datastore-secret,gateway-deployment}.yaml`)
	// gates every LENNY_MINIO_* env on a non-empty endpoint, so a
	// bucket-only entry would otherwise be silently dropped.
	// `validateAnswers` rejects bucket-without-endpoint upstream.
	if a.ObjectStorage.Endpoint != "" {
		minio := map[string]any{"endpoint": a.ObjectStorage.Endpoint}
		if a.ObjectStorage.Bucket != "" {
			minio["bucket"] = a.ObjectStorage.Bucket
		}
		values["minio"] = minio
	}
	// spec: §17.9.3 — emit the canonical `objectStorage:` block for a
	// cloud-managed backend so the chart renders the gateway's
	// --object-storage-* selector flags. validateAnswers has already
	// asserted the required (bucket / accountUrl) fields. F-17.5.1 /
	// F-17.5.3.
	if p := strings.ToLower(strings.TrimSpace(a.ObjectStorage.Provider)); p == "s3" || p == "gcs" || p == "azure" {
		os := map[string]any{"provider": p}
		if a.ObjectStorage.Bucket != "" {
			os["bucket"] = a.ObjectStorage.Bucket
		}
		if a.ObjectStorage.Region != "" {
			os["region"] = a.ObjectStorage.Region
		}
		if a.ObjectStorage.AccountURL != "" {
			os["accountUrl"] = a.ObjectStorage.AccountURL
		}
		values["objectStorage"] = os
	}

	// features gates the optional admission webhooks. Only emit the
	// block when at least one flag is on so the chart defaults stand
	// otherwise.
	if a.Features.LLMProxy || a.Features.DrainReadiness || a.Features.Compliance {
		features := map[string]any{}
		if a.Features.LLMProxy {
			features["llmProxy"] = true
		}
		if a.Features.DrainReadiness {
			features["drainReadiness"] = true
		}
		if a.Features.Compliance {
			features["compliance"] = true
		}
		values["features"] = features
	}

	if len(a.AgentNamespaces) > 0 {
		nss := make([]any, 0, len(a.AgentNamespaces))
		for _, ns := range a.AgentNamespaces {
			nss = append(nss, map[string]any{"name": ns})
		}
		values["agentNamespaces"] = nss
	}

	// spec: §17.6 lines 688-689 — the reference-runtime selection. An empty
	// list leaves the chart default (the whole §26 catalog); ["none"]
	// disables the catalog; any other list narrows it via
	// referenceRuntimes.include. F-17.6.10.
	if len(a.ReferenceRuntimes) == 1 && strings.EqualFold(a.ReferenceRuntimes[0], "none") {
		values["referenceRuntimes"] = map[string]any{"enabled": false}
	} else if len(a.ReferenceRuntimes) > 0 {
		include := make([]any, 0, len(a.ReferenceRuntimes))
		for _, n := range a.ReferenceRuntimes {
			include = append(include, n)
		}
		values["referenceRuntimes"] = map[string]any{"include": include}
	}

	header := fmt.Sprintf(
		"# Helm values composed by `lenny-ctl install`.\n"+
			"# release: %s/%s  environment: %s  tier: %s  auth: %s\n"+
			"# Layer the tier preset under this file:\n"+
			"#   helm install %s charts/lenny -f presets/values-%s.yaml -f <this-file>\n",
		a.Release.Namespace, a.Release.Name, a.Environment, a.Tier, a.Auth.Mode,
		a.Release.Name, a.Tier,
	)

	if len(values) == 0 {
		// Every override fell back to a chart default. Emit only the
		// header so the file is still a valid, if empty, values doc.
		return []byte(header), nil
	}
	body, err := yaml.Marshal(values)
	if err != nil {
		return nil, err
	}
	return append([]byte(header), body...), nil
}

// helmInstallArgs builds the `helm install` argument vector. The tier
// preset is passed first and the composed per-question values second,
// so the per-question values win on any overlapping key (§17.6
// composition order: preset, then overrides).
func helmInstallArgs(a installAnswers, chartDir, presetPath, valuesPath string) []string {
	args := []string{
		"install", a.Release.Name, chartDir,
		"--namespace", a.Release.Namespace,
		"--create-namespace",
		"-f", presetPath,
	}
	if valuesPath != "" {
		args = append(args, "-f", valuesPath)
	}
	return args
}

// promptAnswers runs the interactive question phase (§17.6). It reads
// one answer per line from r, showing each default in brackets. An
// empty line accepts the default.
func promptAnswers(r *bufio.Reader, w io.Writer, det clusterDetection) installAnswers {
	var a installAnswers
	fmt.Fprintln(w, "lenny-ctl install — interactive wizard")
	fmt.Fprintln(w, "Press Enter to accept the bracketed default for any question.")
	fmt.Fprintln(w)

	// spec: §17.9.2 line 1376 — record the §17.9.2 catalog answer-file base
	// the detection phase suggests from the cluster type, so a captured
	// answer file (--save-answers) documents the suggestion. It is advisory
	// metadata; the non-interactive path does not load a second file from
	// it. F-17.9.8.
	a.Profile = det.suggestedAnswerFile

	a.Release.Name = ask(r, w, "Release name", "lenny")
	a.Release.Namespace = ask(r, w, "Release namespace", "lenny-system")
	a.Environment = ask(r, w, "Target environment (local|dev|staging|prod)", "local")
	defTier := "tier1"
	if a.Environment == "prod" || a.Environment == "staging" {
		defTier = "tier2"
	}
	a.Tier = ask(r, w, "Capacity tier (tier1|tier2|tier3)", defTier)
	a.Domain = ask(r, w, "Gateway domain (blank to keep the chart default)", "")
	// spec: §17.6 line 689 — the TLS-strategy default is detection-driven
	// (cert-manager when a Ready ClusterIssuer exists, bring-your-own
	// otherwise) and the question is skipped when exactly one ClusterIssuer
	// is Ready, since the answer is then unambiguous. F-17.6.9.
	tlsDef, issuerDef, skipTLS := tlsDefaults(det)
	if skipTLS {
		a.TLS = tlsDef
		a.TLSIssuer = issuerDef
		fmt.Fprintf(w, "TLS strategy: cert-manager (detected the Ready ClusterIssuer %q — skipping prompt)\n", issuerDef)
	} else {
		a.TLS = ask(r, w, "TLS strategy (cert-manager|bring-your-own, blank to skip)", tlsDef)
		if a.TLS == "cert-manager" {
			a.TLSIssuer = ask(r, w, "cert-manager ClusterIssuer name (blank to add the annotation later)", issuerDef)
		}
	}
	a.Postgres.DSN = ask(r, w, "Postgres DSN (blank for the chart default)", "")
	a.Redis.URL = ask(r, w, "Redis URL (blank for the chart default)", "")
	a.ObjectStorage.Provider = ask(r, w, "Object storage provider (minio|s3|gcs|azure)", "minio")
	a.ObjectStorage.Endpoint = ask(r, w, "MinIO endpoint (blank unless provider=minio with an external MinIO)", "")
	a.ObjectStorage.Bucket = ask(r, w, "Object storage bucket / Azure container (blank for the chart default)", "")
	a.ObjectStorage.Region = ask(r, w, "Object storage region (s3 only; blank for the AWS default chain)", "")
	a.ObjectStorage.AccountURL = ask(r, w, "Azure Blob account URL (azure only; blank otherwise)", "")
	defAuth := "oidc"
	if a.Environment == "local" {
		defAuth = "dev"
	}
	a.Auth.Mode = ask(r, w, "Auth mode (oidc|dev)", defAuth)
	if a.Auth.Mode == "oidc" {
		a.Auth.OIDCIssuer = ask(r, w, "OIDC issuer URL", "")
		a.Auth.OIDCClientID = ask(r, w, "OIDC client ID", "")
	}
	nss := ask(r, w, "Agent namespaces (comma-separated, blank for the chart default)", "")
	if nss != "" {
		for _, ns := range strings.Split(nss, ",") {
			if t := strings.TrimSpace(ns); t != "" {
				a.AgentNamespaces = append(a.AgentNamespaces, t)
			}
		}
	}
	a.Features.LLMProxy = askBool(r, w, "Enable the LLM-proxy admission webhook", false)
	a.Features.DrainReadiness = askBool(r, w, "Enable the drain-readiness admission webhook", false)
	a.Features.Compliance = askBool(r, w, "Enable the compliance admission webhooks", false)
	// spec: §17.6 lines 688-689 — the reference-runtime multi-select. All
	// of §26 is installed by default; the operator names a subset to
	// minimize the image-pull footprint, or "none" to register no
	// reference runtime. F-17.6.10.
	rts := ask(r, w, "Reference runtimes to install (comma-separated names, blank for all of §26, 'none' for none)\n  available: "+strings.Join(referenceRuntimeNames(), ", "), "")
	a.ReferenceRuntimes = parseReferenceRuntimeAnswer(rts)
	return a
}

// parseReferenceRuntimeAnswer splits the comma-separated reference-runtime
// answer into a name list. A blank answer leaves the list nil (install all
// of §26); the literal "none" yields a single-element ["none"] sentinel that
// composeValues maps to referenceRuntimes.enabled=false.
func parseReferenceRuntimeAnswer(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	if strings.EqualFold(s, "none") {
		return []string{"none"}
	}
	var out []string
	for _, n := range strings.Split(s, ",") {
		if t := strings.TrimSpace(n); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// ask prompts for a single answer, returning def when the operator
// enters an empty line.
func ask(r *bufio.Reader, w io.Writer, prompt, def string) string {
	if def != "" {
		fmt.Fprintf(w, "%s [%s]: ", prompt, def)
	} else {
		fmt.Fprintf(w, "%s: ", prompt)
	}
	line, _ := r.ReadString('\n')
	line = strings.TrimSpace(line)
	if line == "" {
		return def
	}
	return line
}

// askBool prompts for a yes/no answer, returning def on an empty line.
func askBool(r *bufio.Reader, w io.Writer, prompt string, def bool) bool {
	d := "y/N"
	if def {
		d = "Y/n"
	}
	fmt.Fprintf(w, "%s [%s]: ", prompt, d)
	line, _ := r.ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "":
		return def
	case "y", "yes", "true":
		return true
	default:
		return false
	}
}

// answerFileKeys returns the documented answer-file key paths, sorted.
// It backs the `install schema` helper and the answer-file
// documentation test.
func answerFileKeys() []string {
	keys := []string{
		"release.name",
		"release.namespace",
		"environment",
		"tier",
		"profile",
		"domain",
		"tls",
		"postgres.dsn",
		"redis.url",
		"objectStorage.endpoint",
		"objectStorage.bucket",
		"auth.mode",
		"auth.oidcIssuer",
		"auth.oidcClientId",
		"agentNamespaces",
		"features.llmProxy",
		"features.drainReadiness",
		"features.compliance",
		"referenceRuntimes",
		"devMode",
	}
	sort.Strings(keys)
	return keys
}

// installPreflightConfig maps the wizard answers onto the §24.2 infra
// preflight config. Postgres and Redis DSNs are self-contained, so they
// probe directly. MinIO credentials are not part of the answer file
// (they live in a Kubernetes Secret); the wizard pulls them from the
// environment when present so the probe can authenticate, and otherwise
// leaves MinIO unconfigured so it is skipped rather than failing on an
// anonymous request. spec: §17.6 line 696; §24.2 line 47.
func installPreflightConfig(a installAnswers) infra.Config {
	cfg := infra.Config{
		PostgresDSN: a.Postgres.DSN,
		RedisDSN:    a.Redis.URL,
		MinIOUseSSL: true,
	}
	ak := os.Getenv("LENNY_MINIO_ACCESS_KEY")
	sk := os.Getenv("LENNY_MINIO_SECRET_KEY")
	if a.ObjectStorage.Endpoint != "" && ak != "" && sk != "" {
		cfg.MinIOEndpoint = a.ObjectStorage.Endpoint
		cfg.MinIOAccessKey = ak
		cfg.MinIOSecretKey = sk
		cfg.MinIOBucket = a.ObjectStorage.Bucket
	}
	return cfg
}

// runInstallPreflight runs the wizard's preflight phase, rendering each
// check and aborting (exit 1) on the first hard failure. It is split
// from cmdInstall so a test drives it with fake probers. An empty config
// (every backend a chart default with no external DSN) probes nothing
// and passes — there is nothing reachable to validate before install.
// spec: §17.6 line 696; §24.20 line 295. F-24.2.5 / F-24.20.3.
func runInstallPreflight(ctx context.Context, cfg infra.Config, probers infra.Probers, stdout, stderr io.Writer) int {
	fmt.Fprintln(stdout, "# Preflight: validating Postgres/Redis/MinIO connectivity before install")
	report := infra.Run(ctx, cfg, probers)
	for _, r := range report {
		printPreflightCheck(stdout, r.Name, r.Decision.Passed, r.Decision.Reason)
	}
	if preflight.Failed(report) {
		fmt.Fprintln(stderr, "lenny-ctl install: preflight failed; aborting before helm install (see §17.6)")
		return 1
	}
	return 0
}

const installUsage = `lenny-ctl install — Lenny installation wizard (§17.6, §24.20)

Usage:
  lenny-ctl install [flags]

Without --answers the wizard prompts interactively on stdin. With
--answers it reads a YAML answer file and runs non-interactively,
which is the repeatable path for CI and IaC.

Flags:
  --answers <path>       Read answers from a YAML file
  --non-interactive      Require --answers; never prompt
  --save-answers <path>  Write the resolved answers back to a YAML file
  --output-values <path> Write the composed Helm values to a file
  --chart <path>         Chart directory (default charts/lenny)
  --release <name>       Override the release name from the answer file
  --namespace <ns>       Override the release namespace from the answer file
  --context <name>       kubeconfig context for the detection phase
  --offline              Skip the cluster detection phase
  --skip-smoke-test      Skip the post-install smoke test against the chat runtime
  --dry-run              Print the composed values and helm command; do not run helm

Answer-file keys:
  release.name, release.namespace, environment, tier, profile, domain,
  tls, postgres.dsn, redis.url, objectStorage.endpoint,
  objectStorage.bucket, auth.mode, auth.oidcIssuer, auth.oidcClientId,
  agentNamespaces, features.llmProxy, features.drainReadiness,
  features.compliance, referenceRuntimes, devMode

Environment-variable references of the form ${VAR} in the answer file
are resolved against the process environment at read time, so secret
DSNs are not committed in plaintext.`
