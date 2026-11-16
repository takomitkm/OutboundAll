//go:build windows

package lockfile

import (
	"os"
	"sync"
	"syscall"
	"unsafe"
)

var (
	kernel32       = syscall.NewLazyDLL("kernel32.dll")
	procLockFileEx = kernel32.NewProc("LockFileEx")
)

const (
	lockExclusiveFlag   = 0x2
	unlockFlag          = 0x1
	lockFailImmediately = 0x1
)

// lockExclusive takes an exclusive byte-range lock over the whole file through
// kernel32!LockFileEx. Pure stdlib (syscall + unsafe); golang.org/x/sys would be
// a dependency for a single call.
var lockMu sync.Mutex // LockFileEx handle use is not goroutine-safe per docs

func lockExclusive(f *os.File) error {
	lockMu.Lock()
	defer lockMu.Unlock()
	h := syscall.Handle(f.Fd())
	var ol syscall.Overlapped
	r1, _, callErr := procLockFileEx.Call(
		uintptr(h),
		uintptr(lockExclusiveFlag),
		0,
		uintptr(0xFFFFFFFF),
		uintptr(0xFFFFFFFF),
		uintptr(unsafe.Pointer(&ol)),
	)
	if r1 == 0 {
		if callErr != nil && callErr != syscall.Errno(0) {
			return callErr
		}
		return syscall.EACCES
	}
	return nil
}

// lockExclusiveNowait is the non-blocking variant: ERROR_IO_PENDING-style
// failure (r1==0) when the lock is held.
func lockExclusiveNowait(f *os.File) error {
	lockMu.Lock()
	defer lockMu.Unlock()
	h := syscall.Handle(f.Fd())
	var ol syscall.Overlapped
	r1, _, _ := procLockFileEx.Call(
		uintptr(h),
		uintptr(lockExclusiveFlag|lockFailImmediately),
		0,
		uintptr(0xFFFFFFFF),
		uintptr(0xFFFFFFFF),
		uintptr(unsafe.Pointer(&ol)),
	)
	if r1 == 0 {
		// LockFileEx with FAIL_IMMEDIATELY fails with ERROR_LOCK_VIOLATION on
		// contention; rarer failures (bad handle, disk) land here too, which
		// is the safe direction for a single-instance guard.
		return ErrLocked
	}
	return nil
}

func unlock(f *os.File) error {
	lockMu.Lock()
	defer lockMu.Unlock()
	h := syscall.Handle(f.Fd())
	var ol syscall.Overlapped
	r1, _, callErr := procLockFileEx.Call(
		uintptr(h),
		uintptr(unlockFlag),
		0,
		uintptr(0xFFFFFFFF),
		uintptr(0xFFFFFFFF),
		uintptr(unsafe.Pointer(&ol)),
	)
	if r1 == 0 {
		if callErr != nil && callErr != syscall.Errno(0) {
			return callErr
		}
		return syscall.EACCES
	}
	return nil
}
