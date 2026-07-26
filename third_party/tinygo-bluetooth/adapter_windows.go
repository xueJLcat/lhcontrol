package bluetooth

import (
	"errors"
	"fmt"
	"runtime"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/go-ole/go-ole"
	"github.com/saltosystems/winrt-go"
	"github.com/saltosystems/winrt-go/windows/devices/bluetooth/advertisement"
	"github.com/saltosystems/winrt-go/windows/foundation"
)

var _ BLEAdapter = (*Adapter)(nil)

var (
	combaseDLL         = syscall.NewLazyDLL("combase.dll")
	procRoInitialize   = combaseDLL.NewProc("RoInitialize")
	procRoUninitialize = combaseDLL.NewProc("RoUninitialize")
)

var enterWinRTThread = enterWinRTThreadReal

func enterWinRTThreadReal() (func(), error) {
	runtime.LockOSThread()
	hr, _, _ := procRoInitialize.Call(1) // RO_INIT_MULTITHREADED
	if hr != 0 && hr != 1 {              // S_OK and S_FALSE are both success.
		runtime.UnlockOSThread()
		return nil, fmt.Errorf("RoInitialize failed: HRESULT 0x%08X", uint32(hr))
	}
	return func() {
		procRoUninitialize.Call()
		runtime.UnlockOSThread()
	}, nil
}

type Adapter struct {
	watcher      *advertisement.BluetoothLEAdvertisementWatcher
	watcherMutex sync.RWMutex

	connectHandler func(device Device, connected bool)

	defaultAdvertisement *Advertisement
}

// DefaultAdapter is the default adapter on the system.
//
// Make sure to call Enable() before using it to initialize the adapter.
var DefaultAdapter = &Adapter{
	connectHandler: func(device Device, connected bool) {
		return
	},
}

// Enable configures the BLE stack. It must be called before any
// Bluetooth-related calls (unless otherwise indicated).
func (a *Adapter) Enable() error {
	leave, err := enterWinRTThread()
	if err != nil {
		return err
	}
	leave()
	return nil
}

func awaitAsyncOperation(asyncOperation *foundation.IAsyncOperation, genericParamSignature string) error {
	if asyncOperation == nil {
		return errors.New("async operation is nil")
	}
	var status foundation.AsyncStatus

	// We need to obtain the GUID of the AsyncOperationCompletedHandler, but its a generic delegate
	// so we also need the generic parameter type's signature:
	// AsyncOperationCompletedHandler<genericParamSignature>
	iid := winrt.ParameterizedInstanceGUID(foundation.GUIDAsyncOperationCompletedHandler, genericParamSignature)

	// Wait until the async operation completes.
	waitChan := make(chan struct{})
	var completedOnce sync.Once
	handler := foundation.NewAsyncOperationCompletedHandler(ole.NewGUID(iid), func(instance *foundation.AsyncOperationCompletedHandler, asyncInfo *foundation.IAsyncOperation, asyncStatus foundation.AsyncStatus) {
		completedOnce.Do(func() {
			status = asyncStatus
			close(waitChan)
		})
	})
	defer handler.Release()

	if err := asyncOperation.SetCompleted(handler); err != nil {
		return fmt.Errorf("set async completion handler: %w", err)
	}

	// A timeout is only a cancellation request threshold. WinRT cancellation is
	// cooperative, so the operation and its callback must stay alive until the
	// runtime reports a real terminal state.
	select {
	case <-waitChan:
	case <-time.After(15 * time.Second):
		asyncInfo, queryErr := queryAsyncInfo(asyncOperation)
		if queryErr != nil {
			// Without IAsyncInfo there is no safe way to prove cancellation.
			// Keep the handler alive and wait for its completion callback.
			<-waitChan
			break
		}
		_ = asyncInfo.Cancel()
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-waitChan:
				asyncInfo.Release()
				goto operationFinished
			case <-ticker.C:
				current, statusErr := asyncInfo.GetStatus()
				if statusErr != nil || current == foundation.AsyncStatusStarted {
					continue
				}
				completedOnce.Do(func() {
					status = current
					close(waitChan)
				})
				asyncInfo.Release()
				goto operationFinished
			}
		}
	}

operationFinished:
	if status != foundation.AsyncStatusCompleted {
		if err := getAsyncError(asyncOperation); err != nil {
			return fmt.Errorf("async operation failed with status %d: %w", status, err)
		}
		return fmt.Errorf("async operation failed with status %d", status)
	}
	return nil
}

func queryAsyncInfo(asyncOperation *foundation.IAsyncOperation) (*foundation.IAsyncInfo, error) {
	iid := ole.NewGUID(foundation.GUIDIAsyncInfo)
	var asyncInfo *foundation.IAsyncInfo
	hr, _, _ := syscall.SyscallN(
		asyncOperation.VTable().QueryInterface,
		uintptr(unsafe.Pointer(asyncOperation)),
		uintptr(unsafe.Pointer(iid)),
		uintptr(unsafe.Pointer(&asyncInfo)),
	)
	if hr != 0 {
		return nil, fmt.Errorf("QueryInterface(IAsyncInfo) failed: HRESULT 0x%08X", uint32(hr))
	}
	return asyncInfo, nil
}

// getAsyncError queries IAsyncInfo from an IAsyncOperation to retrieve
// the error code of a failed async operation. If the HRESULT corresponds
// to a Bluetooth ATT error (facility 0x65), it returns an AttributeProtocolError.
func getAsyncError(asyncOperation *foundation.IAsyncOperation) error {
	asyncInfo, err := queryAsyncInfo(asyncOperation)
	if err != nil {
		return err
	}
	defer asyncInfo.Release()

	result, err := asyncInfo.GetErrorCode()
	if err != nil {
		return err
	}
	if result.Value == 0 {
		return nil
	}

	return hresultToError(uint32(result.Value))
}

// hresultToError converts an HRESULT to an appropriate error type while
// retaining the original HRESULT for diagnostics.
func hresultToError(hr uint32) error {
	facility := (hr >> 16) & 0x1FFF
	code := hr & 0xFFFF

	if facility == 0x65 { // FACILITY_BLUETOOTH_ATT
		return fmt.Errorf(
			"Bluetooth ATT operation failed (HRESULT 0x%08X): %w",
			hr,
			AttributeProtocolError(uint8(code)),
		)
	}

	return fmt.Errorf("HRESULT 0x%08X", hr)
}

func (a *Adapter) Address() (MACAddress, error) {
	// TODO: get mac address
	return MACAddress{}, errors.New("not implemented")
}
