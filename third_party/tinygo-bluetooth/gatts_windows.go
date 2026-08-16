package bluetooth

import (
	"errors"
	"fmt"
	"sync"
	"syscall"
	"unsafe"

	"github.com/go-ole/go-ole"
	"github.com/saltosystems/winrt-go"
	"github.com/saltosystems/winrt-go/windows/devices/bluetooth/genericattributeprofile"
	"github.com/saltosystems/winrt-go/windows/foundation"
	"github.com/saltosystems/winrt-go/windows/foundation/collections"
	"github.com/saltosystems/winrt-go/windows/storage/streams"
)

// attUnlikelyError is the ATT response used to terminate a server request
// whose handling failed; leaving a request unanswered keeps the central
// waiting until its own ATT timeout expires.
const attUnlikelyError = 0x0E

// Characteristic is a single characteristic in a service. It has an UUID and a
// value.
type Characteristic struct {
	wintCharacteristic *genericattributeprofile.GattLocalCharacteristic
	writeEvent         WriteEvent
	flags              CharacteristicPermissions

	valueMtx *sync.Mutex
	value    []byte
}

// gattsCharacteristicState owns one created WinRT characteristic together with
// the event registrations that RemoveService must undo.
type gattsCharacteristicState struct {
	goChar         *Characteristic
	characteristic *genericattributeprofile.GattLocalCharacteristic
	writeToken     foundation.EventRegistrationToken
	readToken      foundation.EventRegistrationToken
}

// gattsServiceState owns every WinRT object AddService created for one
// service. Service and Characteristic are shared across platforms, so the
// Windows-only handles live here, keyed by the service UUID.
type gattsServiceState struct {
	provider        *genericattributeprofile.GattServiceProvider
	service         *genericattributeprofile.GattLocalService
	writeHandler    *foundation.TypedEventHandler
	readHandler     *foundation.TypedEventHandler
	characteristics []*gattsCharacteristicState
}

var (
	gattsMutex    sync.Mutex
	gattsServices = map[string]*gattsServiceState{}
	// gattsCharsMutex guards every per-service characteristic lookup map.
	// Handlers run on WinRT callback threads while AddService populates the
	// map, so the map needs its own synchronization.
	gattsCharsMutex sync.RWMutex
)

func registerGattServiceState(key string, state *gattsServiceState) {
	gattsMutex.Lock()
	gattsServices[key] = state
	gattsMutex.Unlock()
}

func takeGattServiceState(key string) *gattsServiceState {
	gattsMutex.Lock()
	state := gattsServices[key]
	delete(gattsServices, key)
	gattsMutex.Unlock()
	return state
}

// teardownGattServiceState unregisters events, stops advertising, and releases
// every WinRT object one AddService created. Used by RemoveService and by
// AddService failure paths so a partial setup cannot leak COM handles.
func teardownGattServiceState(state *gattsServiceState) error {
	if state == nil {
		return nil
	}
	var stopErr error
	for _, charState := range state.characteristics {
		if charState.characteristic == nil {
			continue
		}
		_ = charState.characteristic.RemoveWriteRequested(charState.writeToken)
		_ = charState.characteristic.RemoveReadRequested(charState.readToken)
		if charState.goChar != nil && charState.goChar.valueMtx != nil {
			charState.goChar.valueMtx.Lock()
			charState.goChar.wintCharacteristic = nil
			charState.goChar.valueMtx.Unlock()
		}
		charState.characteristic.Release()
	}
	if state.writeHandler != nil {
		state.writeHandler.Release()
	}
	if state.readHandler != nil {
		state.readHandler.Release()
	}
	if state.provider != nil {
		stopErr = state.provider.StopAdvertising()
		state.provider.Release()
	}
	if state.service != nil {
		state.service.Release()
	}
	return stopErr
}

// AddService creates a new service with the characteristics listed in the
// Service struct.
func (a *Adapter) AddService(s *Service) error {
	if s == nil {
		return errors.New("bluetooth: service is nil")
	}
	leaveThread, err := enterWinRTThread()
	if err != nil {
		return err
	}
	defer leaveThread()

	gattServiceOp, err := genericattributeprofile.GattServiceProviderCreateAsync(syscallUUIDFromUUID(s.UUID))
	if err != nil {
		return err
	}
	defer gattServiceOp.Release()

	if err = awaitAsyncOperation(gattServiceOp, genericattributeprofile.SignatureGattServiceProviderResult); err != nil {
		return err
	}

	res, err := gattServiceOp.GetResults()
	if err != nil {
		return err
	}
	serviceProviderResult := (*genericattributeprofile.GattServiceProviderResult)(res)
	defer serviceProviderResult.Release()

	serviceProvider, err := serviceProviderResult.GetServiceProvider()
	if err != nil {
		return err
	}

	localService, err := serviceProvider.GetService()
	if err != nil {
		serviceProvider.Release()
		return err
	}

	state := &gattsServiceState{provider: serviceProvider, service: localService}
	fail := func(err error) error {
		_ = teardownGattServiceState(state)
		return err
	}

	// TODO: "ParameterizedInstanceGUID" + "foundation.NewTypedEventHandler"
	// seems to always return the same instance, need to figure out how to get different instances each time...
	// was following c# source for this flow: https://github.com/microsoft/Windows-universal-samples/blob/main/Samples/BluetoothLE/cs/Scenario3_ServerForeground.xaml.cs
	// which relies on instanced event handlers. for now we'll manually setup our handlers with a map of golang characteristics
	//
	// TypedEventHandler<GattLocalCharacteristic,GattWriteRequestedEventArgs>
	writeGuid := winrt.ParameterizedInstanceGUID(
		foundation.GUIDTypedEventHandler,
		genericattributeprofile.SignatureGattLocalCharacteristic,
		genericattributeprofile.SignatureGattWriteRequestedEventArgs)

	lookupMutex := &sync.RWMutex{}
	goChars := map[syscall.GUID]*Characteristic{}
	lookupChar := func(uuid syscall.GUID) (*Characteristic, bool) {
		lookupMutex.RLock()
		defer lookupMutex.RUnlock()
		char, ok := goChars[uuid]
		return char, ok
	}

	state.writeHandler = foundation.NewTypedEventHandler(ole.NewGUID(writeGuid), func(instance *foundation.TypedEventHandler, sender, args unsafe.Pointer) {
		// WinRT can deliver malformed event state; never let a panic cross
		// the trampoline and take down the host process.
		defer func() { _ = recover() }()
		if sender == nil || args == nil {
			return
		}
		writeReqArgs := (*genericattributeprofile.GattWriteRequestedEventArgs)(args)
		reqAsyncOp, err := writeReqArgs.GetRequestAsync()
		if err != nil || reqAsyncOp == nil {
			return
		}
		defer reqAsyncOp.Release()

		if err = awaitAsyncOperation(reqAsyncOp, genericattributeprofile.SignatureGattWriteRequest); err != nil {
			return
		}

		res, err := reqAsyncOp.GetResults()
		if err != nil || res == nil {
			return
		}
		gattWriteRequest := (*genericattributeprofile.GattWriteRequest)(res)
		defer gattWriteRequest.Release()

		withResponse := false
		if option, err := gattWriteRequest.GetOption(); err == nil {
			withResponse = option == genericattributeprofile.GattWriteOptionWriteWithResponse
		}
		failRequest := func() {
			// Without a response the central waits for its own ATT timeout;
			// answer every failure explicitly.
			if withResponse {
				_ = gattWriteRequest.RespondWithProtocolError(attUnlikelyError)
			}
		}

		buf, err := gattWriteRequest.GetValue()
		if err != nil || buf == nil {
			failRequest()
			return
		}

		offset, err := gattWriteRequest.GetOffset()
		if err != nil {
			buf.Release()
			failRequest()
			return
		}

		characteristic := (*genericattributeprofile.GattLocalCharacteristic)(sender)
		uuid, err := characteristic.GetUuid()
		if err != nil {
			buf.Release()
			failRequest()
			return
		}

		goChar, ok := lookupChar(uuid)
		if !ok {
			buf.Release()
			failRequest()
			return
		}

		if goChar.writeEvent != nil {
			// bufferToSlice takes ownership of buf.
			goChar.writeEvent(0, int(offset), bufferToSlice(buf))
		} else {
			buf.Release()
		}
		if withResponse {
			_ = gattWriteRequest.Respond()
		}
	})

	readGuid := winrt.ParameterizedInstanceGUID(
		foundation.GUIDTypedEventHandler,
		genericattributeprofile.SignatureGattLocalCharacteristic,
		genericattributeprofile.SignatureGattReadRequestedEventArgs)

	state.readHandler = foundation.NewTypedEventHandler(ole.NewGUID(readGuid), func(instance *foundation.TypedEventHandler, sender, args unsafe.Pointer) {
		defer func() { _ = recover() }()
		if sender == nil || args == nil {
			return
		}
		readReqArgs := (*genericattributeprofile.GattReadRequestedEventArgs)(args)
		reqAsyncOp, err := readReqArgs.GetRequestAsync()
		if err != nil || reqAsyncOp == nil {
			return
		}
		defer reqAsyncOp.Release()

		if err = awaitAsyncOperation(reqAsyncOp, genericattributeprofile.SignatureGattReadRequest); err != nil {
			return
		}

		res, err := reqAsyncOp.GetResults()
		if err != nil || res == nil {
			return
		}
		gattReadRequest := (*genericattributeprofile.GattReadRequest)(res)
		defer gattReadRequest.Release()

		characteristic := (*genericattributeprofile.GattLocalCharacteristic)(sender)
		uuid, err := characteristic.GetUuid()
		if err != nil {
			_ = gattReadRequest.RespondWithProtocolError(attUnlikelyError)
			return
		}

		goChar, ok := lookupChar(uuid)
		if !ok || goChar.valueMtx == nil {
			_ = gattReadRequest.RespondWithProtocolError(attUnlikelyError)
			return
		}

		writer, err := streams.NewDataWriter()
		if err != nil {
			_ = gattReadRequest.RespondWithProtocolError(attUnlikelyError)
			return
		}
		defer writer.Release()

		goChar.valueMtx.Lock()
		if len(goChar.value) > 0 {
			err = writer.WriteBytes(uint32(len(goChar.value)), goChar.value)
		}
		goChar.valueMtx.Unlock()
		if err != nil {
			_ = gattReadRequest.RespondWithProtocolError(attUnlikelyError)
			return
		}

		buf, err := writer.DetachBuffer()
		if err != nil || buf == nil {
			_ = gattReadRequest.RespondWithProtocolError(attUnlikelyError)
			return
		}

		if err := gattReadRequest.RespondWithValue(buf); err != nil {
			buf.Release()
			return
		}
		buf.Release()
	})

	for _, char := range s.Characteristics {
		params, err := genericattributeprofile.NewGattLocalCharacteristicParameters()
		if err != nil {
			return fail(err)
		}

		if err = params.SetCharacteristicProperties(genericattributeprofile.GattCharacteristicProperties(char.Flags)); err != nil {
			params.Release()
			return fail(err)
		}

		uuid := syscallUUIDFromUUID(char.UUID)
		createCharOp, err := localService.CreateCharacteristicAsync(uuid, params)
		params.Release()
		if err != nil {
			return fail(err)
		}

		if err = awaitAsyncOperation(createCharOp, genericattributeprofile.SignatureGattLocalCharacteristicResult); err != nil {
			createCharOp.Release()
			return fail(err)
		}

		res, err := createCharOp.GetResults()
		if err != nil {
			createCharOp.Release()
			return fail(err)
		}
		characteristicResults := (*genericattributeprofile.GattLocalCharacteristicResult)(res)
		characteristic, err := characteristicResults.GetCharacteristic()
		characteristicResults.Release()
		createCharOp.Release()
		if err != nil {
			return fail(err)
		}

		charState := &gattsCharacteristicState{goChar: char.Handle, characteristic: characteristic}
		// Populate the lookup before registering events: handlers must never
		// observe a partially built map, and the mutex supplies the memory
		// barrier between this goroutine and the WinRT callback threads.
		if char.Handle != nil {
			lookupMutex.Lock()
			goChars[uuid] = char.Handle
			lookupMutex.Unlock()
			char.Handle.wintCharacteristic = characteristic
			char.Handle.value = char.Value
			char.Handle.valueMtx = &sync.Mutex{}
			char.Handle.flags = char.Flags
			char.Handle.writeEvent = char.WriteEvent
		}

		writeToken, err := characteristic.AddWriteRequested(state.writeHandler)
		if err != nil {
			return fail(err)
		}
		charState.writeToken = writeToken

		readToken, err := characteristic.AddReadRequested(state.readHandler)
		if err != nil {
			_ = characteristic.RemoveWriteRequested(writeToken)
			return fail(err)
		}
		charState.readToken = readToken

		// Keep the object around for Characteristic.Write.
		state.characteristics = append(state.characteristics, charState)
	}

	params, err := genericattributeprofile.NewGattServiceProviderAdvertisingParameters()
	if err != nil {
		return fail(err)
	}

	if err = params.SetIsConnectable(true); err != nil {
		params.Release()
		return fail(err)
	}

	if err = params.SetIsDiscoverable(true); err != nil {
		params.Release()
		return fail(err)
	}

	if err = serviceProvider.StartAdvertisingWithParameters(params); err != nil {
		params.Release()
		return fail(err)
	}
	params.Release()

	registerGattServiceState(s.UUID.String(), state)
	return nil
}

// RemoveService stops advertising the service, removes its event
// registrations, and releases the WinRT objects so the same service can be
// added again.
func (a *Adapter) RemoveService(s *Service) error {
	if s == nil {
		return errors.New("bluetooth: service is nil")
	}
	leaveThread, err := enterWinRTThread()
	if err != nil {
		return err
	}
	defer leaveThread()

	state := takeGattServiceState(s.UUID.String())
	if state != nil {
		return teardownGattServiceState(state)
	}

	// No service was created through this adapter instance. Fall back to the
	// platform lookup so callers can still stop an advertisement registered
	// elsewhere.
	gattServiceOp, err := genericattributeprofile.GattServiceProviderCreateAsync(syscallUUIDFromUUID(s.UUID))
	if err != nil {
		return err
	}
	defer gattServiceOp.Release()

	if err = awaitAsyncOperation(gattServiceOp, genericattributeprofile.SignatureGattServiceProviderResult); err != nil {
		return err
	}

	res, err := gattServiceOp.GetResults()
	if err != nil {
		return err
	}
	serviceProviderResult := (*genericattributeprofile.GattServiceProviderResult)(res)
	defer serviceProviderResult.Release()

	serviceProvider, err := serviceProviderResult.GetServiceProvider()
	if err != nil {
		return err
	}
	defer serviceProvider.Release()

	return serviceProvider.StopAdvertising()
}

// Write replaces the characteristic value with a new value.
func (c *Characteristic) Write(p []byte) (n int, err error) {
	length := len(p)

	if length == 0 {
		return 0, nil // nothing to do
	}

	// Guard before touching valueMtx/wintCharacteristic: a handle that was
	// never added to a service (or has been removed) has neither and would
	// otherwise panic.
	if c.valueMtx == nil {
		return 0, errors.New("bluetooth: characteristic was not added to a service")
	}
	c.valueMtx.Lock()
	flags := c.flags
	characteristic := c.wintCharacteristic
	c.valueMtx.Unlock()
	if characteristic == nil {
		return 0, errors.New("bluetooth: characteristic was removed from its service")
	}

	if c.writeEvent != nil {
		c.writeEvent(0, 0, p)
	}

	// writes are only actually processed on read events from clients, we just set a variable here.
	c.valueMtx.Lock()
	c.value = p
	c.valueMtx.Unlock()

	// only notify if it's enabled, otherwise the below leads to an error
	if flags&CharacteristicNotifyPermission == 0 {
		return length, nil
	}

	leaveThread, err := enterWinRTThread()
	if err != nil {
		return length, err
	}
	defer leaveThread()

	writer, err := streams.NewDataWriter()
	if err != nil {
		return length, err
	}
	defer writer.Release()

	err = writer.WriteBytes(uint32(len(p)), p)
	if err != nil {
		return length, err
	}

	buf, err := writer.DetachBuffer()
	if err != nil {
		return length, err
	}
	defer buf.Release()

	op, err := characteristic.NotifyValueAsync(buf)
	if err != nil {
		return length, err
	}
	defer op.Release()

	// IVectorView<GattClientNotificationResult>
	signature := fmt.Sprintf("pinterface({%s};%s)", collections.GUIDIVectorView, genericattributeprofile.SignatureGattClientNotificationResult)
	if err = awaitAsyncOperation(op, signature); err != nil {
		return length, err
	}

	res, err := op.GetResults()
	if err != nil {
		return length, err
	}

	// TODO: process notification results, just getting this to release
	vec := (*collections.IVectorView)(res)
	vec.Release()

	return length, nil
}

func syscallUUIDFromUUID(uuid UUID) syscall.GUID {
	guid := ole.NewGUID(uuid.String())
	return syscall.GUID{
		Data1: guid.Data1,
		Data2: guid.Data2,
		Data3: guid.Data3,
		Data4: guid.Data4,
	}
}
