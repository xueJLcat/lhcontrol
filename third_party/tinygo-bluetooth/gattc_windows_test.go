//go:build windows

package bluetooth

import (
	"errors"
	"testing"

	"github.com/saltosystems/winrt-go/windows/devices/bluetooth/genericattributeprofile"
)

func TestClassifyWriteFailurePossiblySent(t *testing.T) {
	cause := errors.New("completion failed")
	err := classifyWriteFailure(genericattributeprofile.GattWriteOptionWriteWithoutResponse, true, false, cause)
	var possiblySent *WritePossiblySentError
	if !errors.As(err, &possiblySent) || !possiblySent.PossiblySent() || !possiblySent.MayHaveBeenSent() {
		t.Fatalf("classifyWriteFailure() = %v, want possibly-sent marker", err)
	}
	if !errors.Is(err, cause) {
		t.Fatalf("classified error does not wrap cause: %v", err)
	}
}

func TestClassifyWriteFailureCreationErrorIsDefinitelyUnsent(t *testing.T) {
	cause := errors.New("operation creation failed")
	err := classifyWriteFailure(genericattributeprofile.GattWriteOptionWriteWithoutResponse, false, false, cause)
	var possiblySent *WritePossiblySentError
	if !errors.Is(err, cause) || errors.As(err, &possiblySent) {
		t.Fatalf("classifyWriteFailure() = %v, want unclassified creation error", err)
	}
}

func TestClassifyWriteFailureProtocolRejectionIsNotPossiblySent(t *testing.T) {
	for _, test := range []struct {
		err               error
		explicitRejection bool
	}{
		{err: ErrAttWriteNotPermitted},
		{err: errors.Join(errWriteFailed, ErrAttInvalidLength)},
		{
			err:               gattCommunicationStatusError("write rejected", int32(genericattributeprofile.GattCommunicationStatusProtocolError)),
			explicitRejection: true,
		},
	} {
		classified := classifyWriteFailure(genericattributeprofile.GattWriteOptionWriteWithoutResponse, true, test.explicitRejection, test.err)
		var possiblySent *WritePossiblySentError
		if errors.As(classified, &possiblySent) {
			t.Fatalf("protocol rejection %v was marked possibly sent", test.err)
		}
		var protocolErr AttributeProtocolError
		if errors.As(test.err, &protocolErr) && !errors.As(classified, &protocolErr) {
			t.Fatalf("protocol rejection %v lost AttributeProtocolError", test.err)
		}
	}
}

func TestClassifyWriteFailureWithResponseIsNotPossiblySent(t *testing.T) {
	cause := errors.New("completion failed")
	err := classifyWriteFailure(genericattributeprofile.GattWriteOptionWriteWithResponse, true, false, cause)
	var possiblySent *WritePossiblySentError
	if !errors.Is(err, cause) || errors.As(err, &possiblySent) {
		t.Fatalf("classifyWriteFailure() = %v, want unclassified response-write error", err)
	}
}

func TestGattWriteResultABIConstants(t *testing.T) {
	if guidIGattWriteResult != "4991ddb1-cb2b-44f7-99fc-d29a2871dc9b" {
		t.Fatalf("IGattWriteResult IID = %q", guidIGattWriteResult)
	}
	want := "rc(Windows.Devices.Bluetooth.GenericAttributeProfile.GattWriteResult;{4991ddb1-cb2b-44f7-99fc-d29a2871dc9b})"
	if signatureGattWriteResult != want {
		t.Fatalf("GattWriteResult signature = %q, want %q", signatureGattWriteResult, want)
	}
}
