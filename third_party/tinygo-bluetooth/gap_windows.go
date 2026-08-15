package bluetooth

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"github.com/go-ole/go-ole"
	"github.com/saltosystems/winrt-go"
	"github.com/saltosystems/winrt-go/windows/devices/bluetooth"
	"github.com/saltosystems/winrt-go/windows/devices/bluetooth/advertisement"
	"github.com/saltosystems/winrt-go/windows/devices/bluetooth/genericattributeprofile"
	"github.com/saltosystems/winrt-go/windows/foundation"
	"github.com/saltosystems/winrt-go/windows/foundation/collections"
	"github.com/saltosystems/winrt-go/windows/storage/streams"
)

type callbackGate struct {
	mutex  sync.Mutex
	cond   *sync.Cond
	closed bool
	active int
}

var (
	scanStopTimeout      = 2 * time.Second
	scanStopPollInterval = 50 * time.Millisecond
)

type scanControl struct {
	watcher      *advertisement.BluetoothLEAdvertisementWatcher
	stopRequests chan error
	stopOnce     sync.Once
	// mutex guards the start/stop state below so a StopScan racing the
	// watcher setup window cannot call Stop before Start, and concurrent
	// StopScan callers share one Stop call instead of stacking redundant
	// ones that WinRT may reject.
	mutex       sync.Mutex
	started     bool  // watcher.Start() has been accepted
	pendingStop bool  // StopScan arrived before Start
	stopIssued  bool  // watcher.Stop() has been attempted after Start
	stopErr     error // result of the first watcher.Stop() attempt
	terminal    bool  // watcher confirmed Stopped/Aborted
}

// stopWatcher issues watcher.Stop() once watcher.Start() has been accepted.
// A stop requested before Start is recorded as pendingStop and executed by
// ScanWithStart right after Start succeeds: WinRT may accept a Stop on a
// not-yet-started watcher as a no-op, and Scan would then run to completion
// despite the stop request. The returned bool reports whether the stop was
// deferred (pendingStop) instead of issued; a deferred stop carries no result
// yet, so the caller must not deliver one.
func (control *scanControl) stopWatcher() (stopErr error, deferred bool) {
	control.mutex.Lock()
	defer control.mutex.Unlock()
	if control.terminal {
		// The watcher already reached Stopped/Aborted on its own; issuing
		// another Stop can be rejected by WinRT and turn a clean finish into
		// a spurious stop failure. Report the recorded result instead.
		return control.stopErr, false
	}
	if !control.started {
		control.pendingStop = true
		return nil, true
	}
	if control.stopIssued {
		return control.stopErr, false
	}
	if alreadyTerminal(control.watcher) {
		// The watcher stopped or aborted on its own (radio removed, disabled
		// by policy, or a natural drain) before this stop was issued. WinRT
		// can reject a redundant Stop and turn a clean end into a spurious
		// stop failure.
		control.terminal = true
		return nil, false
	}
	control.stopIssued = true
	control.stopErr = control.watcher.Stop()
	return control.stopErr, false
}

// forceStop re-issues watcher.Stop() even though an earlier attempt already
// ran. waitForScanStop calls it only on a retry tick after the initial stop
// request produced an error; WinRT may reject a redundant Stop on a watcher
// merely draining through Stopping, so this re-issues only when the prior
// attempt actually failed (stopErr non-nil) or was never issued at all
// (for example when StopScan could not enter its WinRT thread). Without this
// the dedupe in stopWatcher made the retry a no-op and a failed or missing
// Stop was never retried.
func (control *scanControl) forceStop() error {
	control.mutex.Lock()
	defer control.mutex.Unlock()
	if !control.started || control.terminal {
		return control.stopErr
	}
	if control.stopIssued && control.stopErr == nil {
		// A prior Stop was accepted; stacking another would risk a WinRT
		// rejection while the watcher drains through Stopping.
		return nil
	}
	if alreadyTerminal(control.watcher) {
		// The watcher finished on its own while the earlier Stop was failing
		// or missing; re-issuing would risk a spurious rejection.
		control.terminal = true
		return nil
	}
	control.stopIssued = true
	control.stopErr = control.watcher.Stop()
	return control.stopErr
}

// alreadyTerminal reports a watcher that has already reached a terminal state
// (Stopped or Aborted), so a further Stop call would be redundant. A status
// read failure is treated as "not terminal" so the caller still attempts the
// stop instead of silently skipping it.
func alreadyTerminal(watcher *advertisement.BluetoothLEAdvertisementWatcher) bool {
	if watcher == nil {
		return false
	}
	status, err := watcher.GetStatus()
	if err != nil {
		return false
	}
	return status == advertisement.BluetoothLEAdvertisementWatcherStatusStopped ||
		status == advertisement.BluetoothLEAdvertisementWatcherStatusAborted
}

// ensureStopped is the last-resort stop for cleanup paths that leave without
// a confirmed terminal state (stop timeout, a failure or panic after Start).
// Releasing the watcher alone does not end the WinRT scan activity, so the
// radio could keep scanning and reject the next scan with ResourceInUse.
func (control *scanControl) ensureStopped() {
	control.mutex.Lock()
	if !control.started || control.terminal {
		control.mutex.Unlock()
		return
	}
	watcher := control.watcher
	control.mutex.Unlock()
	_ = watcher.Stop()
}

func (control *scanControl) markTerminal() {
	control.mutex.Lock()
	control.terminal = true
	control.mutex.Unlock()
}

// ScanStopTimeoutError reports a watcher that did not reach a terminal state
// after StopScan was requested.
type ScanStopTimeoutError struct{}

func (*ScanStopTimeoutError) Error() string {
	return "Bluetooth scan did not stop before the cleanup deadline"
}

func newCallbackGate() *callbackGate {
	gate := &callbackGate{}
	gate.cond = sync.NewCond(&gate.mutex)
	return gate
}

func (gate *callbackGate) begin() bool {
	gate.mutex.Lock()
	gate.active++
	allowed := !gate.closed
	gate.mutex.Unlock()
	return allowed
}

func (gate *callbackGate) end() {
	gate.mutex.Lock()
	gate.active--
	if gate.closed && gate.active == 0 {
		gate.cond.Broadcast()
	}
	gate.mutex.Unlock()
}

func (gate *callbackGate) close() {
	gate.mutex.Lock()
	gate.closed = true
	gate.mutex.Unlock()
}

func (gate *callbackGate) wait() {
	gate.mutex.Lock()
	for gate.active > 0 {
		gate.cond.Wait()
	}
	gate.mutex.Unlock()
}

// Address contains a Bluetooth MAC address.
type Address struct {
	MACAddress
}

type Advertisement struct {
	advertisement *advertisement.BluetoothLEAdvertisement
	publisher     *advertisement.BluetoothLEAdvertisementPublisher
}

func scanStoppedError(code bluetooth.BluetoothError) error {
	switch code {
	case bluetooth.BluetoothErrorSuccess:
		return nil
	case bluetooth.BluetoothErrorRadioNotAvailable:
		return fmt.Errorf("%w (WinRT error code %d)", ErrRadioNotAvailable, code)
	case bluetooth.BluetoothErrorResourceInUse:
		return fmt.Errorf("%w (WinRT error code %d)", ErrResourceInUse, code)
	case bluetooth.BluetoothErrorDisabledByPolicy:
		return fmt.Errorf("%w (WinRT error code %d)", ErrDisabledByPolicy, code)
	default:
		return fmt.Errorf("Bluetooth scan stopped with WinRT error code %d", code)
	}
}

// DefaultAdvertisement returns the default advertisement instance but does not
// configure it.
func (a *Adapter) DefaultAdvertisement() *Advertisement {
	if a.defaultAdvertisement == nil {
		a.defaultAdvertisement = &Advertisement{}
	}

	return a.defaultAdvertisement
}

// Configure this advertisement.
// on Windows we're only able to set "Manufacturer Data" for advertisements.
// https://learn.microsoft.com/en-us/uwp/api/windows.devices.bluetooth.advertisement.bluetoothleadvertisementpublisher?view=winrt-22621#remarks
// following this c# source for this implementation: https://github.com/microsoft/Windows-universal-samples/blob/main/Samples/BluetoothAdvertisement/cs/Scenario2_Publisher.xaml.cs
// adding service data / localname leads to errors when starting the advertisement.
func (a *Advertisement) Configure(options AdvertisementOptions) error {
	// we can only advertise manufacturer / company data on windows, so no need to continue if we have none
	if len(options.ManufacturerData) == 0 {
		return nil
	}

	if a.publisher != nil {
		a.publisher.Release()
	}

	if a.advertisement != nil {
		a.advertisement.Release()
	}

	pub, err := advertisement.NewBluetoothLEAdvertisementPublisher()
	if err != nil {
		return err
	}

	a.publisher = pub

	ad, err := a.publisher.GetAdvertisement()
	if err != nil {
		return err
	}

	a.advertisement = ad

	vec, err := ad.GetManufacturerData()
	if err != nil {
		return err
	}
	// GetManufacturerData returns a caller-owned vector reference; the
	// manufacturer data objects and detached buffers created below are also
	// caller-owned. Releasing them (after the vector has AddRef'd what it
	// keeps) prevents COM reference leaks on every Configure call.
	defer vec.Release()

	for _, optManData := range options.ManufacturerData {
		writer, err := streams.NewDataWriter()
		if err != nil {
			return err
		}
		defer writer.Release()

		err = writer.WriteBytes(uint32(len(optManData.Data)), optManData.Data)
		if err != nil {
			return err
		}

		buf, err := writer.DetachBuffer()
		if err != nil {
			return err
		}
		defer buf.Release()

		manData, err := advertisement.BluetoothLEManufacturerDataCreate(optManData.CompanyID, buf)
		if err != nil {
			return err
		}
		defer manData.Release()

		if err = vec.Append(unsafe.Pointer(&manData.IUnknown.RawVTable)); err != nil {
			return err
		}
	}

	return nil
}

// Start advertisement. May only be called after it has been configured.
func (a *Advertisement) Start() error {
	// publisher will be present if we actually have manufacturer data to advertise.
	if a.publisher != nil {
		return a.publisher.Start()
	}

	return nil
}

// Stop advertisement. May only be called after it has been started.
func (a *Advertisement) Stop() error {
	if a.publisher != nil {
		return a.publisher.Stop()
	}

	return nil
}

// Scan starts a BLE scan. It is stopped by a call to StopScan. A common pattern
// is to cancel the scan when a particular device has been found.
func (a *Adapter) Scan(callback func(*Adapter, ScanResult)) (err error) {
	return a.ScanWithStart(callback, nil)
}

// ScanWithStart is the Windows scan implementation with an optional callback
// that runs after watcher.Start() has been accepted. The watcher may still
// abort afterwards; readiness is not guaranteed by the Start call alone.
// Applications that implement a fixed scan duration should start their timer
// from this callback, not before WinRT watcher setup.
func (a *Adapter) ScanWithStart(callback func(*Adapter, ScanResult), started func()) (err error) {
	leaveThread, err := enterWinRTThread()
	if err != nil {
		return err
	}
	defer leaveThread()

	a.watcherMutex.Lock()
	if a.watcher != nil {
		a.watcherMutex.Unlock()
		// Cannot scan more than once: which one should ScanStop()
		// stop?
		return errScanning
	}

	watcher, err := advertisement.NewBluetoothLEAdvertisementWatcher()
	if err != nil {
		a.watcherMutex.Unlock()
		return err
	}
	control := &scanControl{watcher: watcher, stopRequests: make(chan error, 1)}
	a.watcher = watcher
	a.scan = control
	a.watcherMutex.Unlock()
	defer func() {
		a.watcherMutex.Lock()
		if a.watcher == watcher {
			a.watcher = nil
			a.scan = nil
		}
		control.ensureStopped()
		_ = watcher.Release()
		a.watcherMutex.Unlock()
	}()

	// Set scanning mode to active so we receive scan responses
	// from devices in advertising mode
	err = watcher.SetScanningMode(advertisement.BluetoothLEScanningModeActive)
	if err != nil {
		return
	}

	// Listen for incoming BLE advertisement packets.
	// We need a TypedEventHandler<TSender, TResult> to listen to events, but since this is a parameterized delegate
	// its GUID depends on the classes used as sender and result, so we need to compute it:
	// TypedEventHandler<BluetoothLEAdvertisementWatcher, BluetoothLEAdvertisementReceivedEventArgs>
	eventReceivedGuid := winrt.ParameterizedInstanceGUID(
		foundation.GUIDTypedEventHandler,
		advertisement.SignatureBluetoothLEAdvertisementWatcher,
		advertisement.SignatureBluetoothLEAdvertisementReceivedEventArgs,
	)
	callbacks := newCallbackGate()
	handler := foundation.NewTypedEventHandler(ole.NewGUID(eventReceivedGuid), func(instance *foundation.TypedEventHandler, sender, arg unsafe.Pointer) {
		allowed := callbacks.begin()
		defer callbacks.end()
		if !allowed {
			return
		}
		defer func() {
			// WinRT occasionally supplies incomplete advertisement objects.
			// Never allow a malformed packet or COM wrapper panic to terminate
			// the entire host process.
			_ = recover()
		}()
		if arg == nil {
			return
		}
		args := (*advertisement.BluetoothLEAdvertisementReceivedEventArgs)(arg)
		result, resultErr := getScanResultFromArgs(args)
		if resultErr == nil {
			callback(a, result)
		}
	})

	receivedToken, err := watcher.AddReceived(handler)
	if err != nil {
		handler.Release()
		return
	}
	var stoppedHandler *foundation.TypedEventHandler
	var stoppedToken foundation.EventRegistrationToken
	stoppedAdded := false
	defer func() {
		// Prevent callback bodies from starting before unregistering both
		// events. Remove the registrations first, then wait for callbacks that
		// were already dispatched before releasing handlers or the watcher.
		callbacks.close()
		_ = watcher.RemoveReceived(receivedToken)
		if stoppedAdded {
			_ = watcher.RemoveStopped(stoppedToken)
		}
		callbacks.wait()
		if stoppedHandler != nil {
			stoppedHandler.Release()
		}
		handler.Release()
	}()

	// Wait for when advertisement has stopped by a call to StopScan().
	// Advertisement doesn't seem to stop right away, there is an
	// intermediate Stopping state.
	stoppingChan := make(chan error, 1)
	var stoppingOnce sync.Once
	// TypedEventHandler<BluetoothLEAdvertisementWatcher, BluetoothLEAdvertisementWatcherStoppedEventArgs>
	eventStoppedGuid := winrt.ParameterizedInstanceGUID(
		foundation.GUIDTypedEventHandler,
		advertisement.SignatureBluetoothLEAdvertisementWatcher,
		advertisement.SignatureBluetoothLEAdvertisementWatcherStoppedEventArgs,
	)
	stoppedHandler = foundation.NewTypedEventHandler(ole.NewGUID(eventStoppedGuid), func(_ *foundation.TypedEventHandler, _, arg unsafe.Pointer) {
		allowed := callbacks.begin()
		defer callbacks.end()
		if !allowed {
			return
		}
		defer func() {
			if recovered := recover(); recovered != nil {
				stoppingOnce.Do(func() {
					stoppingChan <- fmt.Errorf("Bluetooth watcher stopped callback panicked: %v", recovered)
					close(stoppingChan)
				})
			}
		}()
		if arg == nil {
			stoppingOnce.Do(func() {
				stoppingChan <- errors.New("Bluetooth watcher stopped without event arguments")
				close(stoppingChan)
			})
			return
		}
		args := (*advertisement.BluetoothLEAdvertisementWatcherStoppedEventArgs)(arg)
		errCode, err := args.GetError()
		stoppingOnce.Do(func() {
			if err != nil {
				// Got an error while getting the error value, that shouldn't
				// happen.
				stoppingChan <- fmt.Errorf("failed to get stopping error value: %w", err)
			} else if stopErr := scanStoppedError(errCode); stopErr != nil {
				stoppingChan <- stopErr
			}
			close(stoppingChan)
		})
	})

	stoppedToken, err = watcher.AddStopped(stoppedHandler)
	if err != nil {
		return
	}
	stoppedAdded = true

	err = watcher.Start()
	if err != nil {
		return err
	}
	control.mutex.Lock()
	control.started = true
	pendingStop := control.pendingStop
	control.mutex.Unlock()
	if pendingStop {
		// StopScan landed in the setup window before Start. Its request was
		// recorded instead of being sent to a not-yet-started watcher; issue
		// the real stop now that Start has been accepted. Deliver that real
		// result here (the deferred StopScan did not deliver one) so
		// waitForScanStop sees the actual stop outcome instead of a bare
		// timeout when this deferred Stop fails.
		stopErr, _ := control.stopWatcher()
		control.stopOnce.Do(func() { control.stopRequests <- stopErr })
	}
	if started != nil {
		started()
	}

	// Wait until advertisement has stopped, and finish. Once StopScan is
	// requested, status polling and retries bound cleanup even if WinRT omits
	// the Stopped event.
	err = waitForScanStop(stoppingChan, control.stopRequests, control.forceStop, watcher.GetStatus)
	var stopTimeout *ScanStopTimeoutError
	if !errors.As(err, &stopTimeout) {
		control.markTerminal()
	}
	return err
}

func waitForScanStop(stopped <-chan error, stopRequests <-chan error, stop func() error, getStatus func() (advertisement.BluetoothLEAdvertisementWatcherStatus, error)) error {
	var originalErr error
	select {
	case err := <-stopped:
		return err
	case originalErr = <-stopRequests:
	}

	deadline := time.NewTimer(scanStopTimeout)
	defer deadline.Stop()
	ticker := time.NewTicker(scanStopPollInterval)
	defer ticker.Stop()
	for {
		status, statusErr := getStatus()
		if statusErr == nil && (status == advertisement.BluetoothLEAdvertisementWatcherStatusStopped || status == advertisement.BluetoothLEAdvertisementWatcherStatusAborted) {
			// The Stopped event is dispatched on a separate thread and can lag
			// slightly behind the status transition. Give it one polling
			// interval so an error it carries (for example a radio removed or
			// disabled by policy while draining) is not dropped by the faster
			// status poll. A clean stop closes the channel without a value and
			// returns immediately, so this adds no delay in the common case.
			eventWait := time.NewTimer(scanStopPollInterval)
			var eventErr error
			var eventOK bool
			select {
			case eventErr, eventOK = <-stopped:
				eventWait.Stop()
			case <-eventWait.C:
			}
			if eventOK && eventErr != nil {
				if originalErr != nil {
					return errors.Join(originalErr, eventErr)
				}
				return eventErr
			}
			if originalErr != nil {
				return originalErr
			}
			if status == advertisement.BluetoothLEAdvertisementWatcherStatusAborted {
				return errors.New("Bluetooth scan watcher aborted without a Stopped event")
			}
			return nil
		}

		select {
		case eventErr := <-stopped:
			if eventErr != nil {
				if originalErr != nil {
					return errors.Join(originalErr, eventErr)
				}
				return eventErr
			}
			return originalErr
		case <-ticker.C:
			// Retry the stop only when the initial attempt failed. A
			// watcher that was stopped successfully can stay in the
			// intermediate Stopping state for a while, and WinRT may reject
			// a redundant Stop call; recording that rejection would poison
			// an otherwise clean scan.
			if originalErr != nil {
				_ = stop()
			}
		case <-deadline.C:
			timeoutErr := error(&ScanStopTimeoutError{})
			if originalErr != nil {
				return errors.Join(originalErr, timeoutErr)
			}
			return timeoutErr
		}
	}
}

func getScanResultFromArgs(args *advertisement.BluetoothLEAdvertisementReceivedEventArgs) (ScanResult, error) {
	if args == nil {
		return ScanResult{}, errors.New("advertisement arguments are nil")
	}
	// parse bluetooth address
	addr, err := args.GetBluetoothAddress()
	if err != nil {
		return ScanResult{}, fmt.Errorf("get Bluetooth address: %w", err)
	}
	adr := Address{}
	for i := range adr.MAC {
		adr.MAC[i] = byte(addr)
		addr >>= 8
	}
	sigStrength, _ := args.GetRawSignalStrengthInDBm()
	result := ScanResult{
		RSSI:    sigStrength,
		Address: adr,
	}

	winAdv, err := args.GetAdvertisement()
	if err != nil || winAdv == nil {
		if err == nil {
			err = errors.New("advertisement object is nil")
		}
		return result, fmt.Errorf("get advertisement: %w", err)
	}
	defer winAdv.Release()

	var manufacturerData []ManufacturerDataElement
	var serviceUUIDs []UUID
	// Extract manufacturer data. Every WinRT call is checked because partially
	// populated advertisements are normal while active scanning is running.
	if vector, vectorErr := winAdv.GetManufacturerData(); vectorErr == nil && vector != nil {
		defer vector.Release()
		size, sizeErr := vector.GetSize()
		if sizeErr != nil {
			size = 0
		}
		for i := uint32(0); i < size; i++ {
			element, elementErr := vector.GetAt(i)
			if elementErr != nil || element == nil {
				continue
			}
			manData := (*advertisement.BluetoothLEManufacturerData)(element)

			companyID, companyErr := manData.GetCompanyId()
			buffer, bufferErr := manData.GetData()
			var data []byte
			if companyErr == nil && bufferErr == nil && buffer != nil {
				data = bufferToSliceSafe(buffer)
			} else if buffer != nil {
				// A partial read failure must not leak the IBuffer;
				// bufferToSliceSafe (which releases it) was skipped.
				buffer.Release()
			}
			manData.Release()
			if companyErr != nil || bufferErr != nil || buffer == nil {
				continue
			}
			manufacturerData = append(manufacturerData, ManufacturerDataElement{
				CompanyID: companyID,
				Data:      data,
			})
		}
	}

	// Extract service UUIDs.
	if vector, vectorErr := winAdv.GetServiceUuids(); vectorErr == nil && vector != nil {
		defer vector.Release()
		size, sizeErr := vector.GetSize()
		if sizeErr != nil {
			size = 0
		}
		for i := uint32(0); i < size; i++ {
			guid, guidErr := vectorGUIDAt(vector, i)
			if guidErr != nil {
				continue
			}
			uuid := GUIDToUUID(guid)
			serviceUUIDs = append(serviceUUIDs, uuid)
		}
	}

	// Note: the IsRandom bit is never set.
	localName, _ := winAdv.GetLocalName()
	result.AdvertisementPayload = &advertisementFields{
		AdvertisementFields{
			LocalName:        localName,
			ServiceUUIDs:     serviceUUIDs,
			ManufacturerData: manufacturerData,
		},
	}

	return result, nil
}

func vectorGUIDAt(vector *collections.IVector, index uint32) (syscall.GUID, error) {
	var guid syscall.GUID
	hr, _, _ := syscall.SyscallN(
		vector.VTable().GetAt,
		uintptr(unsafe.Pointer(vector)),
		uintptr(index),
		uintptr(unsafe.Pointer(&guid)),
	)
	if hr != 0 {
		return syscall.GUID{}, ole.NewError(hr)
	}
	return guid, nil
}

func bufferToSliceSafe(buffer *streams.IBuffer) []byte {
	if buffer == nil {
		return nil
	}
	defer buffer.Release()
	dataReader, err := streams.DataReaderFromBuffer(buffer)
	if err != nil || dataReader == nil {
		return nil
	}
	defer dataReader.Release()
	bufferSize, err := buffer.GetLength()
	if err != nil || bufferSize == 0 {
		return nil
	}
	data, err := dataReader.ReadBytes(bufferSize)
	if err != nil {
		return nil
	}
	return data
}

// bufferToSlice is retained for the GATT server implementation. Keep the
// actual conversion in the nil/error-safe helper used by scan parsing.
func bufferToSlice(buffer *streams.IBuffer) []byte {
	return bufferToSliceSafe(buffer)
}

func GUIDToUUID(guid syscall.GUID) UUID {
	return NewUUID([16]byte{
		byte(guid.Data1 >> 24),
		byte(guid.Data1 >> 16),
		byte(guid.Data1 >> 8),
		byte(guid.Data1),
		byte(guid.Data2 >> 8),
		byte(guid.Data2),
		byte(guid.Data3 >> 8),
		byte(guid.Data3),
		guid.Data4[0], guid.Data4[1],
		guid.Data4[2], guid.Data4[3],
		guid.Data4[4], guid.Data4[5],
		guid.Data4[6], guid.Data4[7],
	})
}

// StopScan stops any in-progress scan. It can be called from within a Scan
// callback to stop the current scan. If no scan is in progress, an error will
// be returned.
func (a *Adapter) StopScan() error {
	leaveThread, err := enterWinRTThread()
	if err != nil {
		a.watcherMutex.RLock()
		control := a.scan
		if control != nil {
			control.stopOnce.Do(func() { control.stopRequests <- err })
		}
		a.watcherMutex.RUnlock()
		return err
	}
	defer leaveThread()

	a.watcherMutex.RLock()
	defer a.watcherMutex.RUnlock()
	control := a.scan
	if control == nil || control.watcher == nil {
		return errNotScanning
	}
	// stopWatcher dedupes concurrent callers so at most one watcher.Stop()
	// COM call is issued per scan; a stop before Start is recorded and
	// executed right after Start instead of being lost. A deferred stop has no
	// result yet, so only a stop that was actually issued delivers one;
	// ScanWithStart delivers the deferred stop's real result after Start.
	err, deferred := control.stopWatcher()
	if !deferred {
		control.stopOnce.Do(func() { control.stopRequests <- err })
	}
	return err
}

var _ GAPDevice = Device{}

// Device is a connection to a remote peripheral.
type Device struct {
	Address Address // the MAC address of the device
	state   *deviceState
	ctx     context.Context
}

// deviceState owns all WinRT objects for one connection. Device is copied by
// value throughout the public API, so mutable ownership must live behind a
// shared pointer.
type deviceState struct {
	operationMutex  sync.Mutex
	cleanupMutex    sync.Mutex
	closed          atomic.Bool
	cleanupStarted  bool
	cleanupComplete bool
	cleanupAttempt  *deviceCleanupAttempt
	cleanupRetries  int
	// cleanupRetryPending marks a scheduled automatic retry so interleaved
	// failures cannot stack redundant timers that consume the retry budget.
	cleanupRetryPending bool
	leaveThread         func()
	cancel              context.CancelFunc

	device                        *bluetooth.BluetoothLEDevice
	session                       *genericattributeprofile.GattSession
	connectionStatusListenerToken foundation.EventRegistrationToken
	connectionStatusListener      *foundation.TypedEventHandler
	connectionStatusListenerAdded bool
	services                      []*genericattributeprofile.GattDeviceService
	characteristics               []*genericattributeprofile.GattCharacteristic
	notifications                 []notificationRegistration
	callbacks                     *callbackGate
}

type deviceCleanupAttempt struct {
	done chan struct{}
	err  error
}

type notificationRegistration struct {
	characteristic *genericattributeprofile.GattCharacteristic
	token          foundation.EventRegistrationToken
	handler        *foundation.TypedEventHandler

	// Optional cleanup hooks keep ownership transitions independently
	// testable without invoking COM methods on fabricated WinRT objects.
	removeValueChanged func() error
	releaseHandler     func()
}

func (r notificationRegistration) unregister() error {
	if r.removeValueChanged != nil {
		return r.removeValueChanged()
	}
	if r.characteristic == nil {
		return nil
	}
	return r.characteristic.RemoveValueChanged(r.token)
}

func (r notificationRegistration) release() {
	if r.releaseHandler != nil {
		r.releaseHandler()
		return
	}
	if r.handler != nil {
		r.handler.Release()
	}
}

func (s *deviceState) beginCallback() bool {
	return s.callbacks.begin()
}

func (s *deviceState) endCallback() {
	s.callbacks.end()
}

func (s *deviceState) blockCallbacks() {
	s.callbacks.close()
}

func (s *deviceState) waitCallbacks() {
	s.callbacks.wait()
}

// drainCallbacksForCleanup unregisters event sources while device operations
// are excluded, then releases the operation lock before waiting for callbacks.
// A callback that passed the callback gate immediately before shutdown may
// still be waiting for operationMutex. Releasing the lock lets that callback
// observe closed and leave, avoiding a cleanup/callback lock-order deadlock.
//
// The method returns with operationMutex held. This provides a final barrier
// before the caller releases the WinRT objects used by device operations.
func (s *deviceState) drainCallbacksForCleanup(unregister func()) {
	s.operationMutex.Lock()
	unregister()
	s.operationMutex.Unlock()

	s.waitCallbacks()
	s.operationMutex.Lock()
}

func (d Device) beginOperation() (*deviceState, error) {
	if d.state == nil || d.state.closed.Load() {
		return nil, errors.New("bluetooth: device is disconnected")
	}
	d.state.operationMutex.Lock()
	if d.state.closed.Load() {
		d.state.operationMutex.Unlock()
		return nil, errors.New("bluetooth: device is disconnected")
	}
	leaveThread, err := enterWinRTThread()
	if err != nil {
		d.state.operationMutex.Unlock()
		return nil, err
	}
	d.state.leaveThread = leaveThread
	return d.state, nil
}

func (d Device) endOperation() {
	if d.state != nil {
		defer d.state.operationMutex.Unlock()
		if d.state.leaveThread != nil {
			leaveThread := d.state.leaveThread
			d.state.leaveThread = nil
			leaveThread()
		}
	}
}

func (d Device) registerService(service *genericattributeprofile.GattDeviceService) error {
	if d.state == nil || d.state.closed.Load() {
		return errors.New("bluetooth: device is disconnected")
	}
	d.state.services = append(d.state.services, service)
	return nil
}

func (d Device) registerCharacteristic(characteristic *genericattributeprofile.GattCharacteristic) error {
	if d.state == nil || d.state.closed.Load() {
		return errors.New("bluetooth: device is disconnected")
	}
	d.state.characteristics = append(d.state.characteristics, characteristic)
	return nil
}

func (d Device) registerNotification(registration notificationRegistration) error {
	if d.state == nil || d.state.closed.Load() {
		return errors.New("bluetooth: device is disconnected")
	}
	// Re-enabling notifications for a characteristic must replace the previous
	// registration. Keeping both would invoke one callback per prior
	// registration for every value change.
	for index, existing := range d.state.notifications {
		if existing.characteristic == registration.characteristic {
			if err := existing.unregister(); err != nil {
				return fmt.Errorf("bluetooth: remove existing notification before replacement: %w", err)
			}
			existing.release()
			d.state.notifications[index] = registration
			return nil
		}
	}
	d.state.notifications = append(d.state.notifications, registration)
	return nil
}

// rollbackNotificationRegistration releases a newly-added event handler when
// notification setup fails. If WinRT refuses to unregister it, ownership must
// remain attached to the device so Disconnect can retry the removal before it
// releases the handler and characteristic.
func (d Device) rollbackNotificationRegistration(registration notificationRegistration) error {
	if err := registration.unregister(); err != nil {
		if d.state == nil {
			return fmt.Errorf("bluetooth: roll back notification registration (device state unavailable): %w", err)
		}
		d.state.notifications = append(d.state.notifications, registration)
		return fmt.Errorf("bluetooth: roll back notification registration (retained for device cleanup): %w", err)
	}
	registration.release()
	return nil
}

// Connect starts a connection attempt to the given peripheral device address.
//
// On Linux and Windows, the IsRandom part of the address is ignored.
func (a *Adapter) Connect(address Address, params ConnectionParams) (result Device, returnErr error) {
	return a.ConnectContext(context.Background(), address, params)
}

// ConnectContext starts a connection attempt that can be cancelled through ctx.
func (a *Adapter) ConnectContext(ctx context.Context, address Address, params ConnectionParams) (result Device, returnErr error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return Device{}, err
	}
	leaveThread, threadErr := enterWinRTThread()
	if threadErr != nil {
		return Device{}, threadErr
	}
	defer leaveThread()

	var winAddr uint64
	for i := range address.MAC {
		winAddr += uint64(address.MAC[i]) << (8 * i)
	}

	// IAsyncOperation<BluetoothLEDevice>
	bleDeviceOp, err := bluetooth.BluetoothLEDeviceFromBluetoothAddressAsync(winAddr)
	if err != nil {
		return Device{}, err
	}
	defer bleDeviceOp.Release()

	// We need to pass the signature of the parameter returned by the async operation:
	// IAsyncOperation<BluetoothLEDevice>
	if err := awaitAsyncOperationContext(ctx, bleDeviceOp, bluetooth.SignatureBluetoothLEDevice); err != nil {
		return Device{}, fmt.Errorf("error connecting to device: %w", err)
	}

	res, err := bleDeviceOp.GetResults()
	if err != nil {
		return Device{}, err
	}

	// The returned BluetoothLEDevice is set to null if FromBluetoothAddressAsync can't find the device identified by bluetoothAddress
	if uintptr(res) == 0x0 {
		return Device{}, fmt.Errorf("device with the given address was not found")
	}

	bleDevice := (*bluetooth.BluetoothLEDevice)(res)
	cleanupDevice := true
	defer func() {
		if cleanupDevice && bleDevice != nil {
			if err := bleDevice.Close(); err != nil {
				returnErr = errors.Join(returnErr, err)
			}
			bleDevice.Release()
		}
	}()

	// Creating a BluetoothLEDevice object by calling this method alone doesn't (necessarily) initiate a connection.
	// To initiate a connection, we need to set GattSession.MaintainConnection to true.
	dID, err := bleDevice.GetBluetoothDeviceId()
	if err != nil {
		return Device{}, err
	}
	defer dID.Release()

	// Windows does not support explicitly connecting to a device.
	// Instead it has the concept of a GATT session that is owned
	// by the calling program.
	gattSessionOp, err := genericattributeprofile.GattSessionFromDeviceIdAsync(dID) // IAsyncOperation<GattSession>
	if err != nil {
		return Device{}, err
	}
	defer gattSessionOp.Release()

	if err := awaitAsyncOperationContext(ctx, gattSessionOp, genericattributeprofile.SignatureGattSession); err != nil {
		return Device{}, fmt.Errorf("error getting gatt session: %w", err)
	}

	gattRes, err := gattSessionOp.GetResults()
	if err != nil {
		return Device{}, err
	}
	newSession := (*genericattributeprofile.GattSession)(gattRes)
	if newSession == nil {
		return Device{}, errors.New("Bluetooth GATT session result is nil")
	}
	cleanupSession := true
	defer func() {
		if cleanupSession {
			if err := newSession.Close(); err != nil {
				returnErr = errors.Join(returnErr, err)
			}
			newSession.Release()
		}
	}()
	// This keeps the device connected until we set maintain_connection = False.
	if err := newSession.SetMaintainConnection(true); err != nil {
		return Device{}, err
	}

	ctx, cancel := context.WithCancel(context.Background())
	state := &deviceState{
		cancel:    cancel,
		device:    bleDevice,
		session:   newSession,
		callbacks: newCallbackGate(),
	}

	device := Device{
		Address: address,
		state:   state,
		ctx:     ctx,
	}

	// https://learn.microsoft.com/es-es/uwp/api/windows.devices.bluetooth.bluetoothledevice.connectionstatuschanged?view=winrt-26100
	// TypedEventHandler<BluetoothLEDevice,object>
	connectionStatusChangedGUID := winrt.ParameterizedInstanceGUID(
		foundation.GUIDTypedEventHandler,
		bluetooth.SignatureBluetoothLEDevice,
		"cinterface(IInspectable)", // object
	)

	handler := foundation.NewTypedEventHandler(ole.NewGUID(connectionStatusChangedGUID), func(instance *foundation.TypedEventHandler, sender, arg unsafe.Pointer) {
		// WinRT can hand this callback a broken COM state (for example a radio
		// removed mid-callback); never let a wrapper panic cross the WinRT
		// trampoline and take down the host process, matching the scan and
		// ValueChanged handlers.
		defer func() { _ = recover() }()
		allowed := state.beginCallback()
		defer state.endCallback()
		if !allowed {
			return
		}
		operationState, operationErr := device.beginOperation()
		if operationErr != nil {
			return
		}
		// The operation lock and WinRT thread must be released on every path,
		// including a panic after beginOperation; otherwise a panicked status
		// read would strand the lock and hang every later device operation.
		defer device.endOperation()
		status, statusErr := operationState.device.GetConnectionStatus()
		if statusErr != nil {
			return
		}
		if status == bluetooth.BluetoothConnectionStatusDisconnected {
			// Never tear down the handler on its own callback stack.
			go func() { _ = device.Disconnect() }()
		}
		if connectHandler := a.connectionHandler(); connectHandler != nil {
			// Dispatch after leaving both the operation lock and WinRT callback
			// stack so handlers may safely re-enter Device methods.
			go invokeConnectionCallbackSafely(func() {
				connectHandler(device, status == bluetooth.BluetoothConnectionStatusConnected)
			})
		}
	})

	// Serialize registration with teardown. The callback may run as soon as
	// AddConnectionStatusChanged returns. Hold operationMutex until all
	// handler state fields are published so a callback cannot observe a
	// partially initialised state.
	state.operationMutex.Lock()
	token, err := state.device.AddConnectionStatusChanged(handler)

	if err != nil {
		state.operationMutex.Unlock()
		handler.Release()
		return Device{}, err
	}
	state.connectionStatusListenerToken = token
	state.connectionStatusListenerAdded = true
	state.connectionStatusListener = handler
	cleanupSession = false
	cleanupDevice = false
	state.operationMutex.Unlock()
	return device, nil
}

// Disconnect from the BLE device and wait until all callbacks and WinRT/GATT
// objects owned by this connection have reached cleanup completion.
func (d Device) Disconnect() error {
	if d.state == nil {
		return nil
	}

	state := d.state
	state.cleanupMutex.Lock()
	if state.cleanupComplete {
		err := state.cleanupAttempt.err
		state.cleanupMutex.Unlock()
		return err
	}
	if state.cleanupStarted {
		attempt := state.cleanupAttempt
		state.cleanupMutex.Unlock()
		<-attempt.done
		return attempt.err
	}
	attempt := &deviceCleanupAttempt{done: make(chan struct{})}
	state.cleanupAttempt = attempt
	state.cleanupStarted = true
	state.closed.Store(true)
	state.cleanupMutex.Unlock()
	go d.cleanup(attempt)
	<-attempt.done
	return attempt.err
}

const maxCleanupRetries = 5

var cleanupRetryBaseDelay = 500 * time.Millisecond

// cleanupRetryDelay backs automatic cleanup retries off exponentially so a
// persistently broken WinRT state cannot hammer the stack.
func cleanupRetryDelay(retries int) time.Duration {
	delay := cleanupRetryBaseDelay
	for i := 0; i < retries; i++ {
		delay *= 2
		if delay > 8*time.Second {
			return 8 * time.Second
		}
	}
	return delay
}

// scheduleCleanupRetry re-attempts a retryable cleanup failure after a delay.
// Automatic retry cannot rely on connection status callbacks: the callback
// gate may already be closed by the failed attempt, and no further events
// arrive for a session nobody released. Without this retry the GATT session,
// device object, and every cached service/characteristic COM handle would
// stay alive for the lifetime of the process.
//
// At most one retry may be pending: interleaved manual attempts and failures
// would otherwise stack timers that each fire an extra attempt, burning the
// bounded retry budget on redundant work.
func (d Device) scheduleCleanupRetry(delay time.Duration) {
	state := d.state
	if state == nil {
		return
	}
	state.cleanupMutex.Lock()
	if state.cleanupComplete || state.cleanupStarted || state.cleanupRetryPending {
		state.cleanupMutex.Unlock()
		return
	}
	state.cleanupRetryPending = true
	state.cleanupMutex.Unlock()
	time.AfterFunc(delay, func() {
		state := d.state
		if state == nil {
			return
		}
		state.cleanupMutex.Lock()
		state.cleanupRetryPending = false
		if state.cleanupComplete || state.cleanupStarted {
			state.cleanupMutex.Unlock()
			return
		}
		attempt := &deviceCleanupAttempt{done: make(chan struct{})}
		state.cleanupAttempt = attempt
		state.cleanupStarted = true
		state.cleanupMutex.Unlock()
		go d.cleanup(attempt)
	})
}

func (d Device) cleanup(attempt *deviceCleanupAttempt) {
	state := d.state
	retryable := false
	ownershipDetached := false
	defer func() {
		if recovered := recover(); recovered != nil {
			if attempt.err == nil {
				attempt.err = fmt.Errorf("bluetooth cleanup panicked: %v", recovered)
			}
			retryable = !ownershipDetached
		}
		if !retryable && attempt.err != nil {
			attempt.err = &DisconnectCleanupError{Err: attempt.err}
		}
		state.cleanupMutex.Lock()
		if retryable {
			state.cleanupStarted = false
			state.cleanupComplete = false
			state.cleanupAttempt = nil
			shouldRetry := state.cleanupRetries < maxCleanupRetries
			nextDelay := cleanupRetryDelay(state.cleanupRetries)
			state.cleanupRetries++
			state.cleanupMutex.Unlock()
			if shouldRetry {
				d.scheduleCleanupRetry(nextDelay)
			}
		} else {
			state.cleanupComplete = true
			state.cleanupMutex.Unlock()
		}
		close(attempt.done)
	}()
	leaveThread, err := enterWinRTThread()
	if err != nil {
		attempt.err = err
		retryable = true
		return
	}
	defer leaveThread()
	// Blocking the callback gate only after the WinRT thread is ready keeps a
	// retryable thread-initialization failure from closing the gate forever:
	// late callbacks stay functional until a cleanup attempt actually
	// continues.
	state.blockCallbacks()

	var warnings []error
	state.drainCallbacksForCleanup(func() {
		if state.cancel != nil {
			if err := cleanupCall("cancel device context", func() error {
				state.cancel()
				return nil
			}); err != nil {
				warnings = append(warnings, err)
			}
		}
		if state.device != nil && state.connectionStatusListenerAdded {
			if err := cleanupCall("remove connection status listener", func() error {
				return state.device.RemoveConnectionStatusChanged(state.connectionStatusListenerToken)
			}); err != nil {
				warnings = append(warnings, err)
			}
		}
		for _, notification := range state.notifications {
			if err := cleanupCall("remove characteristic notification", notification.unregister); err != nil {
				warnings = append(warnings, err)
			}
		}
	})

	// Transfer final COM ownership out of shared state before releasing any
	// object. A panic can then never expose an already-released pointer to a
	// subsequent Disconnect attempt.
	listener := state.connectionStatusListener
	notifications := state.notifications
	characteristics := state.characteristics
	services := state.services
	session := state.session
	device := state.device
	state.cancel = nil
	state.connectionStatusListener = nil
	state.connectionStatusListenerAdded = false
	state.notifications = nil
	state.characteristics = nil
	state.services = nil
	state.session = nil
	state.device = nil
	state.operationMutex.Unlock()
	ownershipDetached = true

	if listener != nil {
		if err := cleanupCall("release connection status listener", func() error {
			listener.Release()
			return nil
		}); err != nil {
			warnings = append(warnings, err)
		}
	}
	for _, notification := range notifications {
		if err := cleanupCall("release notification handler", func() error {
			notification.release()
			return nil
		}); err != nil {
			warnings = append(warnings, err)
		}
	}
	for _, characteristic := range characteristics {
		if characteristic != nil {
			if err := cleanupCall("release characteristic", func() error {
				characteristic.Release()
				return nil
			}); err != nil {
				warnings = append(warnings, err)
			}
		}
	}
	for _, service := range services {
		if service != nil {
			if err := cleanupCall("close service", service.Close); err != nil {
				warnings = append(warnings, err)
			}
			if err := cleanupCall("release service", func() error {
				service.Release()
				return nil
			}); err != nil {
				warnings = append(warnings, err)
			}
		}
	}
	if session != nil {
		if err := cleanupCall("disable maintained connection", func() error {
			return session.SetMaintainConnection(false)
		}); err != nil {
			warnings = append(warnings, err)
		}
		if err := cleanupCall("close GATT session", session.Close); err != nil {
			warnings = append(warnings, err)
		}
		if err := cleanupCall("release GATT session", func() error {
			session.Release()
			return nil
		}); err != nil {
			warnings = append(warnings, err)
		}
	}
	if device != nil {
		if err := cleanupCall("close Bluetooth device", device.Close); err != nil {
			warnings = append(warnings, err)
		}
		if err := cleanupCall("release Bluetooth device", func() error {
			device.Release()
			return nil
		}); err != nil {
			warnings = append(warnings, err)
		}
	}
	if len(warnings) > 0 {
		attempt.err = errors.Join(warnings...)
	}
}

func cleanupCall(operation string, cleanup func() error) (returnErr error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			returnErr = fmt.Errorf("%s panicked: %v", operation, recovered)
		}
	}()
	if err := cleanup(); err != nil {
		return fmt.Errorf("%s: %w", operation, err)
	}
	return nil
}

func invokeConnectionCallbackSafely(callback func()) {
	defer func() {
		_ = recover()
	}()
	callback()
}

// Connected returns whether the device is currently connected.
func (d Device) Connected() (bool, error) {
	state, err := d.beginOperation()
	if err != nil {
		return false, err
	}
	defer d.endOperation()
	status, err := state.device.GetConnectionStatus()
	if err != nil {
		return false, err
	}
	return status == bluetooth.BluetoothConnectionStatusConnected, nil
}

// RequestConnectionParams requests a different connection latency and timeout
// of the given device connection. Fields that are unset will be left alone.
// Whether or not the device will actually honor this, depends on the device and
// on the specific parameters.
//
// On Windows, this call doesn't do anything.
func (d Device) RequestConnectionParams(params ConnectionParams) error {
	// TODO: implement this using
	// BluetoothLEDevice.RequestPreferredConnectionParameters.
	return nil
}

// SetRandomAddress sets the random address to be used for advertising.
func (a *Adapter) SetRandomAddress(mac MAC) error {
	return errors.ErrUnsupported
}
