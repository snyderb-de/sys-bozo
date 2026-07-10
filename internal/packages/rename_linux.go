//go:build linux

package packages

import "golang.org/x/sys/unix"

func exchangePaths(oldpath, newpath string) error {
	return unix.Renameat2(unix.AT_FDCWD, oldpath, unix.AT_FDCWD, newpath, unix.RENAME_EXCHANGE)
}
