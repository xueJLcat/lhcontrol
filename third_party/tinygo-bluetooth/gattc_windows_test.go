//go:build windows

package bluetooth

import (
	"context"
	"errors"
	"testing"

	"github.com/saltosystems/winrt-go/windows/devices/bluetooth/genericattributeprofile"
)

func TestWriteWithoutResponseClassifiesDisconnectedDeviceAsNeverSubmitted(t *testing.T) {
	characteristic := DeviceCharacteristic{deviceCharacteristic: &deviceCharacteristic{
		characteristic: &genericattributeprofile.GattCharacteristic{},
		properties:     genericattributeprofile.GattCharacteristicPropertiesWriteWithoutResponse,
		service: DeviceService{deviceService: &deviceService{
			device: Device{},
		}},
	}}

	n, err := characteristic.WriteWithoutResponseContext(context.Background(), []byte{0x01})
	var neverSubmitted *WriteNeverSubmittedError
	if n != 0 || !errors.As(err, &neverSubmitted) {
		t.Fatalf(
			"WriteWithoutResponseContext() = %d, %v (type %T), want 0 and WriteNeverSubmittedError",
			n,
			err,
			err,
		)
	}
	if neverSubmitted.PossiblySent() {
		t.Fatal("pre-submission disconnect was marked possibly sent")
	}
}

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
	var neverSent *WriteNeverSubmittedError
	if !errors.As(err, &neverSent) {
		t.Fatalf("classifyWriteFailure() = %v (type %T), want WriteNeverSubmittedError", err, err)
	}
	if !errors.Is(err, cause) {
		t.Fatalf("classified error does not wrap cause: %v", err)
	}
	if neverSent.PossiblySent() {
		t.Fatal("WriteNeverSubmittedError.PossiblySent() = true, want false")
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

func TestClassifyWriteFailureProtocolRejectionCarriesDefiniteClassification(t *testing.T) {
	cause := gattCommunicationStatusError(
		"write rejected",
		int32(genericattributeprofile.GattCommunicationStatusProtocolError),
	)
	err := classifyWriteFailure(
		genericattributeprofile.GattWriteOptionWriteWithoutResponse,
		true,
		true,
		cause,
	)
	var classification interface{ PossiblySent() bool }
	if !errors.As(err, &classification) {
		t.Fatalf("classifyWriteFailure() = %v, want an explicit send classification", err)
	}
	if classification.PossiblySent() {
		t.Fatalf("classifyWriteFailure() = %v, protocol rejection was marked possibly sent", err)
	}
	if !errors.Is(err, cause) {
		t.Fatalf("classified error does not wrap cause: %v", err)
	}
}

func TestClassifyWriteFailureWithResponseIsPossiblySent(t *testing.T) {
	cause := errors.New("completion failed")
	err := classifyWriteFailure(genericattributeprofile.GattWriteOptionWriteWithResponse, true, false, cause)
	var possiblySent *WritePossiblySentError
	if !errors.Is(err, cause) || !errors.As(err, &possiblySent) {
		t.Fatalf("classifyWriteFailure() = %v, want possibly-sent response-write error", err)
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
