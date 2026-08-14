//go:build windows

package bluetooth

import (
	"errors"
	"testing"

	tinybluetooth "tinygo.org/x/bluetooth"
)

// TestValueNotAllowedRejectionDoesNotRequireReconnect pins that a peer
// rejection of the requested value (ATT 0x13) is a protocol decision and
// never evidence of a broken link: the station layer relies on
// RequiresReconnect to decide whether a failed operation must disconnect a
// station and re-discover its cached GATT session. The wrapped form mirrors
// the Windows write path, which reports a terminal ATT rejection after the
// async operation existed. A genuine transport-level ATT failure must still
// require reconnect.
func TestValueNotAllowedRejectionDoesNotRequireReconnect(t *testing.T) {
	if RequiresReconnect(tinybluetooth.ErrAttValueNotAllowed) {
		t.Fatal("bare value-not-allowed rejection incorrectly required reconnect")
	}
	rejected := &tinybluetooth.WriteRejectedError{
		Err: errors.Join(errors.New("bluetooth: write failed"), tinybluetooth.ErrAttValueNotAllowed),
	}
	writeErr := transportError("write characteristic", rejected)
	if RequiresReconnect(writeErr) {
		t.Fatalf("wrapped value-not-allowed write rejection incorrectly required reconnect: %v", writeErr)
	}
	control := transportError(
		"write characteristic",
		&tinybluetooth.WriteRejectedError{
			Err: errors.Join(errors.New("bluetooth: write failed"), tinybluetooth.ErrAttUnlikelyError),
		},
	)
	if !RequiresReconnect(control) {
		t.Fatalf("genuine ATT failure no longer requires reconnect: %v", control)
	}
}
