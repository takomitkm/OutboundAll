// Package lockfile provides exclusive file locks with the platform syscall only
// (unix flock, Windows LockFileEx) -- no third-party dependency.
package lockfile

import (
	"errors"
	"os"
	"syscall"
)

// Lock holds an exclusive advisory lock on a file for the life of the handle.
type Lock struct {
	f *os.File
}

// Acquire opens (creating if needed) path and takes an exclusive, blocking lock.
func Acquire(path string) (*Lock, error) {
	if err := os.MkdirAll(dirOf(path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := lockExclusive(f); err != nil {
		f.Close()
		return nil, err
	}
	return &Lock{f: f}, nil
}

// ErrLocked is returned by TryAcquire when another holder owns the file.
var ErrLocked = errors.New("lock held by another process")

// TryAcquire is the non-blocking Acquire: ErrLocked instead of waiting when
// the lock is already held (single-instance supervisors need this -- a
// blocking Acquire makes the second instance hang forever).
func TryAcquire(path string) (*Lock, error) {
	if err := os.MkdirAll(dirOf(path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := lockExclusiveNowait(f); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return nil, ErrLocked
		}
		return nil, err
	}
	return &Lock{f: f}, nil
}

// Release removes the lock and closes the file.
func (l *Lock) Release() error {
	if l == nil || l.f == nil {
		return nil
	}
	err := unlock(l.f)
	ferr := l.f.Close()
	l.f = nil
	if err != nil {
		return err
	}
	return ferr
}

func dirOf(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' || path[i] == '\\' {
			return path[:i]
		}
	}
	return "."
}
