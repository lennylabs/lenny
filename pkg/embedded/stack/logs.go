// SPDX-License-Identifier: MIT

package stack

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
)

// logComponents lists the §17.4 components lenny logs can filter to.
// Each name maps to a log file under the Logs directory.
var logComponents = []string{"gateway", "controller", "k3s", "supervisor"}

// LogsOptions configures the lenny logs command.
type LogsOptions struct {
	// Root is the Embedded Mode state directory.
	Root string
	// Component, when set, filters output to one component. Empty
	// merges every component log.
	Component string
	// Out receives the log output.
	Out io.Writer
}

// RunLogs implements the `lenny logs [<component>]` command. §17.4: it
// prints merged logs, or filters to one component. The component names
// are gateway, controller, k3s, and supervisor; the embedded Postgres
// writes to its own data directory and is surfaced through the
// supervisor log.
func RunLogs(opts LogsOptions) error {
	out := orDiscard(opts.Out)
	root, err := resolveRoot(opts.Root)
	if err != nil {
		return err
	}
	paths := NewPaths(root)

	var components []string
	if opts.Component != "" {
		if !knownLogComponent(opts.Component) {
			return fmt.Errorf("lenny logs: unknown component %q (known: %v)", opts.Component, logComponents)
		}
		components = []string{opts.Component}
	} else {
		components = append(components, logComponents...)
		sort.Strings(components)
	}

	printed := false
	for _, c := range components {
		path := logFilePath(paths, c)
		f, err := os.Open(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return fmt.Errorf("lenny logs: open %s log: %w", c, err)
		}
		printed = true
		// Prefix each line with the component name when merging more
		// than one log so the merged stream stays attributable.
		prefix := ""
		if len(components) > 1 {
			prefix = c + " | "
		}
		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for scanner.Scan() {
			fmt.Fprintf(out, "%s%s\n", prefix, scanner.Text())
		}
		_ = f.Close()
	}
	if !printed {
		fmt.Fprintln(out, "lenny logs: no log files found; run 'lenny up' first")
	}
	return nil
}

// logFilePath returns the log file path for a component.
func logFilePath(paths Paths, component string) string {
	switch component {
	case "k3s":
		// The k3s supervisor writes its process log into the k3s state
		// directory.
		return filepath.Join(paths.K3s, "k3s.log")
	default:
		return filepath.Join(paths.Logs, component+".log")
	}
}

// knownLogComponent reports whether name is a recognised log
// component.
func knownLogComponent(name string) bool {
	for _, c := range logComponents {
		if c == name {
			return true
		}
	}
	return false
}
