// SPDX-License-Identifier: MIT

//go:build linux

package scrub

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// This file resolves the scrub's file operations with openat2. The parent of
// each target is opened with RESOLVE_NO_SYMLINKS, so a symbolic link anywhere
// in the platform path fails the operation rather than redirecting it, and
// every entry beneath is opened relative to its directory with
// RESOLVE_BENEATH | RESOLVE_NO_SYMLINKS. Removal uses unlinkat, which never
// follows a link: a planted link is removed as a link.
//
// spec: §5.2 ("What the scrub reaches").

// resolveNoSymlinks is the resolve set of every open beneath a directory.
const resolveBeneath = unix.RESOLVE_BENEATH | unix.RESOLVE_NO_SYMLINKS

// openDir opens an absolute directory path with no symbolic link in it.
func openDir(path string) (int, error) {
	fd, err := unix.Openat2(unix.AT_FDCWD, path, &unix.OpenHow{
		Flags:   unix.O_RDONLY | unix.O_DIRECTORY | unix.O_CLOEXEC,
		Resolve: unix.RESOLVE_NO_SYMLINKS,
	})
	if err != nil {
		return -1, fmt.Errorf("open %s: %w", path, err)
	}
	return fd, nil
}

// openChildDir opens the directory name beneath dirfd.
func openChildDir(dirfd int, name string) (int, error) {
	return unix.Openat2(dirfd, name, &unix.OpenHow{
		Flags:   unix.O_RDONLY | unix.O_DIRECTORY | unix.O_NOFOLLOW | unix.O_CLOEXEC,
		Resolve: resolveBeneath,
	})
}

// removeBeneath removes path beneath its parent directory.
func removeBeneath(path string) error {
	parent, base := filepath.Split(filepath.Clean(path))
	pfd, err := openDir(parent)
	if err != nil {
		if errors.Is(err, unix.ENOENT) {
			return nil
		}
		return err
	}
	defer func() { _ = unix.Close(pfd) }()
	return removeAt(pfd, base)
}

// clearBeneath removes every entry of dir, leaving dir.
func clearBeneath(dir string) error {
	fd, err := openDir(filepath.Clean(dir))
	if err != nil {
		if errors.Is(err, unix.ENOENT) {
			return nil
		}
		return err
	}
	defer func() { _ = unix.Close(fd) }()
	return removeChildren(fd)
}

// removeAt removes name beneath dirfd: a directory with its contents, and
// any other entry, a symbolic link included, as itself.
func removeAt(dirfd int, name string) error {
	var st unix.Stat_t
	if err := unix.Fstatat(dirfd, name, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		if errors.Is(err, unix.ENOENT) {
			return nil
		}
		return fmt.Errorf("stat %s: %w", name, err)
	}
	if st.Mode&unix.S_IFMT != unix.S_IFDIR {
		if err := unix.Unlinkat(dirfd, name, 0); err != nil && !errors.Is(err, unix.ENOENT) {
			return fmt.Errorf("unlink %s: %w", name, err)
		}
		return nil
	}
	fd, err := openChildDir(dirfd, name)
	if err != nil {
		return fmt.Errorf("open %s: %w", name, err)
	}
	childErr := removeChildren(fd)
	_ = unix.Close(fd)
	if childErr != nil {
		return fmt.Errorf("%s: %w", name, childErr)
	}
	if err := unix.Unlinkat(dirfd, name, unix.AT_REMOVEDIR); err != nil && !errors.Is(err, unix.ENOENT) {
		return fmt.Errorf("rmdir %s: %w", name, err)
	}
	return nil
}

// removeChildren removes every entry of the directory open at fd. Every
// entry is attempted, and the errors are joined.
func removeChildren(fd int) error {
	names, err := readDirNames(fd)
	if err != nil {
		return err
	}
	var errs []error
	for _, n := range names {
		if err := removeAt(fd, n); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// readDirNames lists the directory open at fd without consuming fd.
func readDirNames(fd int) ([]string, error) {
	dup, err := unix.Openat2(fd, ".", &unix.OpenHow{
		Flags:   unix.O_RDONLY | unix.O_DIRECTORY | unix.O_CLOEXEC,
		Resolve: resolveBeneath,
	})
	if err != nil {
		return nil, fmt.Errorf("reopen directory: %w", err)
	}
	f := os.NewFile(uintptr(dup), "dir")
	defer func() { _ = f.Close() }()
	names, err := f.Readdirnames(-1)
	if err != nil {
		return nil, fmt.Errorf("read directory: %w", err)
	}
	return names, nil
}

// pathStateBeneath reports whether path exists and whether it is empty.
func pathStateBeneath(path string) (bool, bool, error) {
	parent, base := filepath.Split(filepath.Clean(path))
	pfd, err := openDir(parent)
	if err != nil {
		if errors.Is(err, unix.ENOENT) {
			return false, true, nil
		}
		return false, false, err
	}
	defer func() { _ = unix.Close(pfd) }()
	var st unix.Stat_t
	if err := unix.Fstatat(pfd, base, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		if errors.Is(err, unix.ENOENT) {
			return false, true, nil
		}
		return false, false, fmt.Errorf("stat %s: %w", path, err)
	}
	if st.Mode&unix.S_IFMT != unix.S_IFDIR {
		// A regular file or a symbolic link left behind is a dirty path.
		return true, false, nil
	}
	fd, err := openChildDir(pfd, base)
	if err != nil {
		return true, false, fmt.Errorf("open %s: %w", path, err)
	}
	defer func() { _ = unix.Close(fd) }()
	names, err := readDirNames(fd)
	if err != nil {
		return true, false, err
	}
	return true, len(names) == 0, nil
}

// shmRemove removes a System V shared-memory segment.
func shmRemove(id int) error {
	_, err := unix.SysvShmCtl(id, unix.IPC_RMID, nil)
	return err
}
