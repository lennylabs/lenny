// SPDX-License-Identifier: MIT

package stack

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// logComponents lists the §17.4 / §24.19 components lenny logs can
// filter to by exact name. Each maps to a log file under the Logs
// directory; `k3s` is special-cased to the K3s state directory.
//
// spec: §17.4 line 179, §24.19 line 263.
var logComponents = []string{
	"gateway",
	"controller",
	"ops",
	"postgres",
	"redis",
	"kms",
	"oidc",
	"k3s",
	"supervisor",
}

// LogsOptions configures the lenny logs command.
type LogsOptions struct {
	// Root is the Embedded Mode state directory.
	Root string
	// Component, when set, filters output to one component. An empty
	// string merges every component log. The literal "runtime" expands
	// to every runtime-<name>.log under the Logs directory; an explicit
	// "runtime-<name>" selects exactly one runtime log.
	Component string
	// Follow tails the selected log files in §24.19 line 263 `--follow`
	// mode: RunLogs blocks, polling for new lines, until ctx is
	// cancelled. The poll interval is FollowInterval.
	Follow bool
	// FollowInterval is the poll interval for Follow mode. Zero
	// requests the default (250ms).
	FollowInterval time.Duration
	// Out receives the log output.
	Out io.Writer
}

// RunLogs implements the `lenny logs [<component>] [--follow]` command.
// §17.4 / §24.19: it prints merged logs, or filters to one component.
//
// spec: §17.4 line 179, §24.19 line 263.
func RunLogs(ctx context.Context, opts LogsOptions) error {
	out := orDiscard(opts.Out)
	root, err := resolveRoot(opts.Root)
	if err != nil {
		return err
	}
	paths := NewPaths(root)

	components, err := resolveLogComponents(paths, opts.Component)
	if err != nil {
		return err
	}

	if !opts.Follow {
		return printOnce(out, paths, components)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	interval := opts.FollowInterval
	if interval <= 0 {
		interval = 250 * time.Millisecond
	}
	return followLogs(ctx, out, paths, components, interval)
}

// resolveLogComponents expands the user-supplied component filter into
// the concrete set of component log files to display. The `runtime`
// alias expands to every runtime-<name>.log on disk; an explicit
// runtime-<name> resolves to that single file.
func resolveLogComponents(paths Paths, requested string) ([]string, error) {
	if requested == "" {
		// Merge: every canonical component plus any runtime-<name> logs
		// present under the Logs directory.
		merged := append([]string{}, logComponents...)
		runtimes, err := listRuntimeLogs(paths)
		if err != nil {
			return nil, err
		}
		merged = append(merged, runtimes...)
		sort.Strings(merged)
		return merged, nil
	}
	if requested == "runtime" {
		runtimes, err := listRuntimeLogs(paths)
		if err != nil {
			return nil, err
		}
		if len(runtimes) == 0 {
			return nil, fmt.Errorf("lenny logs: no runtime-*.log files present under %s", paths.Logs)
		}
		sort.Strings(runtimes)
		return runtimes, nil
	}
	if strings.HasPrefix(requested, "runtime-") && len(requested) > len("runtime-") {
		return []string{requested}, nil
	}
	if !knownLogComponent(requested) {
		return nil, fmt.Errorf("lenny logs: unknown component %q (known: %v or runtime-<name>)", requested, logComponents)
	}
	return []string{requested}, nil
}

// listRuntimeLogs returns the component names of every runtime-<name>.log
// present under the Logs directory.
func listRuntimeLogs(paths Paths) ([]string, error) {
	entries, err := os.ReadDir(paths.Logs)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("lenny logs: read %s: %w", paths.Logs, err)
	}
	var out []string
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "runtime-") || !strings.HasSuffix(name, ".log") {
			continue
		}
		out = append(out, strings.TrimSuffix(name, ".log"))
	}
	return out, nil
}

// printOnce is the non-follow path: read each component log to EOF and
// emit, prefixing lines with the component name when merging.
func printOnce(out io.Writer, paths Paths, components []string) error {
	printed := false
	prefix := ""
	for _, c := range components {
		f, err := os.Open(logFilePath(paths, c))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return fmt.Errorf("lenny logs: open %s log: %w", c, err)
		}
		printed = true
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

// logTailer streams new lines from one component log.
type logTailer struct {
	name   string
	path   string
	f      *os.File
	reader *bufio.Reader
}

// drain reads any new lines from the underlying file and emits them with
// the component prefix when merging more than one component.
func (t *logTailer) drain(out io.Writer, merging bool) error {
	if t.f == nil {
		f, err := os.Open(t.path)
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return fmt.Errorf("lenny logs: open %s log: %w", t.name, err)
		}
		t.f = f
		t.reader = bufio.NewReader(f)
	}
	for {
		line, err := t.reader.ReadString('\n')
		if line != "" {
			if merging {
				fmt.Fprintf(out, "%s | %s", t.name, line)
			} else {
				fmt.Fprint(out, line)
			}
		}
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return fmt.Errorf("lenny logs: read %s log: %w", t.name, err)
		}
	}
}

// followLogs streams new lines from each component log until ctx is
// cancelled. It tolerates missing files (a component that has not yet
// emitted a log) and re-opens files that are rotated underneath it.
//
// spec: §24.19 line 263 `--follow`.
func followLogs(ctx context.Context, out io.Writer, paths Paths, components []string, interval time.Duration) error {
	merging := len(components) > 1
	tailers := make([]*logTailer, 0, len(components))
	for _, c := range components {
		tailers = append(tailers, &logTailer{name: c, path: logFilePath(paths, c)})
	}
	defer func() {
		for _, t := range tailers {
			if t.f != nil {
				_ = t.f.Close()
			}
		}
	}()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		for _, t := range tailers {
			if err := t.drain(out, merging); err != nil {
				return err
			}
		}
		select {
		case <-ctx.Done():
			// Drain once more so any final lines reach the writer.
			for _, t := range tailers {
				if err := t.drain(out, merging); err != nil {
					return err
				}
			}
			return nil
		case <-ticker.C:
		}
	}
}

// logFilePath returns the log file path for a component name. The
// caller has already validated the name.
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

// knownLogComponent reports whether name matches a §24.19 line 263
// component allow-list entry. The `runtime-<name>` form is handled by
// the caller via prefix check rather than enumeration.
func knownLogComponent(name string) bool {
	for _, c := range logComponents {
		if c == name {
			return true
		}
	}
	return false
}
