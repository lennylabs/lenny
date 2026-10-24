// SPDX-License-Identifier: MIT

//go:build linux

package superviseprobe

import (
	"testing"
	"unsafe"

	"golang.org/x/sys/unix"
)

// removeIPC removes the objects createIPC made.
func removeIPC(t *testing.T, r Residue) {
	t.Helper()
	if _, err := unix.SysvShmCtl(*r.ShmID, unix.IPC_RMID, nil); err != nil {
		t.Errorf("remove shm %d: %v", *r.ShmID, err)
	}
	if _, _, errno := unix.Syscall(unix.SYS_MSGCTL, uintptr(*r.MsqID), uintptr(unix.IPC_RMID), 0); errno != 0 {
		t.Errorf("remove msq %d: %v", *r.MsqID, errno)
	}
	p, err := unix.BytePtrFromString(r.MQ)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, errno := unix.Syscall(unix.SYS_MQ_UNLINK, uintptr(unsafe.Pointer(p)), 0, 0); errno != 0 {
		t.Errorf("unlink mq %s: %v", r.MQ, errno)
	}
}

// sysvShmCreate creates a segment under key.
func sysvShmCreate(key int) (int, error) {
	return unix.SysvShmGet(key, 4096, unix.IPC_CREAT|0o600)
}

// sysvShmRemove removes segment id.
func sysvShmRemove(id int) error {
	_, err := unix.SysvShmCtl(id, unix.IPC_RMID, nil)
	return err
}
