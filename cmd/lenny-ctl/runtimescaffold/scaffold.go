// SPDX-License-Identifier: MIT

// Package runtimescaffold generates a runtime-repository skeleton for
// the `lenny runtime init` subcommand (§24.18). It emits a Dockerfile, a
// language-specific entrypoint, a runtime.yaml, a Makefile, and a CI
// workflow into a new directory, driven by the Language × Template
// matrix in §24.18.
//
// The scaffold templates are embedded with //go:embed so the binary
// carries them with no runtime filesystem dependency.
package runtimescaffold

import (
	"embed"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"text/template"
)

// templatesFS holds the embedded scaffold templates. Each file is a
// text/template rendered against templateData.
//
//go:embed templates/*.tmpl
var templatesFS embed.FS

// Exit codes for `lenny runtime init` (§24.18). They are returned by
// Generate and surfaced as the process exit status.
const (
	// ExitOK indicates the skeleton was generated.
	ExitOK = 0
	// ExitInvalidName indicates the runtime name is outside [a-z0-9-] or
	// exceeds the length bound.
	ExitInvalidName = 2
	// ExitTargetExists indicates the target directory already exists and
	// --force was not set.
	ExitTargetExists = 3
	// ExitUnsupportedCombination indicates the Language × Template pair
	// is one of the rejected cells.
	ExitUnsupportedCombination = 5
	// ExitMissingFlag indicates --language or --template was omitted.
	ExitMissingFlag = 6
	// ExitInternalError indicates a rendering or filesystem failure.
	ExitInternalError = 1
)

// maxNameLength bounds the runtime name. It matches the runtime-name
// length bound the gateway runtime store enforces (§5.1).
const maxNameLength = 128

// namePattern is the §24.18 runtime-name format: lowercase letters,
// digits, and hyphens. A name must not start or end with a hyphen so it
// is a valid container-image path component and Kubernetes resource
// name.
var namePattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)

// Language is one of the four scaffolder languages.
type Language string

// The supported scaffolder languages (§24.18).
const (
	LangGo         Language = "go"
	LangPython     Language = "python"
	LangTypeScript Language = "typescript"
	LangBinary     Language = "binary"
)

// Template is one of the three scaffolder templates.
type Template string

// The supported scaffolder templates (§24.18).
const (
	TemplateMinimal Template = "minimal"
	TemplateCoding  Template = "coding"
	TemplateChat    Template = "chat"
)

// Spec is a fully parsed `lenny runtime init` invocation.
type Spec struct {
	// Name is the runtime name and the target directory name.
	Name string
	// Language selects the entrypoint language.
	Language Language
	// Template selects the pre-wiring template.
	Template Template
	// Org is the organization slug used in the generated module path and
	// image reference. It defaults to "acme" when the caller leaves it
	// empty.
	Org string
	// Force overwrites an existing target directory when true.
	Force bool
}

// templateData is the value every scaffold template is rendered
// against.
type templateData struct {
	Name             string
	Org              string
	Language         string
	Template         string
	Image            string
	IntegrationLevel string
	EntrypointFile   string
}

// integrationLevelFor returns the §24.18 integration level for a
// Language × Template cell. The binary language is always Basic; the
// minimal template is Standard for the SDK languages; coding and chat
// are Full.
func integrationLevelFor(lang Language, tmpl Template) string {
	if lang == LangBinary {
		return "basic"
	}
	if tmpl == TemplateMinimal {
		return "standard"
	}
	return "full"
}

// entrypointFileFor returns the entrypoint file name for a language.
func entrypointFileFor(lang Language) string {
	switch lang {
	case LangGo, LangBinary:
		return "main.go"
	case LangPython:
		return "main.py"
	case LangTypeScript:
		return "main.ts"
	default:
		return ""
	}
}

// file is one emitted skeleton file: the embedded template that
// produces it and the path it is written to, relative to the target
// directory.
type file struct {
	templateName string
	outPath      string
}

// fileSet returns the ordered set of files a Language × Template cell
// emits. The caller has already rejected unsupported combinations, so
// every (lang, tmpl) pair reaching this function is valid.
func fileSet(lang Language, tmpl Template) []file {
	entrypoint := entrypointFileFor(lang)
	ci := ".github/workflows/ci.yml"

	switch lang {
	case LangBinary:
		// The binary language emits a Basic-level Go skeleton with no
		// SDK dependency. Its go.mod carries no require directive.
		return []file{
			{"binary-minimal.main.go.tmpl", entrypoint},
			{"runtime-binary.yaml.tmpl", "runtime.yaml"},
			{"go.Dockerfile.tmpl", "Dockerfile"},
			{"go.Makefile.tmpl", "Makefile"},
			{"go.ci.yml.tmpl", ci},
			{"binary.go.mod.tmpl", "go.mod"},
			{"go.gitignore.tmpl", ".gitignore"},
			{"README.md.tmpl", "README.md"},
		}

	case LangGo:
		return []file{
			{"go-" + string(tmpl) + ".main.go.tmpl", entrypoint},
			{runtimeYAMLTemplate(tmpl), "runtime.yaml"},
			{"go.Dockerfile.tmpl", "Dockerfile"},
			{"go.Makefile.tmpl", "Makefile"},
			{"go.ci.yml.tmpl", ci},
			{"go.go.mod.tmpl", "go.mod"},
			{"go.gitignore.tmpl", ".gitignore"},
			{"README.md.tmpl", "README.md"},
		}

	case LangTypeScript:
		return []file{
			{"typescript-" + string(tmpl) + ".main.ts.tmpl", entrypoint},
			{runtimeYAMLTemplate(tmpl), "runtime.yaml"},
			{"typescript.Dockerfile.tmpl", "Dockerfile"},
			{"typescript.Makefile.tmpl", "Makefile"},
			{"typescript.ci.yml.tmpl", ci},
			{"typescript.package.json.tmpl", "package.json"},
			{"typescript.tsconfig.json.tmpl", "tsconfig.json"},
			{"typescript.gitignore.tmpl", ".gitignore"},
			{"README.md.tmpl", "README.md"},
		}

	case LangPython:
		return []file{
			{"python-" + string(tmpl) + ".main.py.tmpl", entrypoint},
			{runtimeYAMLTemplate(tmpl), "runtime.yaml"},
			{"python.Dockerfile.tmpl", "Dockerfile"},
			{"python.Makefile.tmpl", "Makefile"},
			{"python.ci.yml.tmpl", ci},
			{"python.requirements.txt.tmpl", "requirements.txt"},
			{"python.pyproject.toml.tmpl", "pyproject.toml"},
			{"python.gitignore.tmpl", ".gitignore"},
			{"README.md.tmpl", "README.md"},
		}

	default:
		return nil
	}
}

// runtimeYAMLTemplate returns the runtime.yaml template for a template
// kind. The minimal template uses the Standard-level manifest; coding
// and chat use their dedicated Full-level manifests.
func runtimeYAMLTemplate(tmpl Template) string {
	switch tmpl {
	case TemplateCoding:
		return "runtime-coding.yaml.tmpl"
	case TemplateChat:
		return "runtime-chat.yaml.tmpl"
	default:
		return "runtime-minimal.yaml.tmpl"
	}
}

// supportedLanguages and supportedTemplates back the flag-validation
// error messages.
var (
	supportedLanguages = []Language{LangGo, LangPython, LangTypeScript, LangBinary}
	supportedTemplates = []Template{TemplateMinimal, TemplateCoding, TemplateChat}
)

// ValidateLanguage reports whether s names a supported language.
func ValidateLanguage(s string) (Language, bool) {
	for _, l := range supportedLanguages {
		if string(l) == s {
			return l, true
		}
	}
	return "", false
}

// ValidateTemplate reports whether s names a supported template.
func ValidateTemplate(s string) (Template, bool) {
	for _, t := range supportedTemplates {
		if string(t) == s {
			return t, true
		}
	}
	return "", false
}

// IsUnsupportedCombination reports whether a Language × Template pair is
// one of the rejected cells. The binary language supports only the
// minimal template; binary+coding and binary+chat are rejected (§24.18).
func IsUnsupportedCombination(lang Language, tmpl Template) bool {
	return lang == LangBinary && tmpl != TemplateMinimal
}

// validateName checks the runtime name against the §24.18 format.
func validateName(name string) error {
	if name == "" {
		return fmt.Errorf("runtime name is empty")
	}
	if len(name) > maxNameLength {
		return fmt.Errorf("runtime name %q exceeds the %d-character bound", name, maxNameLength)
	}
	if !namePattern.MatchString(name) {
		return fmt.Errorf(
			"runtime name %q must contain only lowercase letters, digits, and hyphens, "+
				"and must not start or end with a hyphen", name,
		)
	}
	return nil
}

// Generate scaffolds the runtime skeleton described by spec into
// baseDir/<name>/. It returns one of the Exit* codes. Informational
// output goes to stdout; errors go to stderr.
//
// The combination check runs before any file is written, so a rejected
// Language × Template pair leaves the filesystem untouched (§24.18).
func Generate(spec Spec, baseDir string, stdout, stderr io.Writer) int {
	if err := validateName(spec.Name); err != nil {
		fmt.Fprintf(stderr, "lenny runtime init: %v\n", err)
		return ExitInvalidName
	}

	if IsUnsupportedCombination(spec.Language, spec.Template) {
		fmt.Fprintf(stderr,
			"lenny runtime init: --language %s --template %s is not a supported "+
				"combination. The binary language emits a Basic-level skeleton with "+
				"no MCP client or lifecycle channel, which the %s template requires. "+
				"Use --language binary --template minimal for a Basic-level binary, "+
				"or choose --language {go|python|typescript} for the %s template. "+
				"See the Language x Template matrix in the lenny-ctl command "+
				"reference and the runtime integration levels in the external API "+
				"surface specification.\n",
			spec.Language, spec.Template, spec.Template, spec.Template)
		return ExitUnsupportedCombination
	}

	org := spec.Org
	if org == "" {
		org = "acme"
	}

	targetDir := filepath.Join(baseDir, spec.Name)
	if _, err := os.Stat(targetDir); err == nil {
		if !spec.Force {
			fmt.Fprintf(stderr,
				"lenny runtime init: target directory %s already exists; "+
					"pass --force to overwrite\n", targetDir)
			return ExitTargetExists
		}
	} else if !os.IsNotExist(err) {
		fmt.Fprintf(stderr, "lenny runtime init: stat %s: %v\n", targetDir, err)
		return ExitInternalError
	}

	level := integrationLevelFor(spec.Language, spec.Template)
	data := templateData{
		Name:             spec.Name,
		Org:              org,
		Language:         string(spec.Language),
		Template:         string(spec.Template),
		Image:            fmt.Sprintf("ghcr.io/%s/runtime-%s:0.1.0", org, spec.Name),
		IntegrationLevel: level,
		EntrypointFile:   entrypointFileFor(spec.Language),
	}

	files := fileSet(spec.Language, spec.Template)
	if len(files) == 0 {
		// Unreachable for a validated spec; guards against a future
		// language being added to the enum without a file set.
		fmt.Fprintf(stderr, "lenny runtime init: no file set for language %q\n", spec.Language)
		return ExitInternalError
	}

	// Render every file into memory before writing any of them, so a
	// template error does not leave a half-written skeleton.
	rendered := make(map[string][]byte, len(files))
	for _, f := range files {
		out, err := renderTemplate(f.templateName, data)
		if err != nil {
			fmt.Fprintf(stderr, "lenny runtime init: render %s: %v\n", f.templateName, err)
			return ExitInternalError
		}
		rendered[f.outPath] = out
	}

	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		fmt.Fprintf(stderr, "lenny runtime init: create %s: %v\n", targetDir, err)
		return ExitInternalError
	}

	// Write in a deterministic order so the informational output is
	// stable across runs.
	outPaths := make([]string, 0, len(rendered))
	for p := range rendered {
		outPaths = append(outPaths, p)
	}
	sort.Strings(outPaths)

	for _, p := range outPaths {
		full := filepath.Join(targetDir, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			fmt.Fprintf(stderr, "lenny runtime init: create %s: %v\n", filepath.Dir(full), err)
			return ExitInternalError
		}
		if err := os.WriteFile(full, rendered[p], 0o644); err != nil {
			fmt.Fprintf(stderr, "lenny runtime init: write %s: %v\n", full, err)
			return ExitInternalError
		}
	}

	fmt.Fprintf(stdout, "Scaffolded %s runtime %q (%s template, integration level %s) in %s\n",
		spec.Language, spec.Name, spec.Template, level, targetDir)
	for _, p := range outPaths {
		fmt.Fprintf(stdout, "  %s\n", filepath.Join(spec.Name, p))
	}
	return ExitOK
}

// renderTemplate executes one embedded template against data.
//
// CI-workflow templates use the [[ ]] action delimiters instead of the
// default {{ }}. GitHub Actions expressions are written ${{ ... }}, and
// the default Go-template delimiter would capture the inner {{ ... }};
// the [[ ]] delimiter keeps the GitHub expressions literal.
func renderTemplate(name string, data templateData) ([]byte, error) {
	raw, err := templatesFS.ReadFile("templates/" + name)
	if err != nil {
		return nil, err
	}
	tmpl := template.New(name)
	if strings.HasSuffix(name, ".ci.yml.tmpl") {
		tmpl = tmpl.Delims("[[", "]]")
	}
	// Option "missingkey=error" turns a typo in a template field into a
	// render error instead of an "<no value>" literal in the output.
	tmpl, err = tmpl.Option("missingkey=error").Parse(string(raw))
	if err != nil {
		return nil, err
	}
	var buf strings.Builder
	if err := tmpl.Execute(&buf, data); err != nil {
		return nil, err
	}
	return []byte(buf.String()), nil
}

// EmittedFiles returns the relative paths a Language × Template cell
// emits, sorted. It backs the scaffolder unit tests and is exported so
// callers can describe a skeleton without generating it.
func EmittedFiles(lang Language, tmpl Template) []string {
	if IsUnsupportedCombination(lang, tmpl) {
		return nil
	}
	files := fileSet(lang, tmpl)
	paths := make([]string, 0, len(files))
	for _, f := range files {
		paths = append(paths, f.outPath)
	}
	sort.Strings(paths)
	return paths
}
