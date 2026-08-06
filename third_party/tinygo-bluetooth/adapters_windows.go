package bluetooth

import (
	"context"
	"errors"
	"fmt"
	"syscall"
	"time"
	"unsafe"

	"github.com/go-ole/go-ole"
	"github.com/saltosystems/winrt-go"
	"github.com/saltosystems/winrt-go/windows/foundation"
	"github.com/saltosystems/winrt-go/windows/foundation/collections"
)

// AdapterInfo identifies a local Bluetooth radio as reported by Windows
// device enumeration. DeviceID is stable across reboots for as long as the
// radio stays attached.
type AdapterInfo struct {
	DeviceID string
	Name     string
}

// Hand-written WinRT projections: the pinned winrt-go release predates the
// classes needed to enumerate Bluetooth radios. GUIDs and vtable layouts are
// taken from the Windows winmd metadata.
const (
	signatureDeviceInformation = "rc(Windows.Devices.Enumeration.DeviceInformation;{aba0fb95-4398-489d-8e44-e6130927011f})"

	guidIBluetoothAdapterStatics  = "{8b02fb6a-ac4c-4741-8661-8eab7d17ea9f}"
	guidIDeviceInformationStatics = "{c17f100e-3a46-4a78-8013-769dc9b97390}"
	guidIDeviceInformation        = "{aba0fb95-4398-489d-8e44-e6130927011f}"
)

type iBluetoothAdapterStatics struct {
	ole.IInspectable
}

type iBluetoothAdapterStaticsVtbl struct {
	ole.IInspectableVtbl

	GetDeviceSelector uintptr
	FromIdAsync       uintptr
	GetDefaultAsync   uintptr
}

func (v *iBluetoothAdapterStatics) VTable() *iBluetoothAdapterStaticsVtbl {
	return (*iBluetoothAdapterStaticsVtbl)(unsafe.Pointer(v.RawVTable))
}

func bluetoothAdapterGetDeviceSelector() (string, error) {
	inspectable, err := ole.RoGetActivationFactory("Windows.Devices.Bluetooth.BluetoothAdapter", ole.NewGUID(guidIBluetoothAdapterStatics))
	if err != nil {
		return "", err
	}
	defer inspectable.Release()
	v := (*iBluetoothAdapterStatics)(unsafe.Pointer(inspectable))

	var outHStr ole.HString
	hr, _, _ := syscall.SyscallN(
		v.VTable().GetDeviceSelector,
		uintptr(unsafe.Pointer(v)),        // this
		uintptr(unsafe.Pointer(&outHStr)), // out string
	)
	if hr != 0 {
		return "", ole.NewError(hr)
	}

	out := outHStr.String()
	_ = ole.DeleteHString(outHStr)
	return out, nil
}

type iDeviceInformationStatics struct {
	ole.IInspectable
}

type iDeviceInformationStaticsVtbl struct {
	ole.IInspectableVtbl

	CreateFromIdAsync                             uintptr
	CreateFromIdAsyncAdditionalProperties         uintptr
	FindAllAsync                                  uintptr
	FindAllAsyncDeviceClass                       uintptr
	FindAllAsyncAqsFilter                         uintptr
	FindAllAsyncAqsFilterAndAdditionalProperties  uintptr
	CreateWatcher                                 uintptr
	CreateWatcherDeviceClass                      uintptr
	CreateWatcherAqsFilter                        uintptr
	CreateWatcherAqsFilterAndAdditionalProperties uintptr
}

func (v *iDeviceInformationStatics) VTable() *iDeviceInformationStaticsVtbl {
	return (*iDeviceInformationStaticsVtbl)(unsafe.Pointer(v.RawVTable))
}

func deviceInformationFindAllAsyncAqsFilter(aqsFilter string) (*foundation.IAsyncOperation, error) {
	inspectable, err := ole.RoGetActivationFactory("Windows.Devices.Enumeration.DeviceInformation", ole.NewGUID(guidIDeviceInformationStatics))
	if err != nil {
		return nil, err
	}
	defer inspectable.Release()
	v := (*iDeviceInformationStatics)(unsafe.Pointer(inspectable))

	aqsHStr, err := ole.NewHString(aqsFilter)
	if err != nil {
		return nil, err
	}
	defer ole.DeleteHString(aqsHStr)

	var out *foundation.IAsyncOperation
	hr, _, _ := syscall.SyscallN(
		v.VTable().FindAllAsyncAqsFilter,
		uintptr(unsafe.Pointer(v)),    // this
		uintptr(aqsHStr),              // in string
		uintptr(unsafe.Pointer(&out)), // out foundation.IAsyncOperation
	)
	if hr != 0 {
		return nil, ole.NewError(hr)
	}

	return out, nil
}

type iDeviceInformation struct {
	ole.IInspectable
}

type iDeviceInformationVtbl struct {
	ole.IInspectableVtbl

	GetId                  uintptr
	GetName                uintptr
	GetIsEnabled           uintptr
	GetIsDefault           uintptr
	GetEnclosureLocation   uintptr
	GetProperties          uintptr
	Update                 uintptr
	GetThumbnailAsync      uintptr
	GetGlyphThumbnailAsync uintptr
}

func (v *iDeviceInformation) VTable() *iDeviceInformationVtbl {
	return (*iDeviceInformationVtbl)(unsafe.Pointer(v.RawVTable))
}

func (v *iDeviceInformation) getId() (string, error) {
	var outHStr ole.HString
	hr, _, _ := syscall.SyscallN(
		v.VTable().GetId,
		uintptr(unsafe.Pointer(v)),        // this
		uintptr(unsafe.Pointer(&outHStr)), // out string
	)
	if hr != 0 {
		return "", ole.NewError(hr)
	}
	out := outHStr.String()
	_ = ole.DeleteHString(outHStr)
	return out, nil
}

func (v *iDeviceInformation) getName() (string, error) {
	var outHStr ole.HString
	hr, _, _ := syscall.SyscallN(
		v.VTable().GetName,
		uintptr(unsafe.Pointer(v)),        // this
		uintptr(unsafe.Pointer(&outHStr)), // out string
	)
	if hr != 0 {
		return "", ole.NewError(hr)
	}
	out := outHStr.String()
	_ = ole.DeleteHString(outHStr)
	return out, nil
}

// awaitAsyncOperationByPolling waits for a WinRT async operation by polling
// its status instead of subscribing a completion delegate: the delegate GUID
// depends on the result type signature, which is unavailable for collection
// classes such as DeviceInformationCollection. Adapter enumeration is a fast
// local query, so bounded polling keeps this dependency-free.
func awaitAsyncOperationByPolling(ctx context.Context, asyncOperation *foundation.IAsyncOperation) error {
	if asyncOperation == nil {
		return errors.New("async operation is nil")
	}
	asyncInfo, err := queryAsyncInfo(asyncOperation)
	if err != nil {
		return err
	}
	defer asyncInfo.Release()

	deadline := time.Now().Add(asyncOperationTimeout)
	ticker := time.NewTicker(asyncStatusPollInterval)
	defer ticker.Stop()
	for {
		status, err := asyncInfo.GetStatus()
		if err != nil {
			return err
		}
		switch status {
		case foundation.AsyncStatusCompleted:
			return nil
		case foundation.AsyncStatusCanceled, foundation.AsyncStatusError:
			return asyncCompletionError(asyncOperation, status)
		}
		if err := ctx.Err(); err != nil {
			_ = asyncInfo.Cancel()
			return err
		}
		if time.Now().After(deadline) {
			_ = asyncInfo.Cancel()
			return &AsyncOperationTimeoutError{Cause: context.DeadlineExceeded}
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			_ = asyncInfo.Cancel()
			return ctx.Err()
		}
	}
}

// ListAdapters enumerates the Bluetooth radios known to Windows. The result
// is empty on systems without a Bluetooth radio.
func ListAdapters() ([]AdapterInfo, error) {
	leave, err := enterWinRTThread()
	if err != nil {
		return nil, err
	}
	defer leave()

	selector, err := bluetoothAdapterGetDeviceSelector()
	if err != nil {
		return nil, fmt.Errorf("resolve Bluetooth adapter device selector: %w", err)
	}

	operation, err := deviceInformationFindAllAsyncAqsFilter(selector)
	if err != nil {
		return nil, fmt.Errorf("start Bluetooth adapter enumeration: %w", err)
	}
	defer operation.Release()

	if err := awaitAsyncOperationByPolling(context.Background(), operation); err != nil {
		return nil, fmt.Errorf("enumerate Bluetooth adapters: %w", err)
	}

	results, err := operation.GetResults()
	if err != nil {
		return nil, fmt.Errorf("read Bluetooth adapter enumeration results: %w", err)
	}
	if results == nil {
		return nil, errors.New("Bluetooth adapter enumeration returned nil results")
	}
	collection := (*ole.IInspectable)(results)
	defer collection.Release()

	// DeviceInformationCollection projects as IVectorView<DeviceInformation>.
	vectorIID := winrt.ParameterizedInstanceGUID(collections.GUIDIVectorView, signatureDeviceInformation)
	dispatch, err := collection.QueryInterface(ole.NewGUID(vectorIID))
	if err != nil {
		return nil, fmt.Errorf("query adapter enumeration vector: %w", err)
	}
	vector := (*collections.IVectorView)(unsafe.Pointer(dispatch))
	defer vector.Release()

	size, err := vector.GetSize()
	if err != nil {
		return nil, fmt.Errorf("read adapter enumeration size: %w", err)
	}

	adapters := make([]AdapterInfo, 0, size)
	for index := uint32(0); index < size; index++ {
		item, err := vector.GetAt(index)
		if err != nil {
			return nil, fmt.Errorf("read Bluetooth adapter %d: %w", index, err)
		}
		if item == nil {
			continue
		}
		info, err := decodeAdapterInfo(item)
		if err != nil {
			return nil, err
		}
		adapters = append(adapters, info)
	}
	return adapters, nil
}

func decodeAdapterInfo(item unsafe.Pointer) (AdapterInfo, error) {
	inspectable := (*ole.IInspectable)(item)
	defer inspectable.Release()
	dispatch, err := inspectable.QueryInterface(ole.NewGUID(guidIDeviceInformation))
	if err != nil {
		return AdapterInfo{}, fmt.Errorf("query Bluetooth adapter device information: %w", err)
	}
	deviceInformation := (*iDeviceInformation)(unsafe.Pointer(dispatch))
	defer deviceInformation.Release()

	deviceID, err := deviceInformation.getId()
	if err != nil {
		return AdapterInfo{}, fmt.Errorf("read Bluetooth adapter device id: %w", err)
	}
	name, err := deviceInformation.getName()
	if err != nil {
		return AdapterInfo{}, fmt.Errorf("read Bluetooth adapter name: %w", err)
	}
	return AdapterInfo{DeviceID: deviceID, Name: name}, nil
}
