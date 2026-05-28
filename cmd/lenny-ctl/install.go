// SPDX-License-Identifier: MIT

package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"sigs.k8s.io/yaml"
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
//   - Interactive: with no --answer-file, the wizard prompts for each
//     answer on stdin, showing the default in brackets.
//   - Non-interactive: with --answer-file <path>, the wizard reads a
//     YAML answer file instead of prompting. This path is pure and
//     repeatable, which is what CI and IaC pipelines use.
//
// The values-composition and helm-command logic is identical across
// both modes; only answer collection differs.

// installAnswers is the §17.6 answer-file schema. Every field maps to
// one wizard question. The YAML tags define the on-disk answer-file
// keys; --answer-file decodes this struct and --save-answers re-encodes
// it, so an interactive run can be captured once and replayed.
type installAnswers struct {
	// Release is the Helm release name and target namespace.
	Release installRelease `json:"release"`
	// Environment is the target environment: local, dev, staging, or
	// prod. It drives nothing in the rendered values directly; it is
	// recorded so the answer file documents operator intent and so
	// future wizard logic can branch on it.
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
	// DevMode sets global.devMode. It is valid only for the local
	// environment and must stay false on any multi-tenant cluster.
	DevMode bool `json:"devMode,omitempty"`
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

// installObjectStorage carries the object-store endpoint and bucket.
type installObjectStorage struct {
	Endpoint string `json:"endpoint,omitempty"`
	Bucket   string `json:"bucket,omitempty"`
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
	nonInteract  bool
	dryRun       bool
	offline      bool
}

// knownTiers is the set of capacity tiers the wizard accepts. It
// matches the preset files under charts/lenny/presets/.
var knownTiers = map[string]bool{"tier1": true, "tier2": true, "tier3": true}

// knownEnvironments is the set of target environments (§17.9.1).
var knownEnvironments = map[string]bool{
	"local": true, "dev": true, "staging": true, "prod": true,
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
			fmt.Fprintln(stderr, "lenny-ctl: --non-interactive requires --answer-file <path>")
			return 2
		}
		answers = promptAnswers(bufio.NewReader(stdin), stdout)
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
	return 0
}

// parseInstallFlags parses the `install` flag set.
func parseInstallFlags(args []string) (installConfig, error) {
	cfg := installConfig{chartDir: "charts/lenny"}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		// --answer-file is the build-plan spelling; --answers is the
		// §24.20 spelling. Both name the same input.
		case "--answer-file", "--answers":
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
	return errs
}

// composeValues renders the answer set into a Helm values document.
// The result is the per-question override layer; the tier preset is
// layered under it by helm itself via a second -f argument.
func composeValues(a installAnswers) ([]byte, error) {
	values := map[string]any{}

	// global.devMode is set only when dev auth mode was selected.
	global := map[string]any{}
	if a.DevMode {
		global["devMode"] = true
	}
	if len(global) > 0 {
		values["global"] = global
	}

	if a.Postgres.DSN != "" {
		values["postgres"] = map[string]any{"dsn": a.Postgres.DSN}
	}
	if a.Redis.URL != "" {
		values["redis"] = map[string]any{"url": a.Redis.URL}
	}
	if a.ObjectStorage.Endpoint != "" || a.ObjectStorage.Bucket != "" {
		minio := map[string]any{}
		if a.ObjectStorage.Endpoint != "" {
			minio["endpoint"] = a.ObjectStorage.Endpoint
		}
		if a.ObjectStorage.Bucket != "" {
			minio["bucket"] = a.ObjectStorage.Bucket
		}
		values["minio"] = minio
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
func promptAnswers(r *bufio.Reader, w io.Writer) installAnswers {
	var a installAnswers
	fmt.Fprintln(w, "lenny-ctl install — interactive wizard")
	fmt.Fprintln(w, "Press Enter to accept the bracketed default for any question.")
	fmt.Fprintln(w)

	a.Release.Name = ask(r, w, "Release name", "lenny")
	a.Release.Namespace = ask(r, w, "Release namespace", "lenny-system")
	a.Environment = ask(r, w, "Target environment (local|dev|staging|prod)", "local")
	defTier := "tier1"
	if a.Environment == "prod" || a.Environment == "staging" {
		defTier = "tier2"
	}
	a.Tier = ask(r, w, "Capacity tier (tier1|tier2|tier3)", defTier)
	a.Domain = ask(r, w, "Gateway domain (blank to keep the chart default)", "")
	a.TLS = ask(r, w, "TLS strategy (cert-manager|bring-your-own, blank to skip)", "")
	a.Postgres.DSN = ask(r, w, "Postgres DSN (blank for the chart default)", "")
	a.Redis.URL = ask(r, w, "Redis URL (blank for the chart default)", "")
	a.ObjectStorage.Endpoint = ask(r, w, "Object storage endpoint (blank for the chart default)", "")
	a.ObjectStorage.Bucket = ask(r, w, "Object storage bucket (blank for the chart default)", "")
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
	return a
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
		"devMode",
	}
	sort.Strings(keys)
	return keys
}

const installUsage = `lenny-ctl install — Lenny installation wizard (§17.6, §24.20)

Usage:
  lenny-ctl install [flags]

Without --answer-file the wizard prompts interactively on stdin. With
--answer-file it reads a YAML answer file and runs non-interactively,
which is the repeatable path for CI and IaC.

Flags:
  --answer-file <path>   Read answers from a YAML file (alias: --answers)
  --non-interactive      Require --answer-file; never prompt
  --save-answers <path>  Write the resolved answers back to a YAML file
  --output-values <path> Write the composed Helm values to a file
  --chart <path>         Chart directory (default charts/lenny)
  --release <name>       Override the release name from the answer file
  --namespace <ns>       Override the release namespace from the answer file
  --offline              Skip cluster-reachability detection probes
  --dry-run              Print the composed values and helm command; do not run helm

Answer-file keys:
  release.name, release.namespace, environment, tier, profile, domain,
  tls, postgres.dsn, redis.url, objectStorage.endpoint,
  objectStorage.bucket, auth.mode, auth.oidcIssuer, auth.oidcClientId,
  agentNamespaces, features.llmProxy, features.drainReadiness,
  features.compliance, devMode

Environment-variable references of the form ${VAR} in the answer file
are resolved against the process environment at read time, so secret
DSNs are not committed in plaintext.`
