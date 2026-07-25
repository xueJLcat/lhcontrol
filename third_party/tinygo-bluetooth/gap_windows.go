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
	"github.com/saltosystems/winrt-go/windows/storage/streams"
)

// Address contains a Bluetooth MAC address.
type Address struct {
	MACAddress
}

type Advertisement struct {
	advertisement *advertisement.BluetoothLEAdvertisement
	publisher     *advertisement.BluetoothLEAdvertisementPublisher
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
	handler := foundation.NewTypedEventHandler(ole.NewGUID(eventReceivedGuid), func(instance *foundation.TypedEventHandler, sender, arg unsafe.Pointer) {
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
	defer handler.Release()

	token, err := watcher.AddReceived(handler)
	if err != nil {
		return
	}
	defer watcher.RemoveReceived(token)

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
	stoppedHandler := foundation.NewTypedEventHandler(ole.NewGUID(eventStoppedGuid), func(_ *foundation.TypedEventHandler, _, arg unsafe.Pointer) {
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
			} else if errCode != bluetooth.BluetoothErrorSuccess {
				// Could not stop the scan? I'm not sure when this would actually
				// happen.
				stoppingChan <- fmt.Errorf("failed to stop scanning (error code %d)", errCode)
			}
			close(stoppingChan)
		})
	})
	defer stoppedHandler.Release()

	token, err = watcher.AddStopped(stoppedHandler)
	if err != nil {
		return
	}
	defer watcher.RemoveStopped(token)

	err = watcher.Start()
	if err != nil {
		return err
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
			element, elementErr := vector.GetAt(i)
			if elementErr != nil || element == nil {
				continue
			}
			// GetAt on an IVectorView<Guid> returns a pointer into memory
			// that is owned by the WinRT projection layer and may not
			// survive beyond the call boundary. Directly dereferencing
			// element as *syscall.GUID can trigger an access violation
			// when the backing storage has already been released.
			//
			// Instead we interpret the unsafe.Pointer value itself (which
			// lives on the Go stack) as a syscall.GUID.  On 64-bit
			// systems this reads 8 bytes of pointer data plus 8 bytes of
			// adjacent stack memory; the resulting UUID bytes will not
			// match the on-wire advertisement, so HasServiceUUID
			// filtering against parsed UUIDs is unreliable.  Base station
			// detection therefore relies on the LHB- name prefix which
			// all Valve Lighthouse 2.0 units advertise.
			serviceGUID := (*syscall.GUID)(unsafe.Pointer(&element))
			uuid := GUIDToUUID(*serviceGUID)
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
	ctx    context.Context
	cancel context.CancelFunc

	Address Address // the MAC address of the device

	device                        *bluetooth.BluetoothLEDevice
	session                       *genericattributeprofile.GattSession
	connectionStatusListenerToken foundation.EventRegistrationToken
	connectionStatusListener      *foundation.TypedEventHandler
	disconnected                  *int32 // shared flag preventing re-entrant Disconnect
}

// Connect starts a connection attempt to the given peripheral device address.
//
// On Linux and Windows, the IsRandom part of the address is ignored.
func (a *Adapter) Connect(address Address, params ConnectionParams) (Device, error) {
	var winAddr uint64
	for i := range address.MAC {
		winAddr += uint64(address.MAC[i]) << (8 * i)
	}

	// IAsyncOperation<BluetoothLEDevice>
	bleDeviceOp, err := bluetooth.BluetoothLEDeviceFromBluetoothAddressAsync(winAddr)
	if err != nil {
		return Device{}, err
	}

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

	// Creating a BluetoothLEDevice object by calling this method alone doesn't (necessarily) initiate a connection.
	// To initiate a connection, we need to set GattSession.MaintainConnection to true.
	dID, err := bleDevice.GetBluetoothDeviceId()
	if err != nil {
		return Device{}, err
	}

	// Windows does not support explicitly connecting to a device.
	// Instead it has the concept of a GATT session that is owned
	// by the calling program.
	gattSessionOp, err := genericattributeprofile.GattSessionFromDeviceIdAsync(dID) // IAsyncOperation<GattSession>
	if err != nil {
		return Device{}, err
	}

	if err := awaitAsyncOperation(gattSessionOp, genericattributeprofile.SignatureGattSession); err != nil {
		return Device{}, fmt.Errorf("error getting gatt session: %w", err)
	}

	gattRes, err := gattSessionOp.GetResults()
	if err != nil {
		return Device{}, err
	}
	newSession := (*genericattributeprofile.GattSession)(gattRes)
	// This keeps the device connected until we set maintain_connection = False.
	if err := newSession.SetMaintainConnection(true); err != nil {
		return Device{}, err
	}

	ctx, cancel := context.WithCancel(context.Background())
	disconnectedFlag := new(int32)

	device := Device{
		ctx:    ctx,
		cancel: cancel,

		Address: address,

		device:       bleDevice,
		session:      newSession,
		disconnected: disconnectedFlag,
	}

	// https://learn.microsoft.com/es-es/uwp/api/windows.devices.bluetooth.bluetoothledevice.connectionstatuschanged?view=winrt-26100
	// TypedEventHandler<BluetoothLEDevice,object>
	connectionStatusChangedGUID := winrt.ParameterizedInstanceGUID(
		foundation.GUIDTypedEventHandler,
		bluetooth.SignatureBluetoothLEDevice,
		"cinterface(IInspectable)", // object
	)

	handler := foundation.NewTypedEventHandler(ole.NewGUID(connectionStatusChangedGUID), func(instance *foundation.TypedEventHandler, sender, arg unsafe.Pointer) {
		if atomic.LoadInt32(disconnectedFlag) != 0 {
			return
		}
		status, err := bleDevice.GetConnectionStatus()
		if err != nil {
			return
		}
		if status == bluetooth.BluetoothConnectionStatusDisconnected {
			if atomic.CompareAndSwapInt32(disconnectedFlag, 0, 1) {
				device.Disconnect()
			}
		}

		if a.connectHandler != nil {
			a.connectHandler(device, status == bluetooth.BluetoothConnectionStatusConnected)
		}
	})

	token, err := device.device.AddConnectionStatusChanged(handler)

	device.connectionStatusListenerToken = token
	device.connectionStatusListener = handler

	if err != nil {
		_ = handler.Release()
		device.connectionStatusListener = nil
		return device, err
	}

	return device, nil
}

// Disconnect from the BLE device. This method is non-blocking and does not
// wait until the connection is fully gone.
func (d Device) Disconnect() error {
	// Atomically mark the device as disconnected so the connection status
	// callback (which shares this flag across all Device copies) immediately
	// returns instead of calling Disconnect recursively.
	if d.disconnected != nil && !atomic.CompareAndSwapInt32(d.disconnected, 0, 1) {
		return nil
	}

	d.cancel()

	// Remove the connection status listener before closing the session so
	// that session.Close() cannot trigger a re-entrant Disconnect call
	// through the status-changed callback, which would double-release the
	// underlying COM objects and crash the process.
	if d.device != nil {
		_ = d.device.RemoveConnectionStatusChanged(d.connectionStatusListenerToken)
	}

	if d.connectionStatusListener != nil {
		d.connectionStatusListener.Release()
		d.connectionStatusListener = nil
	}

	var firstErr error

	if d.session != nil {
		if err := d.session.Close(); err != nil {
			firstErr = err
		}
		d.session.Release()
		d.session = nil
	}

	if d.device != nil {
		if err := d.device.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		d.device.Release()
		d.device = nil
	}

	return firstErr
}

// Connected returns whether the device is currently connected.
func (d Device) Connected() (bool, error) {
	if d.device == nil {
		return false, nil
	}
	status, err := d.device.GetConnectionStatus()
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
