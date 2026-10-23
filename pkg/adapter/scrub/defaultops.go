// SPDX-License-Identifier: MIT

package scrub

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"syscall"
)

// DefaultOps runs the real §5.2 scrub host operations inside the agent pod's
// adapter container. It runs no external command, because the distroless
// adapter image carries no shell and no ipcrm: step 1 is kill(-1, SIGKILL)
// from the adapter process, step 1b removes System V segments through
// shmctl(IPC_RMID), and every file operation resolves beneath its target
// directory with openat2 and RESOLVE_BENEATH | RESOLVE_NO_SYMLINKS, so a
// symbolic link the agent planted is never followed.
//
// The zero value runs the real operations. The function fields are test
// seams: no test outside a container may call kill(-1), and a test cannot
// create a System V segment owned by another UID.
//
// spec: §5.2 (Lenny scrub procedure; What the scrub reaches).
type DefaultOps struct {
	// Kill replaces syscall.Kill.
	Kill func(pid int, sig syscall.Signal) error
	// ShmList replaces the read of /proc/sysvipc/shm.
	ShmList func() (io.ReadCloser, error)
	// ShmRemove replaces shmctl(id, IPC_RMID).
	ShmRemove func(id int) error
	// UID replaces os.Getuid, the UID whose segments step 1b removes.
	UID func() int
}

// sysvShmPath lists the pod's System V shared-memory segments.
const sysvShmPath = "/proc/sysvipc/shm"

// KillUserProcesses runs scrub step 1: kill(-1, SIGKILL) ends every process
// the adapter's UID can signal in the adapter's container other than the
// adapter itself and the container's init. It never reaches the runtime
// container in the sidecar model, and in the embedded model it ends only the
// processes the runtime started. A namespace with no other process (ESRCH)
// is a clean step. spec: §5.2 step 1; §5.2 ("What the scrub reaches").
func (d DefaultOps) KillUserProcesses(_ context.Context) error {
	kill := d.Kill
	if kill == nil {
		kill = syscall.Kill
	}
	if err := kill(-1, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("kill(-1, SIGKILL): %w", err)
	}
	return nil
}

// PurgeIPCShm runs scrub step 1b: it removes each System V shared-memory
// segment whose owning UID or creating UID is the adapter's UID. The creating
// UID is matched as well, because an owner can move the owning UID with
// IPC_SET. A segment already gone (EINVAL, EIDRM) is not an error.
// spec: §5.2 step 1b.
func (d DefaultOps) PurgeIPCShm(_ context.Context) error {
	ids, err := d.ownedSegments()
	if err != nil {
		return err
	}
	remove := d.ShmRemove
	if remove == nil {
		remove = shmRemove
	}
	var errs []error
	for _, id := range ids {
		if err := remove(id); err != nil && !errors.Is(err, syscall.EINVAL) && !errors.Is(err, syscall.EIDRM) {
			errs = append(errs, fmt.Errorf("shmctl(%d, IPC_RMID): %w", id, err))
		}
	}
	return errors.Join(errs...)
}

// ownedSegments lists the segment ids step 1b removes. A host with no
// /proc/sysvipc/shm has no segments to remove.
func (d DefaultOps) ownedSegments() ([]int, error) {
	list := d.ShmList
	if list == nil {
		list = func() (io.ReadCloser, error) { return os.Open(sysvShmPath) }
	}
	rc, err := list()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", sysvShmPath, err)
	}
	defer func() { _ = rc.Close() }()
	uid := os.Getuid()
	if d.UID != nil {
		uid = d.UID()
	}
	return parseShmList(rc, uid)
}

// parseShmList selects from a /proc/sysvipc/shm listing the shmid of each
// row whose uid or cuid is uid. The column positions come from the header,
// which names them.
func parseShmList(r io.Reader, uid int) ([]int, error) {
	sc := bufio.NewScanner(r)
	if !sc.Scan() {
		return nil, sc.Err()
	}
	col := map[string]int{}
	for i, name := range strings.Fields(sc.Text()) {
		col[name] = i
	}
	idCol, okID := col["shmid"]
	uidCol, okUID := col["uid"]
	cuidCol, okCUID := col["cuid"]
	if !okID || !okUID || !okCUID {
		return nil, errors.New("sysvipc shm listing has no shmid, uid, or cuid column")
	}
	var ids []int
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) <= idCol || len(f) <= uidCol || len(f) <= cuidCol {
			continue
		}
		if f[uidCol] != strconv.Itoa(uid) && f[cuidCol] != strconv.Itoa(uid) {
			continue
		}
		id, err := strconv.Atoi(f[idCol])
		if err != nil {
			return nil, fmt.Errorf("sysvipc shm listing: shmid %q: %w", f[idCol], err)
		}
		ids = append(ids, id)
	}
	return ids, sc.Err()
}

// RemoveAll removes path and its contents (steps 0, 2) beneath path's parent
// directory, following no symbolic link. A missing path is not an error.
// spec: §5.2 ("What the scrub reaches").
func (d DefaultOps) RemoveAll(path string) error {
	return removeBeneath(path)
}

// ClearContents empties dir but leaves dir itself (step 4), following no
// symbolic link. A missing dir is not an error.
func (d DefaultOps) ClearContents(dir string) error {
	return clearBeneath(dir)
}

// PathState reports existence and, for a directory, emptiness (step 6),
// following no symbolic link. A symbolic link at path exists and is
// non-empty for verification purposes.
func (d DefaultOps) PathState(path string) (exists bool, empty bool, err error) {
	return pathStateBeneath(path)
}
