// SPDX-License-Identifier: MIT

//go:build linux

package superviseprobe

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

// startDaemon double-forks a daemon in its own session: the child starts the
// grandchild with setsid and exits, so the grandchild is reparented to the
// container's PID 1 and outlives the session that started it unless the
// supervisor ends it. spec: §4.7.10 (Runtime supervisor, End).
func startDaemon() ActionResult {
	out, err := exec.Command(selfExe(), subDaemonChild).Output()
	if err != nil {
		return ActionResult{Error: fmt.Sprintf("daemon child: %v", err)}
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		return ActionResult{Error: fmt.Sprintf("daemon child printed %q: %v", out, err)}
	}
	return ActionResult{OK: true, Residue: Residue{DaemonPID: pid}}
}

// daemonChild starts the grandchild in a new session, prints its PID, and
// exits without waiting for it.
func daemonChild(stdout, stderr io.Writer) int {
	cmd := exec.Command(selfExe(), subDaemonGrandchild)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(stderr, "supervise-probe: start daemon: %v\n", err)
		return exitRuntimeError
	}
	fmt.Fprintln(stdout, cmd.Process.Pid)
	_ = cmd.Process.Release()
	return exitOK
}

// daemonGrandchild sleeps until it is killed.
func daemonGrandchild() {
	for {
		time.Sleep(time.Hour)
	}
}

// createIPC creates a System V shared-memory segment, a System V message
// queue, and a POSIX message queue owned by the agent UID, the objects the
// sweep removes on a `restart` pod. spec: §4.7.10 (Runtime supervisor,
// Sweep).
func createIPC() ActionResult {
	shm, err := unix.SysvShmGet(unix.IPC_PRIVATE, 4096, unix.IPC_CREAT|0o600)
	if err != nil {
		return ActionResult{Error: fmt.Sprintf("shmget: %v", err)}
	}
	msq, _, errno := unix.Syscall(unix.SYS_MSGGET, uintptr(unix.IPC_PRIVATE), uintptr(unix.IPC_CREAT|0o600), 0)
	if errno != 0 {
		return ActionResult{Error: fmt.Sprintf("msgget: %v", errno)}
	}
	fd, err := mqOpen(mqName, unix.O_CREAT|unix.O_RDWR)
	if err != nil {
		return ActionResult{Error: fmt.Sprintf("mq_open: %v", err)}
	}
	_ = unix.Close(fd)
	msqID := int(msq)
	return ActionResult{OK: true, Residue: Residue{ShmID: &shm, MsqID: &msqID, MQ: mqName}}
}

// mqOpen opens the POSIX message queue name (without its leading slash, the
// form the system call takes).
func mqOpen(name string, flags int) (int, error) {
	p, err := unix.BytePtrFromString(name)
	if err != nil {
		return -1, err
	}
	fd, _, errno := unix.Syscall6(unix.SYS_MQ_OPEN, uintptr(unsafe.Pointer(p)), uintptr(flags), 0o600, 0, 0, 0)
	if errno != 0 {
		return -1, errno
	}
	return int(fd), nil
}

// checkResidue decodes a Residue and reports which of its objects exist.
func checkResidue(arg string) ResidueResult {
	var r Residue
	if err := json.Unmarshal([]byte(arg), &r); err != nil {
		return ResidueResult{}
	}
	return ResidueResult{
		DaemonAlive: r.DaemonPID > 0 && daemonAlive(r.DaemonPID),
		ShmExists:   r.ShmID != nil && shmExists(*r.ShmID),
		MsqExists:   r.MsqID != nil && msqExists(*r.MsqID),
		MQExists:    r.MQ != "" && mqExists(r.MQ),
		LinkExists:  r.Link != "" && linkExists(r.Link),
	}
}

// daemonAlive reports whether pid is a live probe daemon. It reads the
// command line so a reused PID is not mistaken for the daemon.
func daemonAlive(pid int) bool {
	cmd, err := readCmdline(pid)
	if err != nil {
		return false
	}
	return len(cmd) > 1 && cmd[1] == subDaemonGrandchild && !isZombie(pid)
}

// isZombie reports whether pid has exited and awaits its reaper.
func isZombie(pid int) bool {
	f, err := os.Open(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		return true
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if v, ok := strings.CutPrefix(sc.Text(), "State:"); ok {
			return strings.HasPrefix(strings.TrimSpace(v), "Z")
		}
	}
	return false
}

// shmExists reports whether the segment id still exists.
func shmExists(id int) bool {
	var ds unix.SysvShmDesc
	_, err := unix.SysvShmCtl(id, unix.IPC_STAT, &ds)
	return err == nil
}

// msqExists reports whether the System V message queue id still exists.
func msqExists(id int) bool {
	// struct msqid_ds is at most a few hundred bytes on every Linux ABI.
	var buf [512]byte
	_, _, errno := unix.Syscall(unix.SYS_MSGCTL, uintptr(id), uintptr(unix.IPC_STAT), uintptr(unsafe.Pointer(&buf[0])))
	return errno == 0
}

// mqExists reports whether the POSIX message queue name still exists.
func mqExists(name string) bool {
	fd, err := mqOpen(name, unix.O_RDONLY)
	if err != nil {
		return !errors.Is(err, unix.ENOENT)
	}
	_ = unix.Close(fd)
	return true
}

// linkExists reports whether path is still a symbolic link.
func linkExists(path string) bool {
	fi, err := os.Lstat(path)
	return err == nil && fi.Mode()&os.ModeSymlink != 0
}

// createSetupResidue leaves the residue a setup command can leave in the
// adapter container: a daemon double-forked into its own session, outside
// the setup command's process group, and a System V shared-memory segment
// under key. spec: §5.2 (Supervised scrub sequence).
func createSetupResidue(key int) SetupResidue {
	if d := startDaemon(); !d.OK {
		return SetupResidue{Key: key, Error: d.Error}
	}
	if _, err := unix.SysvShmGet(key, 4096, unix.IPC_CREAT|0o600); err != nil {
		return SetupResidue{Key: key, Error: fmt.Sprintf("shmget key %d: %v", key, err)}
	}
	return readSetupResidue(key)
}

// readSetupResidue reports the probe daemons in this container and whether
// a segment under key exists.
func readSetupResidue(key int) SetupResidue {
	r := SetupResidue{Key: key, DaemonPIDs: []int{}}
	procs, err := listProcesses()
	if err != nil {
		r.Error = err.Error()
		return r
	}
	for _, p := range procs {
		if daemonAlive(p.PID) {
			r.DaemonPIDs = append(r.DaemonPIDs, p.PID)
		}
	}
	_, err = unix.SysvShmGet(key, 0, 0)
	r.ShmExists = err == nil
	return r
}
