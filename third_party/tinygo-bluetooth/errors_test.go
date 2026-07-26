package bluetooth

import (
	"errors"
	"strings"
	"testing"
)

func TestGATTCommunicationStatusErrorClassification(t *testing.T) {
	tests := []struct {
		name   string
		status int32
		target error
	}{
		{name: "unreachable", status: 1, target: ErrGATTUnreachable},
		{name: "protocol", status: 2, target: ErrGATTProtocol},
		{name: "access denied", status: 3, target: ErrGATTAccessDenied},
		{name: "unknown", status: 99, target: ErrGATTCommunication},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := gattCommunicationStatusError("test operation", test.status)
			if !errors.Is(err, test.target) {
				t.Fatalf("errors.Is(%v, %v) = false", err, test.target)
			}
			var statusErr *GATTCommunicationError
			if !errors.As(err, &statusErr) {
				t.Fatalf("errors.As(%v, *GATTCommunicationError) = false", err)
			}
			if statusErr.Status != test.status {
				t.Fatalf("status = %d, want %d", statusErr.Status, test.status)
			}
			if !strings.Contains(err.Error(), "test operation") {
				t.Fatalf("error %q does not contain operation", err)
			}
		})
	}
}
