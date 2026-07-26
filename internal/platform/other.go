//go:build !windows

package platform

import "log"

func AcquireSingleInstance(string) (func(), bool, error) {
	return func() {}, false, nil
}

// BringWindowToFront is a no-op on non-Windows platforms for now.
func BringWindowToFront(appTitle string) bool {
	log.Println("BringWindowToFront not implemented for this platform.")
	return false
}
