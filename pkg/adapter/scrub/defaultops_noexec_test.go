// SPDX-License-Identifier: MIT

package scrub

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"syscall"
	"testing"
)

const shmHeader = "       key      shmid perms                  size  cpid  lpid nattch   uid   gid  cuid  cgid      atime      dtime      ctime                   rss                  swap\n"

func shmListing(rows ...string) func() (io.ReadCloser, error) {
	return func() (io.ReadCloser, error) {
		return io.NopCloser(strings.NewReader(shmHeader + strings.Join(rows, "\n") + "\n")), nil
	}
}

// shmRow renders a listing row with the given shmid, uid, and cuid.
func shmRow(id, uid, cuid string) string {
	return "         0 " + id + "  600 4096 1 1 0 " + uid + " " + uid + " " + cuid + " " + cuid + " 0 0 0 4096 0"
}

// spec: 5.2 (Lenny scrub procedure, step 1b)
func TestPurgeIPCShmRemovesSegmentsOwnedOrCreatedByTheAdapterUID_spec_5_2(t *testing.T) {
	var removed []int
	ops := DefaultOps{
		UID: func() int { return 1000 },
		ShmList: shmListing(
			shmRow("1", "1000", "1000"), // owned and created
			shmRow("2", "2000", "1000"), // owner moved with IPC_SET; created by the adapter
			shmRow("3", "1000", "2000"), // owned by the adapter
			shmRow("4", "2000", "2000"), // another UID's
		),
		ShmRemove: func(id int) error { removed = append(removed, id); return nil },
	}
	if err := ops.PurgeIPCShm(context.Background()); err != nil {
		t.Fatal(err)
	}
	sort.Ints(removed)
	if !reflect.DeepEqual(removed, []int{1, 2, 3}) {
		t.Fatalf("removed %v, want [1 2 3]", removed)
	}
}

// spec: 5.2 (Lenny scrub procedure, step 1b)
func TestPurgeIPCShmErrors_spec_5_2(t *testing.T) {
	t.Run("an IPC_RMID error fails the step", func(t *testing.T) {
		ops := DefaultOps{
			UID: func() int { return 1 }, ShmList: shmListing(shmRow("7", "1", "1")),
			ShmRemove: func(int) error { return syscall.EPERM },
		}
		if err := ops.PurgeIPCShm(context.Background()); !errors.Is(err, syscall.EPERM) {
			t.Fatalf("PurgeIPCShm = %v, want EPERM", err)
		}
	})
	t.Run("a segment already gone is not an error", func(t *testing.T) {
		ops := DefaultOps{
			UID: func() int { return 1 }, ShmList: shmListing(shmRow("7", "1", "1")),
			ShmRemove: func(int) error { return syscall.EINVAL },
		}
		if err := ops.PurgeIPCShm(context.Background()); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("no listing is no segments", func(t *testing.T) {
		ops := DefaultOps{ShmList: func() (io.ReadCloser, error) { return nil, os.ErrNotExist }}
		if err := ops.PurgeIPCShm(context.Background()); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("an unreadable listing fails the step", func(t *testing.T) {
		ops := DefaultOps{ShmList: func() (io.ReadCloser, error) { return nil, syscall.EACCES }}
		if err := ops.PurgeIPCShm(context.Background()); err == nil {
			t.Fatal("PurgeIPCShm = nil, want an error")
		}
	})
	t.Run("a listing without the columns fails the step", func(t *testing.T) {
		ops := DefaultOps{ShmList: func() (io.ReadCloser, error) { return io.NopCloser(strings.NewReader("key\n")), nil }}
		if err := ops.PurgeIPCShm(context.Background()); err == nil {
			t.Fatal("PurgeIPCShm = nil, want an error")
		}
	})
}

// spec: 5.2 (Lenny scrub procedure, step 1; What the scrub reaches)
func TestKillUserProcessesSignalsEveryProcessWithoutExec_spec_5_2(t *testing.T) {
	var gotPid int
	var gotSig syscall.Signal
	ops := DefaultOps{Kill: func(pid int, sig syscall.Signal) error { gotPid, gotSig = pid, sig; return nil }}
	if err := ops.KillUserProcesses(context.Background()); err != nil {
		t.Fatal(err)
	}
	if gotPid != -1 || gotSig != syscall.SIGKILL {
		t.Fatalf("kill(%d, %v), want kill(-1, SIGKILL)", gotPid, gotSig)
	}
	ops.Kill = func(int, syscall.Signal) error { return syscall.ESRCH }
	if err := ops.KillUserProcesses(context.Background()); err != nil {
		t.Fatalf("ESRCH = %v, want a clean step", err)
	}
	ops.Kill = func(int, syscall.Signal) error { return syscall.EPERM }
	if err := ops.KillUserProcesses(context.Background()); err == nil {
		t.Fatal("EPERM = nil, want an error")
	}
}

// spec: 5.2 (What the scrub reaches)
func TestScrubFileOperationsFollowNoSymlink_spec_5_2(t *testing.T) {
	root := t.TempDir()
	secret := filepath.Join(root, "run-lenny")
	if err := os.MkdirAll(secret, 0o755); err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(secret, "credentials.json")
	if err := os.WriteFile(keep, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	ws := filepath.Join(root, "workspace")
	if err := os.MkdirAll(filepath.Join(ws, "slots", "s1", "deep"), 0o755); err != nil {
		t.Fatal(err)
	}
	// The agent plants links toward the platform directory, as a leaf and
	// inside a tree the scrub removes.
	if err := os.Symlink(secret, filepath.Join(ws, "slots", "s1", "deep", "planted")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(ws, "leaf")); err != nil {
		t.Fatal(err)
	}
	ops := DefaultOps{}
	exists, empty, err := ops.PathState(filepath.Join(ws, "leaf"))
	if err != nil || !exists || empty {
		t.Fatalf("PathState(link) = %v %v %v, want a dirty path", exists, empty, err)
	}
	if err := ops.RemoveAll(filepath.Join(ws, "slots", "s1")); err != nil {
		t.Fatal(err)
	}
	if err := ops.ClearContents(ws); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("the scrub followed a planted link and removed %s: %v", keep, err)
	}
	entries, _ := os.ReadDir(ws)
	if len(entries) != 0 {
		t.Fatalf("ClearContents left %v", entries)
	}
	// A symbolic link in the path itself is refused rather than followed.
	linkedWS := filepath.Join(root, "linked")
	if err := os.Symlink(secret, linkedWS); err != nil {
		t.Fatal(err)
	}
	if err := ops.ClearContents(linkedWS); err == nil {
		t.Fatal("ClearContents through a symlinked directory = nil, want a refusal")
	}
	if err := ops.RemoveAll(filepath.Join(linkedWS, "credentials.json")); err == nil {
		t.Fatal("RemoveAll beneath a symlinked directory = nil, want a refusal")
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("a refused operation removed %s", keep)
	}
	// Missing paths are clean.
	if err := ops.RemoveAll(filepath.Join(root, "absent", "x")); err != nil {
		t.Fatal(err)
	}
	if exists, empty, err := ops.PathState(filepath.Join(root, "absent")); err != nil || exists || !empty {
		t.Fatalf("PathState(absent) = %v %v %v", exists, empty, err)
	}
}
