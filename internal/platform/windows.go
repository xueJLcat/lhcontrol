//go:build windows

package platform

import (
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
	procFindWindowW                = user32.NewProc("FindWindowW")
	procSetForegroundWindow        = user32.NewProc("SetForegroundWindow")
	procShowWindow                 = user32.NewProc("ShowWindow")
	procFlashWindowEx              = user32.NewProc("FlashWindowEx")
	procGetWindowThreadProcessId   = user32.NewProc("GetWindowThreadProcessId")
	kernel32                       = syscall.NewLazyDLL("kernel32.dll")
	procCreateMutexW               = kernel32.NewProc("CreateMutexW")
	procCloseHandle                = kernel32.NewProc("CloseHandle")
	procOpenProcess                = kernel32.NewProc("OpenProcess")
	procQueryFullProcessImageNameW = kernel32.NewProc("QueryFullProcessImageNameW")
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

// findWindow finds a window by title.
func findWindow(title string) (syscall.Handle, error) {
	titlePtr, err := syscall.UTF16PtrFromString(title)
	if err != nil {
		return 0, err
	}
	hwnd, _, _ := procFindWindowW.Call(0, uintptr(unsafe.Pointer(titlePtr)))
	return foundWindowHandle(hwnd), nil
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
	finder func(string) (syscall.Handle, error),
	sleeper func(time.Duration),
) (syscall.Handle, error) {
	attempts := int(timeout/interval) + 1
	for attempt := 0; attempt < attempts; attempt++ {
		hwnd, err := finder(appTitle)
		if err != nil || hwnd != 0 {
			return hwnd, err
		}
		if attempt+1 < attempts {
			sleeper(interval)
		}
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

// BringWindowToFront finds the existing window, tries to set foreground, and flashes it (Windows specific)
func BringWindowToFront(appTitle string) bool {
	hwnd, err := waitForWindow(appTitle, 5*time.Second, 250*time.Millisecond, findWindow, time.Sleep)
	if err != nil {
		log.Printf("Error finding window: %v", err)
		return false
	}
	if hwnd == 0 {
		log.Println("Existing instance owns the mutex, but its window did not appear within 5 seconds.")
		return false
	}

	if expected, nameErr := ownProcessImageBaseName(); nameErr == nil {
		if actual, ownerErr := windowOwnerProcessName(hwnd); ownerErr == nil &&
			!strings.EqualFold(actual, expected) {
			log.Printf("Window titled %q belongs to %q, not %q; leaving the foreign window alone", appTitle, actual, expected)
			return false
		}
	}

	if !activateWindow(hwnd, showWindow, setForegroundWindow, flashWindowEx) {
		log.Println("SetForegroundWindow failed (maybe window is not allowed to take focus?). Flashing instead.")
	} else {
		log.Println("SetForegroundWindow succeeded.")
	}
	return true
}
