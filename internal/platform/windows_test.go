//go:build windows

package platform

import (
	"fmt"
	"syscall"
	"testing"
	"time"
)

func TestAcquireSingleInstance(t *testing.T) {
	name := fmt.Sprintf(`Local\lhcontrol-test-%d`, time.Now().UnixNano())
	release, alreadyRunning, err := AcquireSingleInstance(name)
	if err != nil || alreadyRunning {
		t.Fatalf("first acquire = already %v, err %v", alreadyRunning, err)
	}
	defer release()

	releaseSecond, alreadyRunning, err := AcquireSingleInstance(name)
	if err != nil {
		t.Fatal(err)
	}
	defer releaseSecond()
	if !alreadyRunning {
		t.Fatal("second acquire should report an existing instance")
	}
}

func TestFoundWindowHandleUsesOnlyReturnedHandle(t *testing.T) {
	if got := foundWindowHandle(0); got != 0 {
		t.Fatalf("zero HWND = %v, want not found", got)
	}
	const hwnd = uintptr(0x1234)
	if got := foundWindowHandle(hwnd); uintptr(got) != hwnd {
		t.Fatalf("HWND = %#x, want %#x", uintptr(got), hwnd)
	}
}

func TestFirstOwnedWindowSkipsSameTitledForeignWindow(t *testing.T) {
	foreign := syscall.Handle(0x1000)
	owned := syscall.Handle(0x2000)
	sequence := []syscall.Handle{foreign, owned, 0}
	index := 0
	got, err := firstOwnedWindow(
		"lhcontrol.exe",
		func(after syscall.Handle) (syscall.Handle, error) {
			if index == 0 && after != 0 {
				t.Fatalf("first enumeration started after %v", after)
			}
			if index == 1 && after != foreign {
				t.Fatalf("second enumeration started after %v, want foreign window", after)
			}
			value := sequence[index]
			index++
			return value, nil
		},
		func(hwnd syscall.Handle) (string, error) {
			if hwnd == foreign {
				return "notepad.exe", nil
			}
			return "LHCONTROL.EXE", nil
		},
	)
	if err != nil || got != owned {
		t.Fatalf("firstOwnedWindow() = (%v, %v), want (%v, nil)", got, err, owned)
	}
}

// TestFirstOwnedWindowFallsBackToTitleMatchWhenOwnerDiffers covers a running
// instance whose executable base name differs from the second launch (wails
// dev build next to an installed copy, or a renamed portable exe). The
// single-instance mutex already proves the application is running, so the
// same-titled window must be focused instead of being reported as missing.
func TestFirstOwnedWindowFallsBackToTitleMatchWhenOwnerDiffers(t *testing.T) {
	renamed := syscall.Handle(0x3000)
	sequence := []syscall.Handle{renamed, 0}
	index := 0
	got, err := firstOwnedWindow(
		"lhcontrol.exe",
		func(syscall.Handle) (syscall.Handle, error) {
			value := sequence[index]
			index++
			return value, nil
		},
		func(syscall.Handle) (string, error) {
			return "lhcontrol-dev.exe", nil
		},
	)
	if err != nil || got != renamed {
		t.Fatalf("firstOwnedWindow() = (%v, %v), want (%v, nil)", got, err, renamed)
	}
}

func TestWaitForWindowRetriesUntilWindowAppears(t *testing.T) {
	var attempts int
	var sleeps int
	hwnd, err := waitForWindow("Lighthouse Control", time.Second, 250*time.Millisecond,
		func(string) (syscall.Handle, error) {
			attempts++
			if attempts == 3 {
				return syscall.Handle(0x1234), nil
			}
			return 0, nil
		},
		func(delay time.Duration) {
			if delay != 250*time.Millisecond {
				t.Fatalf("sleep delay = %v", delay)
			}
			sleeps++
		},
	)
	if err != nil || hwnd != syscall.Handle(0x1234) {
		t.Fatalf("waitForWindow = (%v, %v)", hwnd, err)
	}
	if attempts != 3 || sleeps != 2 {
		t.Fatalf("attempts = %d, sleeps = %d", attempts, sleeps)
	}
}

func TestWaitForWindowTimesOutAfterAllAttempts(t *testing.T) {
	var attempts int
	hwnd, err := waitForWindow("Lighthouse Control", time.Second, 250*time.Millisecond,
		func(string) (syscall.Handle, error) {
			attempts++
			return 0, nil
		},
		func(time.Duration) {},
	)
	if err != nil || hwnd != 0 {
		t.Fatalf("waitForWindow = (%v, %v)", hwnd, err)
	}
	if attempts != 5 {
		t.Fatalf("attempts = %d, want 5", attempts)
	}
}

func TestActivateWindowFlashesWhenForegroundFails(t *testing.T) {
	var restored bool
	var flashed bool
	foregrounded := activateWindow(syscall.Handle(0x1234),
		func(_ syscall.Handle, command int) bool {
			restored = command == SW_RESTORE
			return true
		},
		func(syscall.Handle) bool { return false },
		func(_ syscall.Handle, flags uint32, count uint32, timeout uint32) bool {
			flashed = flags == FLASHW_ALL|FLASHW_TIMERNOFG && count == 0 && timeout == 0
			return true
		},
	)
	if foregrounded || !restored || !flashed {
		t.Fatalf("foregrounded=%v restored=%v flashed=%v", foregrounded, restored, flashed)
	}
}
