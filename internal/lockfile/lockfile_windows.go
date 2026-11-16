//go:build windows

package lockfile

import (
	"os"
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
//
// NO package-level mutex here: LockFileEx on distinct file handles is
// thread-safe (kernel32 synchronous-call semantics), and serializing every
// operation through one mutex turned any BLOCKING Acquire into a deadlock --
// the second acquirer held the mutex while blocked inside LockFileEx, so the
// first holder could not even reach UnlockFileEx (CI-proven: the concurrent
// test hung 10 minutes on windows-latest; the unix path never had the mutex
// and behaves correctly).
func lockExclusive(f *os.File) error {
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

// lockExclusiveNowait is the non-blocking variant: fails (r1==0) when the
// lock is held.
func lockExclusiveNowait(f *os.File) error {
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
