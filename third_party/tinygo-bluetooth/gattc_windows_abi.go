package bluetooth

import (
	"errors"
	"syscall"
	"unsafe"

	"github.com/go-ole/go-ole"
	"github.com/saltosystems/winrt-go/windows/devices/bluetooth/genericattributeprofile"
	"github.com/saltosystems/winrt-go/windows/foundation"
	"github.com/saltosystems/winrt-go/windows/storage/streams"
)

const (
	guidIGattWriteResult     = "4991ddb1-cb2b-44f7-99fc-d29a2871dc9b"
	signatureGattWriteResult = "rc(Windows.Devices.Bluetooth.GenericAttributeProfile.GattWriteResult;{4991ddb1-cb2b-44f7-99fc-d29a2871dc9b})"
)

// These definitions fill the narrow gap in the installed winrt-go bindings:
// the generated vtables exist, but their write-result methods are not exposed.
type iGattCharacteristic3Vtbl struct {
	ole.IInspectableVtbl
	getDescriptorsAsync                                             uintptr
	getDescriptorsWithCacheModeAsync                                uintptr
	getDescriptorsForUUIDAsync                                      uintptr
	getDescriptorsForUUIDWithCacheModeAsync                         uintptr
	writeValueWithResultAsync                                       uintptr
	writeValueWithResultAndOptionAsync                              uintptr
	writeClientCharacteristicConfigurationDescriptorWithResultAsync uintptr
}

type gattWriteResult struct {
	ole.IUnknown
}

type iGattWriteResultVtbl struct {
	ole.IInspectableVtbl
	status        uintptr
	protocolError uintptr
}

func writeValueWithResultAndOptionAsync(characteristic *genericattributeprofile.GattCharacteristic, value *streams.IBuffer, option genericattributeprofile.GattWriteOption) (*foundation.IAsyncOperation, bool, error) {
	itf, err := characteristic.QueryInterface(ole.NewGUID(genericattributeprofile.GUIDiGattCharacteristic3))
	if err != nil {
		var oleErr *ole.OleError
		if errors.As(err, &oleErr) && uint32(oleErr.Code()) == uint32(ole.E_NOINTERFACE) {
			op, legacyErr := characteristic.WriteValueWithOptionAsync(value, option)
			if legacyErr == nil && op == nil {
				legacyErr = errors.New("bluetooth: legacy write returned nil async operation")
			}
			return op, false, legacyErr
		}
		return nil, false, err
	}
	defer itf.Release()
	vtable := (*iGattCharacteristic3Vtbl)(unsafe.Pointer(itf.RawVTable))
	if vtable.writeValueWithResultAndOptionAsync == 0 {
		// No async operation was created, so the write definitely was not
		// submitted; reporting it as created would misclassify the failure
		// as possibly sent.
		return nil, false, errors.New("bluetooth: IGattCharacteristic3 write method is unavailable")
	}
	var operation *foundation.IAsyncOperation
	hr, _, _ := syscall.SyscallN(
		vtable.writeValueWithResultAndOptionAsync,
		uintptr(unsafe.Pointer(itf)),
		uintptr(unsafe.Pointer(value)),
		uintptr(option),
		uintptr(unsafe.Pointer(&operation)),
	)
	if hr != 0 {
		return nil, true, ole.NewError(hr)
	}
	if operation == nil {
		return nil, true, errors.New("bluetooth: write returned nil async operation")
	}
	return operation, true, nil
}

func (r *gattWriteResult) status() (genericattributeprofile.GattCommunicationStatus, error) {
	itf, err := r.QueryInterface(ole.NewGUID(guidIGattWriteResult))
	if err != nil {
		return genericattributeprofile.GattCommunicationStatusSuccess, err
	}
	defer itf.Release()
	vtable := (*iGattWriteResultVtbl)(unsafe.Pointer(itf.RawVTable))
	var status genericattributeprofile.GattCommunicationStatus
	hr, _, _ := syscall.SyscallN(vtable.status, uintptr(unsafe.Pointer(itf)), uintptr(unsafe.Pointer(&status)))
	if hr != 0 {
		return genericattributeprofile.GattCommunicationStatusSuccess, ole.NewError(hr)
	}
	return status, nil
}

func (r *gattWriteResult) protocolError() error {
	return getGattProtocolError(&r.IUnknown, guidIGattWriteResult, 7)
}
