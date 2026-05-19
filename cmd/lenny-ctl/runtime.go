// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"

	"sigs.k8s.io/yaml"

	"github.com/lennylabs/lenny/cmd/lenny-ctl/runtimescaffold"
	"github.com/lennylabs/lenny/pkg/ctl"
)

// runtime.go implements the §24.18 `lenny runtime` command group:
//
//   - `lenny runtime init`     — offline scaffolder; emits a runtime
//     repository skeleton. No gateway call.
//   - `lenny runtime validate` — static validation of a runtime
//     repository against the §15.4 adapter spec and the §5.1
//     runtime.yaml contract. No gateway call.
//   - `lenny runtime publish`  — pushes the built image with `docker
//     push` and registers the runtime against the gateway, reusing the
//     `admin runtimes register` path.
//
// init and validate are local; publish is the only subcommand that
// reaches the gateway, so it is dispatched with the gateway client.

// cmdRuntime dispatches the §24.18 `runtime` command group. init and
// validate are local; publish takes the gateway client.
func cmdRuntime(ctx context.Context, c *ctl.Client, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "lenny runtime: requires a subcommand (init|validate|publish)")
		fmt.Fprintln(stderr, runtimeUsage)
		return 2
	}
	switch args[0] {
	case "-h", "--help", "help":
		fmt.Fprintln(stdout, runtimeUsage)
		return 0
	case "init":
		return cmdRuntimeInit(args[1:], stdout, stderr)
	case "validate":
		return cmdRuntimeValidate(args[1:], stdout, stderr)
	case "publish":
		return cmdRuntimePublish(ctx, c, args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "lenny runtime: unknown subcommand %q\n", args[0])
		fmt.Fprintln(stderr, runtimeUsage)
		return 2
	}
}

// cmdRuntimeInit parses the `runtime init` flag set and runs the
// scaffolder. It returns the scaffolder exit code directly (§24.18).
func cmdRuntimeInit(args []string, stdout, stderr io.Writer) int {
	for _, a := range args {
		if a == "-h" || a == "--help" {
			fmt.Fprintln(stdout, runtimeInitUsage)
			return 0
		}
	}

	var name, language, template, org string
	var force bool
	var langSet, tmplSet bool
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--language":
			if i+1 >= len(args) {
				fmt.Fprintln(stderr, "lenny runtime init: --language requires a value")
				return runtimescaffold.ExitMissingFlag
			}
			language, langSet, i = args[i+1], true, i+1
		case "--template":
			if i+1 >= len(args) {
				fmt.Fprintln(stderr, "lenny runtime init: --template requires a value")
				return runtimescaffold.ExitMissingFlag
			}
			template, tmplSet, i = args[i+1], true, i+1
		case "--org":
			if i+1 >= len(args) {
				fmt.Fprintln(stderr, "lenny runtime init: --org requires a value")
				return runtimescaffold.ExitInvalidName
			}
			org, i = args[i+1], i+1
		case "--force":
			force = true
		default:
			if len(args[i]) > 0 && args[i][0] == '-' {
				fmt.Fprintf(stderr, "lenny runtime init: unknown flag %q\n", args[i])
				return runtimescaffold.ExitMissingFlag
			}
			if name != "" {
				fmt.Fprintf(stderr, "lenny runtime init: unexpected argument %q\n", args[i])
				return runtimescaffold.ExitInvalidName
			}
			name = args[i]
		}
	}

	if name == "" {
		fmt.Fprintln(stderr, "lenny runtime init: requires a <name> argument")
		fmt.Fprintln(stderr, runtimeInitUsage)
		return runtimescaffold.ExitInvalidName
	}
	// --language and --template are both required (§24.18 exit code 6).
	if !langSet {
		fmt.Fprintln(stderr, "lenny runtime init: --language is required (go|python|typescript|binary)")
		return runtimescaffold.ExitMissingFlag
	}
	if !tmplSet {
		fmt.Fprintln(stderr, "lenny runtime init: --template is required (chat|coding|minimal)")
		return runtimescaffold.ExitMissingFlag
	}

	lang, ok := runtimescaffold.ValidateLanguage(language)
	if !ok {
		fmt.Fprintf(stderr,
			"lenny runtime init: --language %q is not one of go, python, typescript, or binary\n",
			language)
		return runtimescaffold.ExitMissingFlag
	}
	tmpl, ok := runtimescaffold.ValidateTemplate(template)
	if !ok {
		fmt.Fprintf(stderr,
			"lenny runtime init: --template %q is not one of chat, coding, or minimal\n",
			template)
		return runtimescaffold.ExitMissingFlag
	}

	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(stderr, "lenny runtime init: resolve working directory: %v\n", err)
		return runtimescaffold.ExitInternalError
	}

	return runtimescaffold.Generate(runtimescaffold.Spec{
		Name:     name,
		Language: lang,
		Template: tmpl,
		Org:      org,
		Force:    force,
	}, cwd, stdout, stderr)
}

// cmdRuntimeValidate parses the `runtime validate` flag set and runs the
// static validator (§24.18).
func cmdRuntimeValidate(args []string, stdout, stderr io.Writer) int {
	path := "."
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-h", "--help":
			fmt.Fprintln(stdout, runtimeValidateUsage)
			return 0
		default:
			if len(args[i]) > 0 && args[i][0] == '-' {
				fmt.Fprintf(stderr, "lenny runtime validate: unknown flag %q\n", args[i])
				return runtimescaffold.ValidateUsage
			}
			path = args[i]
		}
	}
	return runtimescaffold.Validate(path, stdout, stderr)
}

// cmdRuntimePublish implements `lenny runtime publish` (§24.18). It
// pushes the built image with `docker push` and registers the runtime
// against the gateway, reusing the `admin runtimes register` path.
func cmdRuntimePublish(ctx context.Context, c *ctl.Client, args []string, stdout, stderr io.Writer) int {
	for _, a := range args {
		if a == "-h" || a == "--help" {
			fmt.Fprintln(stdout, runtimePublishUsage)
			return 0
		}
	}

	var name, image, manifestPath string
	skipPush := false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--image":
			if i+1 >= len(args) {
				fmt.Fprintln(stderr, "lenny runtime publish: --image requires a value")
				return 2
			}
			image, i = args[i+1], i+1
		case "--manifest":
			if i+1 >= len(args) {
				fmt.Fprintln(stderr, "lenny runtime publish: --manifest requires a path")
				return 2
			}
			manifestPath, i = args[i+1], i+1
		case "--skip-push":
			// --skip-push registers a runtime whose image is already in
			// the registry, skipping the docker push step.
			skipPush = true
		default:
			if len(args[i]) > 0 && args[i][0] == '-' {
				fmt.Fprintf(stderr, "lenny runtime publish: unknown flag %q\n", args[i])
				return 2
			}
			if name != "" {
				fmt.Fprintf(stderr, "lenny runtime publish: unexpected argument %q\n", args[i])
				return 2
			}
			name = args[i]
		}
	}

	if name == "" {
		fmt.Fprintln(stderr, "lenny runtime publish: requires a <name> argument")
		fmt.Fprintln(stderr, runtimePublishUsage)
		return 2
	}
	if image == "" {
		fmt.Fprintln(stderr, "lenny runtime publish: --image <ref> is required")
		return 2
	}

	// Push the image unless the caller opted out. docker push is a
	// subprocess; a missing docker binary is a clear up-front error.
	if !skipPush {
		if _, err := exec.LookPath("docker"); err != nil {
			fmt.Fprintln(stderr,
				"lenny runtime publish: docker not found on PATH; install Docker "+
					"or re-run with --skip-push if the image is already pushed")
			return 1
		}
		fmt.Fprintf(stderr, "lenny runtime publish: pushing %s\n", image)
		cmd := exec.Command("docker", "push", image)
		cmd.Stdout = stdout
		cmd.Stderr = stderr
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(stderr, "lenny runtime publish: docker push failed: %v\n", err)
			return 1
		}
	}

	// Build the registration body. --manifest names a runtime.yaml whose
	// fields become the registration request; without it, a minimal
	// {name, image, type: agent} record is registered.
	body, err := publishRegistrationBody(name, image, manifestPath)
	if err != nil {
		fmt.Fprintf(stderr, "lenny runtime publish: %v\n", err)
		return 1
	}

	// Register against the gateway, reusing the admin runtimes register
	// path (POST /v1/admin/runtimes).
	if code := registerRuntime(ctx, c, body, stdout, stderr); code != 0 {
		return code
	}
	fmt.Fprintf(stderr, "lenny runtime publish: runtime %q registered\n", name)
	return 0
}

// publishRegistrationBody builds the §15.1 runtime-registration request
// body. When manifestPath is set, the runtime.yaml fields drive the
// request and the name/image arguments override the manifest's. Without
// a manifest, a minimal agent record is registered.
func publishRegistrationBody(name, image, manifestPath string) (map[string]any, error) {
	body := map[string]any{}
	if manifestPath != "" {
		raw, err := os.ReadFile(manifestPath)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", manifestPath, err)
		}
		if err := yaml.Unmarshal(raw, &body); err != nil {
			return nil, fmt.Errorf("%s is not valid YAML: %w", manifestPath, err)
		}
	}
	// The name and image arguments are authoritative — they name the
	// runtime being published and the image that was just pushed.
	body["name"] = name
	body["image"] = image
	if _, ok := body["type"]; !ok {
		body["type"] = "agent"
	}
	return body, nil
}

// registerRuntime registers a runtime definition against the gateway.
// It is the shared `admin runtimes register` path: `lenny runtime
// publish` and `lenny-ctl admin runtimes register` both call it.
func registerRuntime(ctx context.Context, c *ctl.Client, body map[string]any, stdout, stderr io.Writer) int {
	var out map[string]any
	if err := c.Do(ctx, "POST", "/v1/admin/runtimes", body, &out); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	printJSON(stdout, out)
	return 0
}

const runtimeUsage = `lenny runtime — Lenny runtime authoring commands (§24.18)

Usage:
  lenny runtime <subcommand> [args]

Subcommands:
  init <name> --language <l> --template <t>   Scaffold a new runtime repository
  validate [<path>]                           Statically validate a runtime repository
  publish <name> --image <ref>                Push the image and register the runtime

Run "lenny runtime <subcommand> --help" for subcommand detail.`

const runtimeInitUsage = `lenny runtime init — scaffold a new runtime repository (§24.18)

Usage:
  lenny runtime init <name> --language {go|python|typescript|binary} --template {chat|coding|minimal} [flags]

The skeleton is determined by the Language x Template matrix in §24.18:

  - go, python, typescript support every template. minimal is a
    Standard-level skeleton that imports the runtime-author SDK; coding
    and chat are Full-level skeletons wired to the shared coding-agent
    and chat reference patterns.
  - binary supports only minimal: a Basic-level skeleton with no SDK
    import, just the stdin/stdout JSON Lines loop. binary+coding and
    binary+chat are rejected.

Flags:
  --language <l>   Entrypoint language: go, python, typescript, or binary (required)
  --template <t>   Template: chat, coding, or minimal (required)
  --org <slug>     Organization slug for the module path and image (default acme)
  --force          Overwrite the target directory if it already exists

Exit codes:
  0  skeleton generated
  2  runtime name is invalid
  3  target directory exists and --force was not set
  5  the Language x Template combination is not supported
  6  --language or --template was omitted`

const runtimeValidateUsage = `lenny runtime validate — statically validate a runtime repository (§24.18)

Usage:
  lenny runtime validate [<path>]

Validates the runtime repository at <path> (default the current
directory) against the §5.1 runtime.yaml contract and the §15.4 adapter
specification's repository expectations.

This is a static validator: it checks the runtime.yaml structure and the
repository layout. It does not start the runtime, so it reports the
declared integration level only. To reconcile declared against observed,
register the runtime with a gateway and inspect the lifecycle handshake.

Exit codes:
  0  the repository passed every static check
  1  one or more static checks failed
  2  the path argument could not be read`

const runtimePublishUsage = `lenny runtime publish — push and register a runtime (§24.18)

Usage:
  lenny runtime publish <name> --image <ref> [flags]

Pushes the built image with "docker push" and registers the runtime
against the gateway (POST /v1/admin/runtimes). Requires --api-url and an
admin token for the register step.

Flags:
  --image <ref>     Image reference to push and register (required)
  --manifest <path> runtime.yaml whose fields drive the registration request
  --skip-push       Skip "docker push"; register an image already in the registry`
