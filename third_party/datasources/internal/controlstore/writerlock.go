package controlstore

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

type writerLock struct {
	file *os.File
}

func acquireWriterLock(path string) (*writerLock, string, error) {
	canonicalPath, err := canonicalDBPath(path)
	if err != nil {
		return nil, "", err
	}
	lockPath := canonicalPath + ".lock"
	fd, err := unix.Open(lockPath, unix.O_RDWR|unix.O_CREAT|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, "", fmt.Errorf("controlstore: open writer lock %q: %w", lockPath, err)
	}
	file := os.NewFile(uintptr(fd), lockPath)
	if file == nil {
		unix.Close(fd)
		return nil, "", fmt.Errorf("controlstore: open writer lock %q: invalid file descriptor", lockPath)
	}
	if err := unix.Fchmod(fd, 0o600); err != nil {
		file.Close()
		return nil, "", fmt.Errorf("controlstore: secure writer lock %q: %w", lockPath, err)
	}
	if err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		file.Close()
		if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
			return nil, "", fmt.Errorf("%w: %q", ErrWriterActive, canonicalPath)
		}
		return nil, "", fmt.Errorf("controlstore: lock writer %q: %w", lockPath, err)
	}
	return &writerLock{file: file}, canonicalPath, nil
}

func canonicalDBPath(path string) (string, error) {
	absolutePath, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("controlstore: resolve path %q: %w", path, err)
	}
	resolvedPath, err := filepath.EvalSymlinks(absolutePath)
	if err == nil {
		return resolvedPath, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("controlstore: resolve path %q: %w", absolutePath, err)
	}
	info, lstatErr := os.Lstat(absolutePath)
	if lstatErr == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("controlstore: resolve database symlink %q: target does not exist: %w", absolutePath, err)
		}
		return "", fmt.Errorf("controlstore: resolve existing path %q: %w", absolutePath, err)
	}
	if !errors.Is(lstatErr, os.ErrNotExist) {
		return "", fmt.Errorf("controlstore: inspect path %q: %w", absolutePath, lstatErr)
	}
	resolvedParent, err := filepath.EvalSymlinks(filepath.Dir(absolutePath))
	if err != nil {
		return "", fmt.Errorf("controlstore: resolve parent for %q: %w", absolutePath, err)
	}
	return filepath.Join(resolvedParent, filepath.Base(absolutePath)), nil
}

func (lock *writerLock) release() error {
	if lock == nil || lock.file == nil {
		return nil
	}
	unlockErr := unix.Flock(int(lock.file.Fd()), unix.LOCK_UN)
	closeErr := lock.file.Close()
	lock.file = nil
	if err := errors.Join(unlockErr, closeErr); err != nil {
		return fmt.Errorf("release writer lock: %w", err)
	}
	return nil
}
