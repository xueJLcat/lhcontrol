//go:build windows

package platform

import (
	"fmt"
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
