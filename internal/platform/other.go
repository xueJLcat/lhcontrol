//go:build !windows

package platform

import "log"

// BringWindowToFront is a no-op on non-Windows platforms for now.
func BringWindowToFront(appTitle string) bool {
	log.Println("BringWindowToFront not implemented for this platform.")
	return false
}
