//go:build windows

package platform

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

var (
	user32                         = syscall.NewLazyDLL("user32.dll")
	procFindWindowExW              = user32.NewProc("FindWindowExW")
	procSetForegroundWindow        = user32.NewProc("SetForegroundWindow")
	procShowWindow                 = user32.NewProc("ShowWindow")
	procFlashWindowEx              = user32.NewProc("FlashWindowEx")
	procGetWindowThreadProcessId   = user32.NewProc("GetWindowThreadProcessId")
	kernel32                       = syscall.NewLazyDLL("kernel32.dll")
	procCreateMutexW               = kernel32.NewProc("CreateMutexW")
	procCloseHandle                = kernel32.NewProc("CloseHandle")
	procOpenProcess                = kernel32.NewProc("OpenProcess")
	procQueryFullProcessImageNameW = kernel32.NewProc("QueryFullProcessImageNameW")
	procCreateToolhelp32Snapshot   = kernel32.NewProc("CreateToolhelp32Snapshot")
	procProcess32FirstW            = kernel32.NewProc("Process32FirstW")
	procProcess32NextW             = kernel32.NewProc("Process32NextW")
)

// AcquireSingleInstance owns a named Windows mutex until the returned release
// function is called. Unlike a TCP port lock it cannot collide with unrelated
// local services or depend on localized socket error strings.
func AcquireSingleInstance(name string) (release func(), alreadyRunning bool, err error) {
	namePtr, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return nil, false, err
	}
	handle, _, callErr := procCreateMutexW.Call(0, 0, uintptr(unsafe.Pointer(namePtr)))
	if handle == 0 {
		return nil, false, callErr
	}
	if errno, ok := callErr.(syscall.Errno); ok && errno == syscall.ERROR_ALREADY_EXISTS {
		procCloseHandle.Call(handle)
		return func() {}, true, nil
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			procCloseHandle.Call(handle)
		})
	}, false, nil
}

// Windows API constants (from winuser.h)
const (
	SW_RESTORE    = 9
	SW_SHOWNORMAL = 1
)

// FLASHW flags
const (
	FLASHW_STOP      = 0
	FLASHW_CAPTION   = 0x00000001
	FLASHW_TRAY      = 0x00000002
	FLASHW_ALL       = FLASHW_CAPTION | FLASHW_TRAY
	FLASHW_TIMER     = 0x00000004
	FLASHW_TIMERNOFG = 0x0000000C
)

// FLASHWINFO struct
type FLASHWINFO struct {
	CbSize    uint32
	Hwnd      syscall.Handle
	DwFlags   uint32
	UCout     uint32
	DwTimeout uint32
}

// findWindow finds a window with the requested title that belongs to this
// executable. FindWindowW returns only one arbitrary title match; iterating
// with FindWindowExW prevents a same-titled foreign window from hiding the
// actual existing instance.
func findWindow(title string) windowSearchResult {
	titlePtr, err := syscall.UTF16PtrFromString(title)
	if err != nil {
		return windowSearchResult{err: err}
	}
	expected, expectedErr := ownProcessImageBaseName()
	ownerOf := windowOwnerProcessName
	if expectedErr != nil {
		// Without the own executable name there is no baseline to verify a
		// candidate against. Reporting that baseline failure as every
		// candidate's owner-query error would mark all candidates
		// unverifiable and promote the first one to an immediate match: an
		// unrelated program that happens to use the same window title could
		// be activated. Degrade every candidate to the deferred
		// verified-foreign fallback instead: the search keeps polling until
		// the budget runs out and focuses the first candidate only as the
		// final best effort. Swallowing the per-window lookup error keeps it
		// from taking the same unverifiable-immediate-match path.
		expected = ""
		ownerOf = func(hwnd syscall.Handle) (string, error) {
			name, _ := windowOwnerProcessName(hwnd)
			return name, nil
		}
	}
	match, foreign, err := firstOwnedWindow(
		expected,
		func(after syscall.Handle) (syscall.Handle, error) {
			hwnd, _, _ := procFindWindowExW.Call(
				0,
				uintptr(after),
				0,
				uintptr(unsafe.Pointer(titlePtr)),
			)
			return foundWindowHandle(hwnd), nil
		},
		ownerOf,
	)
	// A verified-foreign window is only a last-resort candidate; keep
	// polling for the real instance's window until the budget runs out.
	return windowSearchResult{match: match, foreignFallback: foreign, err: err}
}

// windowSearchResult reports one window-search attempt: a match owned by this
// application, or a verified-foreign same-titled window that may only be
// focused as the final best effort, or a hard enumeration error.
type windowSearchResult struct {
	match           syscall.Handle
	foreignFallback syscall.Handle
	err             error
}

func firstOwnedWindow(
	expected string,
	next func(after syscall.Handle) (syscall.Handle, error),
	owner func(syscall.Handle) (string, error),
) (match syscall.Handle, foreignFallback syscall.Handle, enumErr error) {
	var after syscall.Handle
	var unverifiable syscall.Handle
	for {
		hwnd, err := next(after)
		if err != nil {
			if unverifiable != 0 {
				return unverifiable, 0, nil
			}
			if foreignFallback != 0 {
				return 0, foreignFallback, nil
			}
			return 0, 0, err
		}
		if hwnd == 0 {
			break
		}
		actual, ownerErr := owner(hwnd)
		// An empty expected name means there is no baseline to verify
		// against; no candidate may become an immediate match in that case.
		if ownerErr == nil && expected != "" && strings.EqualFold(actual, expected) {
			return hwnd, 0, nil
		}
		// A same-titled window must not shadow a later verified match. Keep
		// the first candidate of each kind: an unverifiable owner can be a
		// renamed instance whose process cannot be queried and stays an
		// immediate fallback, while a verified-foreign window is proof the
		// candidate belongs to another program and is only acceptable as the
		// final best-effort focus when the instance window never appears.
		if ownerErr != nil {
			if unverifiable == 0 {
				unverifiable = hwnd
			}
		} else if foreignFallback == 0 {
			foreignFallback = hwnd
		}
		after = hwnd
	}
	// No window was owned by a matching executable name. The already-held
	// single-instance mutex proves that this application is running, so its
	// window must be one of the same-titled candidates even when the running
	// instance has a different executable base name (a wails-dev build next
	// to an installed copy, or a renamed portable exe). Prefer the exact
	// owner match above; only fall back here instead of reporting the window
	// as missing, which would make the second launch exit without focusing
	// the instance it just detected.
	if unverifiable != 0 {
		return unverifiable, 0, nil
	}
	return 0, foreignFallback, nil
}

// FindWindowW documents "not found" with a zero HWND. Its last-error value is
// not defined for that outcome and may be rendered in the user's UI language.
func foundWindowHandle(hwnd uintptr) syscall.Handle {
	return syscall.Handle(hwnd)
}

// setForegroundWindow brings a window to the foreground.
func setForegroundWindow(hwnd syscall.Handle) bool {
	ret, _, _ := procSetForegroundWindow.Call(uintptr(hwnd))
	return ret != 0
}

// showWindow changes the visibility state of a window.
func showWindow(hwnd syscall.Handle, cmdshow int) bool {
	ret, _, _ := procShowWindow.Call(uintptr(hwnd), uintptr(cmdshow))
	return ret != 0
}

// flashWindowEx flashes the window using FlashWindowEx.
func flashWindowEx(hwnd syscall.Handle, flags uint32, count uint32, timeout uint32) bool {
	var fi FLASHWINFO
	fi.CbSize = uint32(unsafe.Sizeof(fi))
	fi.Hwnd = hwnd
	fi.DwFlags = flags
	fi.UCout = count
	fi.DwTimeout = timeout

	ret, _, _ := procFlashWindowEx.Call(uintptr(unsafe.Pointer(&fi)))
	return ret != 0
}

func waitForWindow(
	appTitle string,
	timeout time.Duration,
	interval time.Duration,
	finder func(string) windowSearchResult,
	sleeper func(time.Duration),
) (syscall.Handle, error) {
	attempts := int(timeout/interval) + 1
	for attempt := 0; attempt < attempts; attempt++ {
		result := finder(appTitle)
		if result.err != nil {
			return 0, result.err
		}
		if result.match != 0 {
			return result.match, nil
		}
		if attempt+1 == attempts {
			// Budget exhausted. A verified-foreign same-titled window is the
			// best remaining focus target; earlier attempts kept polling so a
			// slow-starting instance window could still appear first.
			return result.foreignFallback, nil
		}
		sleeper(interval)
	}
	return 0, nil
}

func activateWindow(
	hwnd syscall.Handle,
	show func(syscall.Handle, int) bool,
	foreground func(syscall.Handle) bool,
	flash func(syscall.Handle, uint32, uint32, uint32) bool,
) bool {
	show(hwnd, SW_RESTORE)
	if foreground(hwnd) {
		return true
	}
	flash(hwnd, FLASHW_ALL|FLASHW_TIMERNOFG, 0, 0)
	return false
}

const processQueryLimitedInformation = 0x1000

// Injectable in tests.
var windowOwnerProcessName = queryWindowOwnerProcessName
var ownProcessImageBaseName = defaultOwnProcessImageBaseName

// queryWindowOwnerProcessName returns the base name of the executable that
// owns hwnd, so a same-titled foreign window is never activated by mistake.
func queryWindowOwnerProcessName(hwnd syscall.Handle) (string, error) {
	var pid uint32
	ret, _, _ := procGetWindowThreadProcessId.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&pid)))
	if ret == 0 || pid == 0 {
		return "", syscall.EINVAL
	}
	//nolint:gosec // PROCESS_QUERY_LIMITED_INFORMATION on an existing PID.
	handle, _, callErr := procOpenProcess.Call(processQueryLimitedInformation, 0, uintptr(pid))
	if handle == 0 {
		return "", callErr
	}
	defer procCloseHandle.Call(handle)
	buf := make([]uint16, syscall.MAX_PATH)
	size := uint32(len(buf))
	ret, _, callErr = procQueryFullProcessImageNameW.Call(
		handle,
		0,
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(unsafe.Pointer(&size)),
	)
	if ret == 0 {
		return "", callErr
	}
	return filepath.Base(syscall.UTF16ToString(buf[:size])), nil
}

func defaultOwnProcessImageBaseName() (string, error) {
	executable, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.Base(executable), nil
}

// Toolhelp32 snapshot constants and structures (from tlhelp32.h).
const thSnapProcess = 0x00000002

type processEntry32W struct {
	Size            uint32
	Usage           uint32
	ProcessID       uint32
	DefaultHeapID   uintptr
	ModuleID        uint32
	Threads         uint32
	ParentProcessID uint32
	PriClassBase    int32
	Flags           uint32
	ExeFile         [syscall.MAX_PATH]uint16
}

// Injectable in tests.
var runningProcessNames = toolhelpProcessNames

// IsProcessRunning reports whether any running process image matches one of
// the given base names (case-insensitive, e.g. "steam.exe").
func IsProcessRunning(names ...string) (bool, error) {
	if len(names) == 0 {
		return false, nil
	}
	running, err := runningProcessNames()
	if err != nil {
		return false, err
	}
	for _, name := range names {
		if _, ok := running[strings.ToLower(name)]; ok {
			return true, nil
		}
	}
	return false, nil
}

// toolhelpProcessNames snapshots the system process list and returns the set
// of executable base names, lowercased. A process snapshot is cheaper than
// per-process handle queries and needs no elevated privileges.
func toolhelpProcessNames() (map[string]struct{}, error) {
	snapshot, _, callErr := procCreateToolhelp32Snapshot.Call(thSnapProcess, 0)
	if snapshot == uintptr(syscall.InvalidHandle) {
		return nil, fmt.Errorf("create process snapshot: %w", callErr)
	}
	defer procCloseHandle.Call(snapshot)

	names := make(map[string]struct{})
	var entry processEntry32W
	entry.Size = uint32(unsafe.Sizeof(entry))
	ret, _, callErr := procProcess32FirstW.Call(snapshot, uintptr(unsafe.Pointer(&entry)))
	if ret == 0 {
		return nil, fmt.Errorf("read first process entry: %w", callErr)
	}
	for {
		name := syscall.UTF16ToString(entry.ExeFile[:])
		if name != "" {
			names[strings.ToLower(name)] = struct{}{}
		}
		entry.Size = uint32(unsafe.Sizeof(entry))
		ret, _, callErr = procProcess32NextW.Call(snapshot, uintptr(unsafe.Pointer(&entry)))
		if ret == 0 {
			if errno, ok := callErr.(syscall.Errno); ok && errno == syscall.ERROR_NO_MORE_FILES {
				break
			}
			return nil, fmt.Errorf("read process entry: %w", callErr)
		}
	}
	return names, nil
}

// windowWaitBudget bounds how long a second launch waits for the running
// instance's window. The instance mutex is acquired before the Wails window
// exists, and a cold start (WebView2 runtime initialization, AV scanner
// interference, slow disks) can take far longer than the original 5 seconds;
// giving up early made the second launch exit without focusing or running
// anything.
const windowWaitBudget = 30 * time.Second

// BringWindowToFront finds the existing window, tries to set foreground, and flashes it (Windows specific)
func BringWindowToFront(appTitle string) bool {
	hwnd, err := waitForWindow(appTitle, windowWaitBudget, 250*time.Millisecond, findWindow, time.Sleep)
	if err != nil {
		log.Printf("Error finding window: %v", err)
		return false
	}
	if hwnd == 0 {
		log.Printf("Existing instance owns the mutex, but its window did not appear within %s.", windowWaitBudget)
		return false
	}

	// Ownership was already decided by findWindow: an exact executable-name
	// match wins, and a fallback to the first same-titled window only happens
	// when the single-instance mutex proves this application is running. A
	// second owner check here would reject exactly that mutex-backed fallback
	// (a running instance with a different executable base name).

	if !activateWindow(hwnd, showWindow, setForegroundWindow, flashWindowEx) {
		log.Println("SetForegroundWindow failed (maybe window is not allowed to take focus?). Flashing instead.")
	} else {
		log.Println("SetForegroundWindow succeeded.")
	}
	return true
}

// instanceRecheckInterval spaces the mutex re-checks a second launch runs
// while waiting for the first instance's window. The kernel releases the
// instance mutex together with the process, so a first instance that exits
// mid-wait (a crash or a forced kill) is noticed within one interval.
const instanceRecheckInterval = 2 * time.Second

// FocusExistingInstance waits for the running instance's window and focuses
// it, re-checking the instance mutex between attempts. When the first
// instance exits while waiting, its window can never appear and the kernel
// has released the mutex; the returned release then carries the re-acquired
// mutex so the caller continues as a fresh instance instead of waiting out
// the whole budget and exiting with nothing to focus. A nil release with
// focused=false means the budget ran out with the instance still running.
func FocusExistingInstance(appTitle, mutexName string) (focused bool, reacquiredRelease func()) {
	return focusExistingInstance(appTitle, mutexName, windowWaitBudget, findWindow, AcquireSingleInstance, time.Sleep)
}

func focusExistingInstance(
	appTitle string,
	mutexName string,
	budget time.Duration,
	finder func(string) windowSearchResult,
	acquire func(string) (func(), bool, error),
	sleeper func(time.Duration),
) (focused bool, reacquiredRelease func()) {
	start := time.Now()
	for {
		result := finder(appTitle)
		if result.err != nil {
			log.Printf("Error finding window: %v", result.err)
		} else if result.match != 0 {
			if !activateWindow(result.match, showWindow, setForegroundWindow, flashWindowEx) {
				log.Println("SetForegroundWindow failed (maybe window is not allowed to take focus?). Flashing instead.")
			} else {
				log.Println("SetForegroundWindow succeeded.")
			}
			return true, nil
		}
		if time.Since(start) >= budget {
			// Budget exhausted. The verified-foreign same-titled window is
			// the final best-effort focus target, matching the plain
			// single-wait behavior; only a still-running instance can reach
			// this point because an exited one is caught by the re-check
			// below before the next attempt.
			if result.err == nil && result.foreignFallback != 0 {
				if !activateWindow(result.foreignFallback, showWindow, setForegroundWindow, flashWindowEx) {
					log.Println("SetForegroundWindow failed (maybe window is not allowed to take focus?). Flashing instead.")
				}
				return true, nil
			}
			return false, nil
		}
		release, stillRunning, acquireErr := acquire(mutexName)
		if acquireErr == nil && !stillRunning {
			return false, release
		}
		if acquireErr == nil {
			release()
		}
		sleeper(instanceRecheckInterval)
	}
}
