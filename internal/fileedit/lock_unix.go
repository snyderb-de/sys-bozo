package fileedit

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

func acquireRepoLock(path string) (func() error, error) {
	fd, err := unix.Open(path, unix.O_CREAT|unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open repository transaction lock: %w", err)
	}
	closeOnError := func(cause error) (func() error, error) {
		return nil, errors.Join(cause, unix.Close(fd))
	}
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return closeOnError(fmt.Errorf("inspect repository transaction lock: %w", err))
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFREG || os.FileMode(stat.Mode).Perm() != 0o600 {
		return closeOnError(fmt.Errorf("repository transaction lock must be a mode-0600 regular file: %s", path))
	}
	if err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
			return closeOnError(ErrTransactionBusy)
		}
		return closeOnError(fmt.Errorf("lock repository transaction: %w", err))
	}
	return func() error {
		return errors.Join(unix.Flock(fd, unix.LOCK_UN), unix.Close(fd))
	}, nil
}
