// SPDX-License-Identifier: MIT

package imagetag

import (
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
)

// dockerfileInputs is what a Dockerfile reads from its build context, as
// analyzeDockerfile derives it.
type dockerfileInputs struct {
	// Sources are the context paths named by COPY and ADD instructions
	// without --from, other than the whole context. Each is a file, a
	// directory, or a glob, relative to the context root.
	Sources []string
	// GoPackages are the ./-relative package paths the Dockerfile's
	// `go build` commands compile, with build arguments substituted.
	GoPackages []string
	// WholeContext is true when a COPY or ADD copies the whole context and
	// the stage that does so runs anything other than recognized Go build
	// commands afterwards, or compiles no Go package. The image then
	// depends on every file in the context.
	WholeContext bool
}

var (
	argRef        = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}|\$([A-Za-z_][A-Za-z0-9_]*)`)
	envAssignment = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=\S*\s*`)
	goBuild       = regexp.MustCompile(`(^|[\s;&|{(])go\s+build\s`)
)

// analyzeDockerfile parses a Dockerfile and reports the context inputs its
// image depends on. args are the build arguments the build passes; they
// override the Dockerfile's ARG defaults when a COPY source or a `go build`
// package path references them.
//
// A stage that copies the whole context and then runs only `go build`, `go
// mod download`, and shell test or echo commands depends on the context only
// through the Go package closure of the packages it builds, so the caller
// hashes that closure instead of the whole context. Any other command after a
// whole-context copy sets WholeContext, because nothing bounds what such a
// command reads.
func analyzeDockerfile(text string, args map[string]string) (dockerfileInputs, error) {
	var in dockerfileInputs
	vars := map[string]string{}
	stageCopiedContext := false
	sources := map[string]bool{}
	pkgs := map[string]bool{}

	for _, line := range logicalLines(text) {
		instr, rest := splitInstruction(line)
		switch instr {
		case "ARG":
			name, def, hasDef := strings.Cut(strings.TrimSpace(rest), "=")
			name = strings.TrimSpace(name)
			if v, ok := args[name]; ok {
				vars[name] = v
			} else if hasDef {
				vars[name] = strings.Trim(strings.TrimSpace(def), `"'`)
			}
		case "FROM":
			stageCopiedContext = false
		case "COPY", "ADD":
			fields := strings.Fields(rest)
			if len(fields) > 0 && strings.HasPrefix(fields[0], "[") {
				return in, fmt.Errorf("JSON-form %s is not analyzed: %q", instr, line)
			}
			fromStage := false
			var operands []string
			for _, f := range fields {
				if strings.HasPrefix(f, "--") {
					if strings.HasPrefix(f, "--from=") {
						fromStage = true
					}
					continue
				}
				operands = append(operands, f)
			}
			if fromStage || len(operands) < 2 {
				continue
			}
			for _, src := range operands[:len(operands)-1] {
				src, err := substitute(src, vars)
				if err != nil {
					return in, err
				}
				clean := path.Clean(strings.TrimPrefix(src, "/"))
				if clean == "." {
					stageCopiedContext = true
					continue
				}
				sources[clean] = true
			}
		case "RUN":
			cmd := strings.TrimSpace(rest)
			if strings.HasPrefix(cmd, "[") {
				if stageCopiedContext {
					in.WholeContext = true
				}
				continue
			}
			found, err := goBuildPackages(cmd, vars)
			if err != nil {
				return in, err
			}
			for _, p := range found {
				pkgs[p] = true
			}
			if stageCopiedContext && !goOnlyCommand(cmd) {
				in.WholeContext = true
			}
		}
	}
	in.Sources = sortedKeys(sources)
	in.GoPackages = sortedKeys(pkgs)
	if !in.WholeContext && len(in.GoPackages) == 0 && copiesContext(text) {
		in.WholeContext = true
	}
	return in, nil
}

// copiesContext reports whether any COPY or ADD instruction copies the whole
// build context.
func copiesContext(text string) bool {
	for _, line := range logicalLines(text) {
		instr, rest := splitInstruction(line)
		if instr != "COPY" && instr != "ADD" {
			continue
		}
		var operands []string
		from := false
		for _, f := range strings.Fields(rest) {
			if strings.HasPrefix(f, "--from=") {
				from = true
			}
			if !strings.HasPrefix(f, "--") {
				operands = append(operands, f)
			}
		}
		if from || len(operands) < 2 {
			continue
		}
		for _, src := range operands[:len(operands)-1] {
			if path.Clean(strings.TrimPrefix(src, "/")) == "." {
				return true
			}
		}
	}
	return false
}

// logicalLines joins backslash continuations and drops comments and blank
// lines.
func logicalLines(text string) []string {
	var out []string
	var cur strings.Builder
	for _, raw := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(raw)
		if cur.Len() == 0 && (trimmed == "" || strings.HasPrefix(trimmed, "#")) {
			continue
		}
		if cur.Len() > 0 && strings.HasPrefix(trimmed, "#") {
			// A comment line inside a continuation is dropped by the parser.
			continue
		}
		if strings.HasSuffix(trimmed, `\`) {
			cur.WriteString(strings.TrimSuffix(trimmed, `\`))
			cur.WriteString(" ")
			continue
		}
		cur.WriteString(trimmed)
		out = append(out, cur.String())
		cur.Reset()
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

func splitInstruction(line string) (string, string) {
	instr, rest, _ := strings.Cut(line, " ")
	return strings.ToUpper(instr), rest
}

// substitute replaces $NAME and ${NAME} references with build-argument
// values. An unresolved reference is an error, because the path it names is
// unknown.
func substitute(s string, vars map[string]string) (string, error) {
	var missing string
	out := argRef.ReplaceAllStringFunc(s, func(m string) string {
		sub := argRef.FindStringSubmatch(m)
		name := sub[1]
		if name == "" {
			name = sub[2]
		}
		v, ok := vars[name]
		if !ok {
			missing = name
		}
		return v
	})
	if missing != "" {
		return "", fmt.Errorf("build argument %s is referenced in %q but has no value", missing, s)
	}
	return out, nil
}

// goBuildPackages returns the ./-relative package operands of every `go
// build` command in a RUN instruction.
func goBuildPackages(cmd string, vars map[string]string) ([]string, error) {
	var out []string
	for _, segment := range shellSegments(cmd) {
		if !goBuild.MatchString(" " + segment + " ") {
			continue
		}
		_, after, _ := strings.Cut(segment, "go build")
		for _, tok := range strings.Fields(after) {
			tok = strings.NewReplacer(`"`, "", `'`, "").Replace(tok)
			if !strings.HasPrefix(tok, "./") {
				continue
			}
			tok, err := substitute(tok, vars)
			if err != nil {
				return nil, err
			}
			out = append(out, "./"+path.Clean(tok))
		}
	}
	return out, nil
}

// shellSegments splits a shell command on the &&, ||, and ; separators.
func shellSegments(cmd string) []string {
	r := strings.NewReplacer("&&", "\x00", "||", "\x00", ";", "\x00")
	return strings.Split(r.Replace(cmd), "\x00")
}

// goOnlyCommand reports whether every segment of a RUN command is a Go build,
// a module download, or a shell test, echo, or exit. These read the context
// only through the Go package closure the caller hashes.
func goOnlyCommand(cmd string) bool {
	for _, segment := range shellSegments(cmd) {
		s := strings.TrimSpace(segment)
		s = strings.TrimSpace(strings.TrimLeft(s, "{("))
		for envAssignment.MatchString(s) {
			s = envAssignment.ReplaceAllString(s, "")
		}
		s = strings.TrimSpace(strings.TrimRight(s, "})"))
		if s == "" {
			continue
		}
		words := strings.Fields(s)
		switch words[0] {
		case "test", "echo", "exit", "true", "false":
			continue
		case "go":
			if len(words) > 1 && (words[1] == "build" || (words[1] == "mod" && len(words) > 2 && words[2] == "download")) {
				continue
			}
		}
		return false
	}
	return true
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
