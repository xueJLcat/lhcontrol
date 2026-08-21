//go:build !windows

package platform

import (
	"errors"
	"log"
)

var ErrUnsupportedPlatform = errors.New("lhcontrol is supported only on Windows")

func AcquireSingleInstance(string) (func(), bool, error) {
	return nil, false, ErrUnsupportedPlatform
}

// IsProcessRunning is unavailable off Windows; callers treat the error as
// "feature disabled".
func IsProcessRunning(...string) (bool, error) {
	return false, ErrUnsupportedPlatform
}

// BringWindowToFront is a no-op on non-Windows platforms for now.
func BringWindowToFront(appTitle string) bool {
	log.Println("BringWindowToFront not implemented for this platform.")
	return false
}

// FocusExistingInstance is a no-op on non-Windows platforms for now.
func FocusExistingInstance(appTitle, mutexName string) (focused bool, reacquiredRelease func()) {
	log.Println("FocusExistingInstance not implemented for this platform.")
	return false, nil
}
