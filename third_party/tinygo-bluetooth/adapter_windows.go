package bluetooth

import (
	"context"
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
	scan         *scanControl

	connectHandlerMutex sync.RWMutex
	connectHandler      func(device Device, connected bool)

	defaultAdvertisement *Advertisement
}

var (
	asyncOperationTimeout   = 15 * time.Second
	asyncCancellationGrace  = 2 * time.Second
	asyncStatusPollInterval = 100 * time.Millisecond
)

// AsyncOperationTimeoutError reports an operation that did not reach a
// terminal WinRT state before its deadline and cancellation grace elapsed.
type AsyncOperationTimeoutError struct {
	Cause error
}

func (e *AsyncOperationTimeoutError) Error() string {
	return "WinRT async operation timed out"
}

func (e *AsyncOperationTimeoutError) Unwrap() error {
	return e.Cause
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
	return awaitAsyncOperationContext(context.Background(), asyncOperation, genericParamSignature)
}

func boundedAsyncOperationContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithTimeout(ctx, asyncOperationTimeout)
}

func awaitAsyncOperationContext(ctx context.Context, asyncOperation *foundation.IAsyncOperation, genericParamSignature string) error {
	if asyncOperation == nil {
		return errors.New("async operation is nil")
	}
	operationCtx, cancel := boundedAsyncOperationContext(ctx)
	defer cancel()

	// We need to obtain the GUID of the AsyncOperationCompletedHandler, but its a generic delegate
	// so we also need the generic parameter type's signature:
	// AsyncOperationCompletedHandler<genericParamSignature>
	iid := winrt.ParameterizedInstanceGUID(foundation.GUIDAsyncOperationCompletedHandler, genericParamSignature)

	// Wait until the async operation completes.
	completed := make(chan foundation.AsyncStatus, 1)
	var completedOnce sync.Once
	handler := foundation.NewAsyncOperationCompletedHandler(ole.NewGUID(iid), func(instance *foundation.AsyncOperationCompletedHandler, asyncInfo *foundation.IAsyncOperation, asyncStatus foundation.AsyncStatus) {
		completedOnce.Do(func() {
			completed <- asyncStatus
		})
	})
	defer handler.Release()

	if err := asyncOperation.SetCompleted(handler); err != nil {
		return fmt.Errorf("set async completion handler: %w", err)
	}

	asyncInfo, queryErr := queryAsyncInfo(asyncOperation)
	if queryErr != nil {
		select {
		case status := <-completed:
			return asyncCompletionError(asyncOperation, status)
		case <-operationCtx.Done():
			_ = asyncOperation.SetCompleted(nil)
			return &AsyncOperationTimeoutError{Cause: operationCtx.Err()}
		}
	}
	defer asyncInfo.Release()

	status, err := waitForAsyncCompletion(operationCtx, completed, asyncInfo.Cancel, asyncInfo.GetStatus)
	if err != nil {
		// Detaching is best-effort. If WinRT rejects it, the operation retains
		// its COM reference until the caller releases the operation.
		_ = asyncOperation.SetCompleted(nil)
		return err
	}
	return asyncCompletionError(asyncOperation, status)
}

func waitForAsyncCompletion(ctx context.Context, completed <-chan foundation.AsyncStatus, cancel func() error, getStatus func() (foundation.AsyncStatus, error)) (foundation.AsyncStatus, error) {
	select {
	case status := <-completed:
		return status, nil
	case <-ctx.Done():
	}

	_ = cancel()
	grace := time.NewTimer(asyncCancellationGrace)
	defer grace.Stop()
	ticker := time.NewTicker(asyncStatusPollInterval)
	defer ticker.Stop()
	for {
		select {
		case status := <-completed:
			return status, nil
		case <-ticker.C:
			status, err := getStatus()
			if err == nil && status != foundation.AsyncStatusStarted {
				return status, nil
			}
		case <-grace.C:
			return foundation.AsyncStatusStarted, &AsyncOperationTimeoutError{Cause: ctx.Err()}
		}
	}
}

func asyncCompletionError(asyncOperation *foundation.IAsyncOperation, status foundation.AsyncStatus) error {
	if status != foundation.AsyncStatusCompleted {
		if err := getAsyncError(asyncOperation); err != nil {
			return fmt.Errorf("async operation failed with status %d: %w", status, err)
		}
		return fmt.Errorf("async operation failed with status %d", status)
	}
	return nil
}

func (a *Adapter) connectionHandler() func(Device, bool) {
	a.connectHandlerMutex.RLock()
	handler := a.connectHandler
	a.connectHandlerMutex.RUnlock()
	return handler
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
