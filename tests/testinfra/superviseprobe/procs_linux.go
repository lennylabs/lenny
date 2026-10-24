// SPDX-License-Identifier: MIT

//go:build linux

package superviseprobe

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"syscall"
)

// listProcesses reads every process of the container's PID namespace from
// /proc. A process that exits during the walk is skipped.
func listProcesses() ([]Process, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, fmt.Errorf("read /proc: %w", err)
	}
	out := []Process{}
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		p, err := readProcess(pid)
		if err != nil {
			continue
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PID < out[j].PID })
	return out, nil
}

// readProcess reads one process's status and command line.
func readProcess(pid int) (Process, error) {
	p := Process{PID: pid}
	f, err := os.Open(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		return p, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		key, val, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		fields := strings.Fields(val)
		if len(fields) == 0 {
			continue
		}
		switch key {
		case "State":
			p.State = fields[0]
		case "PPid":
			p.PPID, _ = strconv.Atoi(fields[0])
		case "Uid":
			p.UID, _ = strconv.Atoi(fields[0])
		}
	}
	// A kernel thread or a process whose cmdline the caller cannot read has
	// an empty command line; that is not an error.
	p.Cmdline, _ = readCmdline(pid)
	return p, nil
}

// readCmdline reads /proc/<pid>/cmdline as its NUL-separated arguments.
func readCmdline(pid int) ([]string, error) {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
	if err != nil {
		return nil, err
	}
	b = bytes.TrimRight(b, "\x00")
	if len(b) == 0 {
		return nil, nil
	}
	return strings.Split(string(b), "\x00"), nil
}

// IsServingProbe reports whether p is a serving probe: the probe binary with
// no subcommand, outside PID 1, which is the supervisor.
func IsServingProbe(p Process, exe string) bool {
	return p.PID != 1 && len(p.Cmdline) == 1 && p.Cmdline[0] == exe && p.State != "Z"
}

// killRuntime sends SIGKILL to every serving probe process in the container,
// which on a supervised pod is the runtime process. spec: §4.7.10 (Runtime
// supervisor, Runtime exits).
func killRuntime() AttackResult {
	res := AttackResult{Attack: SubKillRuntime}
	procs, err := listProcesses()
	if err != nil {
		res.Detail = err.Error()
		return res
	}
	exe := selfExe()
	for _, p := range procs {
		if p.PID == os.Getpid() || !IsServingProbe(p, exe) {
			continue
		}
		if err := syscall.Kill(p.PID, syscall.SIGKILL); err != nil {
			res.Errno = errnoName(err)
			continue
		}
		res.OK = true
		res.Detail = strings.TrimSpace(res.Detail + " " + strconv.Itoa(p.PID))
	}
	if !res.OK && res.Errno == "" {
		res.Detail = "no serving probe process"
	}
	return res
}
