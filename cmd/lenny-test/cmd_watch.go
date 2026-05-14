// SPDX-License-Identifier: MIT

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// runWatch re-runs a selector when files under the watched paths
// change. Polling-based (mtime + size hash) so the harness has no
// fsnotify dependency.
//
// Usage:
//
//	lenny-test watch --tier unit
//	lenny-test watch --changed
//	lenny-test watch --pkg pkg/quota --interval 1s
func runWatch(args []string) int {
	fs_ := flag.NewFlagSet("watch", flag.ExitOnError)
	tier := fs_.String("tier", "", "tier to run on each change")
	group := fs_.String("group", "", "group to run on each change")
	changed := fs_.Bool("changed", false, "run --changed on each iteration")
	specs := fs_.String("spec", "", "comma-separated spec sections")
	pkgs := fs_.String("pkg", "", "comma-separated source packages")
	interval := fs_.Duration("interval", 750*time.Millisecond, "poll interval")
	watchDirs := fs_.String("dirs", "pkg,cmd,tests,schemas,scripts", "comma-separated directories to watch")
	if err := fs_.Parse(args); err != nil {
		return 2
	}
	if *tier == "" && *group == "" && !*changed && *specs == "" && *pkgs == "" {
		fmt.Fprintln(os.Stderr, "watch: at least one selector flag is required")
		return 2
	}

	// Build the lenny-test invocation that fires on each cycle.
	cmdArgs := []string{"run"}
	if *tier != "" {
		cmdArgs = append(cmdArgs, "--tier", *tier)
	}
	if *group != "" {
		cmdArgs = append(cmdArgs, "--group", *group)
	}
	if *changed {
		cmdArgs = append(cmdArgs, "--changed")
	}
	if *specs != "" {
		cmdArgs = append(cmdArgs, "--spec", *specs)
	}
	if *pkgs != "" {
		cmdArgs = append(cmdArgs, "--pkg", *pkgs)
	}
	cmdArgs = append(cmdArgs, "--output", "human")

	root := repoRoot()
	dirs := []string{}
	for _, d := range splitNonEmpty(*watchDirs) {
		dirs = append(dirs, filepath.Join(root, d))
	}

	self, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "watch: locate self: %v\n", err)
		return 1
	}

	fmt.Printf("lenny-test watch: %s\n", strings.Join(cmdArgs, " "))
	fmt.Printf("polling: %s; dirs: %s\n", *interval, strings.Join(dirs, ", "))

	// First run immediately.
	runOnce(self, cmdArgs)
	prev := snapshotTree(dirs)

	for {
		time.Sleep(*interval)
		cur := snapshotTree(dirs)
		if cur != prev {
			fmt.Println()
			fmt.Printf("watch: change detected at %s\n", time.Now().Format("15:04:05"))
			runOnce(self, cmdArgs)
			prev = cur
		}
	}
}

// runOnce invokes lenny-test in the foreground and prints its
// output as a pass-through.
func runOnce(self string, args []string) {
	c := exec.Command(self, args...)
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	_ = c.Run()
}

// snapshotTree returns a SHA-256 of every file's (mtime, size)
// tuple across the watched directories. Cheap enough for ~10k
// files; ignores .git and node_modules.
func snapshotTree(dirs []string) string {
	h := sha256.New()
	for _, root := range dirs {
		_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				base := d.Name()
				if base == ".git" || base == "node_modules" || base == "results" {
					return fs.SkipDir
				}
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return nil
			}
			fmt.Fprintf(h, "%s %d %d\n", path, info.Size(), info.ModTime().UnixNano())
			return nil
		})
	}
	return hex.EncodeToString(h.Sum(nil))
}
