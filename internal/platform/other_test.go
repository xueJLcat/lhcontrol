//go:build !windows

package platform

import (
	"errors"
	"testing"
)

func TestAcquireSingleInstanceRejectsUnsupportedPlatform(t *testing.T) {
	release, alreadyRunning, err := AcquireSingleInstance("lhcontrol-test")
	if release != nil || alreadyRunning || !errors.Is(err, ErrUnsupportedPlatform) {
		t.Fatalf("AcquireSingleInstance() = (%v, %v, %v), want (nil, false, ErrUnsupportedPlatform)", release, alreadyRunning, err)
	}
}
