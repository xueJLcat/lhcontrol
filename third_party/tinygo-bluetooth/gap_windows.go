package bluetooth

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"syscall"
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

		manData, err := advertisement.BluetoothLEManufacturerDataCreate(optManData.CompanyID, buf)
		if err != nil {
			return err
		}

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
// that runs only after the watcher has successfully entered the Started state.
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
	a.watcher = watcher
	a.watcherMutex.Unlock()
	defer func() {
		a.watcherMutex.Lock()
		if a.watcher == watcher {
			a.watcher = nil
		}
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
	if started != nil {
		started()
	}

	// Wait until advertisement has stopped, and finish.
	return <-stoppingChan
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
		return err
	}
	defer leaveThread()

	a.watcherMutex.RLock()
	defer a.watcherMutex.RUnlock()
	if a.watcher == nil {
		return errNotScanning
	}
	return a.watcher.Stop()
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
	leaveThread     func()
	cancel          context.CancelFunc

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
		if d.state.leaveThread != nil {
			d.state.leaveThread()
			d.state.leaveThread = nil
		}
		d.state.operationMutex.Unlock()
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
	d.state.notifications = append(d.state.notifications, registration)
	return nil
}

// Connect starts a connection attempt to the given peripheral device address.
//
// On Linux and Windows, the IsRandom part of the address is ignored.
func (a *Adapter) Connect(address Address, params ConnectionParams) (Device, error) {
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
	if err := awaitAsyncOperation(bleDeviceOp, bluetooth.SignatureBluetoothLEDevice); err != nil {
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
			_ = bleDevice.Close()
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

	if err := awaitAsyncOperation(gattSessionOp, genericattributeprofile.SignatureGattSession); err != nil {
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
			_ = newSession.Close()
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
		allowed := state.beginCallback()
		defer state.endCallback()
		if !allowed {
			return
		}
		operationState, operationErr := device.beginOperation()
		if operationErr != nil {
			return
		}
		status, err := operationState.device.GetConnectionStatus()
		device.endOperation()
		if err != nil {
			return
		}
		if status == bluetooth.BluetoothConnectionStatusDisconnected {
			// Do not release the currently executing handler on its own
			// callback stack. Disconnect is idempotent across Device copies.
			go func() { _ = device.Disconnect() }()
		}

		if a.connectHandler != nil {
			a.connectHandler(device, status == bluetooth.BluetoothConnectionStatusConnected)
		}
	})

	// Serialize registration with teardown. The callback may run as soon as
	// AddConnectionStatusChanged returns, so publish all handler ownership
	// before allowing it to inspect or disconnect the shared state.
	state.operationMutex.Lock()
	state.connectionStatusListener = handler
	token, err := state.device.AddConnectionStatusChanged(handler)

	if err != nil {
		state.connectionStatusListener = nil
		state.operationMutex.Unlock()
		handler.Release()
		_ = device.Disconnect()
		return Device{}, err
	}
	state.connectionStatusListenerToken = token
	state.connectionStatusListenerAdded = true
	state.operationMutex.Unlock()
	cleanupSession = false
	cleanupDevice = false

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

func (d Device) cleanup(attempt *deviceCleanupAttempt) {
	state := d.state
	retryable := false
	defer func() {
		if recovered := recover(); recovered != nil {
			if attempt.err == nil {
				attempt.err = fmt.Errorf("bluetooth cleanup panicked: %v", recovered)
			}
		}
		state.cleanupMutex.Lock()
		if retryable {
			state.cleanupStarted = false
			state.cleanupComplete = false
			state.cleanupAttempt = nil
		} else {
			state.cleanupComplete = true
		}
		close(attempt.done)
		state.cleanupMutex.Unlock()
	}()
	state.blockCallbacks()
	leaveThread, err := enterWinRTThread()
	if err != nil {
		attempt.err = err
		retryable = true
		return
	}
	defer leaveThread()

	state.drainCallbacksForCleanup(func() {
		if state.cancel != nil {
			state.cancel()
		}
		if state.device != nil && state.connectionStatusListenerAdded {
			_ = state.device.RemoveConnectionStatusChanged(state.connectionStatusListenerToken)
		}
		for _, notification := range state.notifications {
			if notification.characteristic != nil {
				_ = notification.characteristic.RemoveValueChanged(notification.token)
			}
		}
	})
	defer state.operationMutex.Unlock()

	if state.connectionStatusListener != nil {
		state.connectionStatusListener.Release()
		state.connectionStatusListener = nil
	}
	for _, notification := range state.notifications {
		if notification.handler != nil {
			notification.handler.Release()
		}
	}
	state.notifications = nil
	for _, characteristic := range state.characteristics {
		if characteristic != nil {
			characteristic.Release()
		}
	}
	state.characteristics = nil
	for _, service := range state.services {
		if service != nil {
			_ = service.Close()
			service.Release()
		}
	}
	state.services = nil
	if state.session != nil {
		_ = state.session.SetMaintainConnection(false)
		if err := state.session.Close(); err != nil {
			attempt.err = err
		}
		state.session.Release()
		state.session = nil
	}
	if state.device != nil {
		if err := state.device.Close(); err != nil && attempt.err == nil {
			attempt.err = err
		}
		state.device.Release()
		state.device = nil
	}
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
