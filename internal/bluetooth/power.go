package bluetooth

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"
	"tinygo.org/x/bluetooth"
)

// finalSleepWriteTimeout is a test seam: a positive value overrides the
// policy-driven final sleep write deadline, zero follows the TimingPolicy.
var finalSleepWriteTimeout time.Duration

// TimingPolicy carries the user-tunable protocol timing knobs. The bluetooth
// package stays configuration-free; the application layer pushes values
// through ConfigureTiming after loading or changing settings. Zero-valued
// fields keep the built-in defaults.
type TimingPolicy struct {
	ConfirmAttemptsOn   int
	ConfirmAttemptsOff  int
	ConfirmPollInterval time.Duration
	BootFallbackAfter   time.Duration
	// FinalSleepWrite bounds the paired-write sleep command that must be
	// completed even when the caller context is already cancelled.
	FinalSleepWrite time.Duration
	// PrepareGap is the firmware settling delay between the prepare (0x01)
	// and final (0x00) writes of the paired sleep sequence.
	PrepareGap time.Duration
	// DiscoveryAttempts/DiscoveryRetryDelay bound GATT service discovery
	// retries when a first connection attempt fails.
	DiscoveryAttempts   int
	DiscoveryRetryDelay time.Duration
	// WriteAttempts bounds the power command write loop (including the
	// reconnect fallback between attempts); OperationRetryDelay is the backoff
	// between those attempts.
	WriteAttempts       int
	OperationRetryDelay time.Duration
	// ChannelConfirmAttempts/ChannelConfirmInterval drive the channel write
	// readback polling.
	ChannelConfirmAttempts int
	ChannelConfirmInterval time.Duration
	// ConfirmReconnectThreshold/ConfirmReconnectDelay apply to both power and
	// channel confirmation: after that many consecutive read errors a
	// reconnect is attempted, waiting the delay first.
	ConfirmReconnectThreshold int
	ConfirmReconnectDelay     time.Duration
	IdentifyAttempts          int
	PresenceMissThreshold     int
}

const (
	defaultConfirmAttemptsOn         = 51
	defaultConfirmAttemptsOff        = 15
	defaultConfirmPollInterval       = 200 * time.Millisecond
	defaultBootFallbackAfter         = 8 * time.Second
	defaultFinalSleepWrite           = 30 * time.Second
	defaultPrepareGap                = 50 * time.Millisecond
	defaultDiscoveryAttempts         = 3
	defaultDiscoveryRetryDelay       = 500 * time.Millisecond
	defaultWriteAttempts             = 2
	defaultOperationRetryDelay       = 500 * time.Millisecond
	defaultChannelConfirmAttempts    = 5
	defaultChannelConfirmInterval    = 250 * time.Millisecond
	defaultConfirmReconnectThreshold = 2
	defaultConfirmReconnectDelay     = 250 * time.Millisecond
	defaultIdentifyAttempts          = 2
	defaultPresenceMissThreshold     = 2
)

var (
	timingMutex  sync.RWMutex
	timingPolicy = TimingPolicy{
		ConfirmAttemptsOn:         defaultConfirmAttemptsOn,
		ConfirmAttemptsOff:        defaultConfirmAttemptsOff,
		ConfirmPollInterval:       defaultConfirmPollInterval,
		BootFallbackAfter:         defaultBootFallbackAfter,
		FinalSleepWrite:           defaultFinalSleepWrite,
		PrepareGap:                defaultPrepareGap,
		DiscoveryAttempts:         defaultDiscoveryAttempts,
		DiscoveryRetryDelay:       defaultDiscoveryRetryDelay,
		WriteAttempts:             defaultWriteAttempts,
		OperationRetryDelay:       defaultOperationRetryDelay,
		ChannelConfirmAttempts:    defaultChannelConfirmAttempts,
		ChannelConfirmInterval:    defaultChannelConfirmInterval,
		ConfirmReconnectThreshold: defaultConfirmReconnectThreshold,
		ConfirmReconnectDelay:     defaultConfirmReconnectDelay,
		IdentifyAttempts:          defaultIdentifyAttempts,
		PresenceMissThreshold:     defaultPresenceMissThreshold,
	}
)

// ConfigureTiming replaces the active timing policy, filling zero-valued
// fields with the built-in defaults so partial updates stay safe.
func ConfigureTiming(policy TimingPolicy) {
	if policy.ConfirmAttemptsOn <= 0 {
		policy.ConfirmAttemptsOn = defaultConfirmAttemptsOn
	}
	if policy.ConfirmAttemptsOff <= 0 {
		policy.ConfirmAttemptsOff = defaultConfirmAttemptsOff
	}
	if policy.ConfirmPollInterval <= 0 {
		policy.ConfirmPollInterval = defaultConfirmPollInterval
	}
	if policy.BootFallbackAfter <= 0 {
		policy.BootFallbackAfter = defaultBootFallbackAfter
	}
	if policy.FinalSleepWrite <= 0 {
		policy.FinalSleepWrite = defaultFinalSleepWrite
	}
	if policy.PrepareGap < 0 {
		policy.PrepareGap = defaultPrepareGap
	}
	if policy.DiscoveryAttempts <= 0 {
		policy.DiscoveryAttempts = defaultDiscoveryAttempts
	}
	if policy.DiscoveryRetryDelay <= 0 {
		policy.DiscoveryRetryDelay = defaultDiscoveryRetryDelay
	}
	if policy.WriteAttempts <= 0 {
		policy.WriteAttempts = defaultWriteAttempts
	}
	if policy.OperationRetryDelay <= 0 {
		policy.OperationRetryDelay = defaultOperationRetryDelay
	}
	if policy.ChannelConfirmAttempts <= 0 {
		policy.ChannelConfirmAttempts = defaultChannelConfirmAttempts
	}
	if policy.ChannelConfirmInterval <= 0 {
		policy.ChannelConfirmInterval = defaultChannelConfirmInterval
	}
	if policy.ConfirmReconnectThreshold <= 0 {
		policy.ConfirmReconnectThreshold = defaultConfirmReconnectThreshold
	}
	if policy.ConfirmReconnectDelay <= 0 {
		policy.ConfirmReconnectDelay = defaultConfirmReconnectDelay
	}
	if policy.IdentifyAttempts <= 0 {
		policy.IdentifyAttempts = defaultIdentifyAttempts
	}
	if policy.PresenceMissThreshold <= 0 {
		policy.PresenceMissThreshold = defaultPresenceMissThreshold
	}
	timingMutex.Lock()
	timingPolicy = policy
	timingMutex.Unlock()
}

// CurrentTiming returns the active timing policy.
func CurrentTiming() TimingPolicy {
	timingMutex.RLock()
	defer timingMutex.RUnlock()
	return timingPolicy
}

func writeCharacteristicValueInternal(ctx context.Context, characteristic characteristicIO, value byte) error {
	if characteristic == nil {
		return transportError("write characteristic", fmt.Errorf("characteristic is unavailable"))
	}
	ctx = normalizeContext(ctx)
	if err := ctx.Err(); err != nil {
		return err
	}
	// Cancellable writes keep the operation lock bounded by the caller's
	// context instead of the transport's full budget. A cancelled
	// already-submitted write still carries the transport's possibly-sent
	// classification, so callers never replay it.
	contextWriter, hasContextWriter := characteristic.(contextCharacteristicWriter)
	writeWithoutResponse := func() (int, error) {
		if hasContextWriter {
			return contextWriter.WriteWithoutResponseContext(ctx, []byte{value})
		}
		return characteristic.WriteWithoutResponse([]byte{value})
	}
	writeWithResponse := func() (int, error) {
		if hasContextWriter {
			return contextWriter.WriteContext(ctx, []byte{value})
		}
		return characteristic.Write([]byte{value})
	}
	properties := bluetooth.CharacteristicPermissions(characteristic.Properties())
	var n int
	var err error
	switch {
	case properties.WriteWithoutResponse():
		n, err = writeWithoutResponse()
		if err != nil && properties.Write() && IsCapabilityUnsupported(err) {
			n, err = writeWithResponse()
		} else if err != nil && !isDefiniteWriteRejection(err) {
			possiblySent, classified := possiblySentClassification(err)
			if !classified {
				err = &PossiblySentError{Err: err}
			} else if !possiblySent {
				// A transport-provided definite classification preserves retry safety.
			}
		}
	case properties.Write():
		n, err = writeWithResponse()
	default:
		return unsupportedCapability("characteristic write", nil)
	}
	if err != nil {
		return transportError("write characteristic", err)
	}
	if n != 1 {
		shortWriteErr := fmt.Errorf("wrote %d bytes instead of 1", n)
		if properties.WriteWithoutResponse() {
			return transportError("write characteristic", &PossiblySentError{Err: shortWriteErr})
		}
		return transportError("write characteristic", shortWriteErr)
	}
	return nil
}
func isDefiniteWriteRejection(err error) bool {
	var protocolErr bluetooth.AttributeProtocolError
	return errors.As(err, &protocolErr)
}
func writePowerValueInternal(ctx context.Context, station *BaseStation, value byte) error {
	if station.characteristic == nil {
		return fmt.Errorf("power characteristic is unavailable")
	}
	return writeCharacteristicValueInternal(ctx, station.characteristic, value)
}

// confirmPowerStateInternalContext polls briefly because Lighthouse state
// transitions are not always visible immediately after a successful GATT write.
// Assumes caller holds station.mutex. Inter-attempt sleeps release the lock so
// snapshots and other short readers are not queued behind the whole polling
// window; the lock is always held again on return. Attempt counts and the
// poll interval follow the user-configured TimingPolicy.
func confirmPowerStateInternalContext(ctx context.Context, station *BaseStation, expectedState PowerState) error {
	timing := CurrentTiming()
	attempts := timing.ConfirmAttemptsOff
	if expectedState == PowerStateOn {
		attempts = timing.ConfirmAttemptsOn
	}
	var lastErr error
	consecutiveReadErrors := 0
	for attempt := 0; attempt < attempts; attempt++ {
		if contextErr := ctx.Err(); contextErr != nil {
			if lastErr != nil {
				return errors.Join(lastErr, contextErr)
			}
			return contextErr
		}
		if attempt > 0 {
			station.mutex.Unlock()
			err := sleepContext(ctx, timing.ConfirmPollInterval)
			station.mutex.Lock()
			if err != nil {
				if lastErr != nil {
					return errors.Join(lastErr, err)
				}
				return err
			}
		}
		if err := readPowerStateInternalContext(ctx, station); err != nil {
			lastErr = err
			if IsUnsupportedCapabilityError(err) {
				return err
			}
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return err
			}
			consecutiveReadErrors++
			if consecutiveReadErrors >= timing.ConfirmReconnectThreshold && attempt < attempts-1 {
				_ = disconnectInternal(station)
				station.mutex.Unlock()
				sleepErr := sleepContext(ctx, timing.ConfirmReconnectDelay)
				station.mutex.Lock()
				if sleepErr != nil {
					return errors.Join(lastErr, sleepErr)
				}
				if reconnectErr := connectAndDiscoverInternalContext(ctx, station); reconnectErr != nil {
					lastErr = errors.Join(lastErr, fmt.Errorf("confirmation reconnect failed: %w", reconnectErr))
					// A failed reconnect must not discard the remaining budget:
					// a rebooting station is unreachable exactly while this polling
					// exists for, and a later attempt can still confirm the state.
				}
				consecutiveReadErrors = 0
			}
			continue
		}
		consecutiveReadErrors = 0
		if station.PowerState == expectedState {
			return nil
		}
		lastErr = fmt.Errorf(
			"reported %s with raw 0x%02X, expected a confirmed %s state",
			station.PowerState,
			byte(station.RawPowerState),
			expectedState,
		)
	}
	return lastErr
}
func IsPowerStateConfirmed(expectedState PowerState, raw int) bool {
	switch expectedState {
	case PowerStateSleep:
		return raw == 0x00
	case PowerStateStandby:
		return raw == 0x02
	case PowerStateOn:
		return raw == 0x09 || raw == 0x0B
	default:
		return false
	}
}

// IsPowerStateVerified reports whether a decoded state is backed by a raw
// value that decodePowerStateWithHistory accepts as that stable state. Some
// Lighthouse 2.0 firmware keeps reporting booting raw values (such as 0x01)
// while already awake, and the decode history falls back to On for them, so
// verification follows the displayed state instead of raw values such
// firmware never produces.
func IsPowerStateVerified(decoded PowerState, raw int) bool {
	if IsPowerStateConfirmed(decoded, raw) {
		return true
	}
	return decoded == PowerStateOn && (raw == 0x01 || raw == 0x08)
}

type PowerControlResult struct {
	State     PowerState
	Confirmed bool
}

// PowerConfirmationError means the write completed, but the target stable
// state could not be confirmed by readback.
type PowerConfirmationError struct {
	Target PowerState
	Actual PowerState
	Raw    int
	Err    error
}

func (e *PowerConfirmationError) Error() string {
	raw := "unavailable"
	if e.Raw >= 0 {
		raw = fmt.Sprintf("0x%02X", byte(e.Raw))
	}
	return fmt.Sprintf(
		"%s command sent but state confirmation failed (actual %s, raw %s): %v",
		e.Target,
		e.Actual,
		raw,
		e.Err,
	)
}
func (e *PowerConfirmationError) Unwrap() error {
	return e.Err
}

// SetPowerState writes a stable target state and confirms it when the firmware
// exposes a readable power characteristic.
func SetPowerState(station *BaseStation, target PowerState) (PowerControlResult, error) {
	return SetPowerStateContext(context.Background(), station, target)
}

// SetPowerStateContext is the cancellable form of SetPowerState. Cancellation
// before the command write aborts the operation; once the write has been
// issued the outcome is still reported (confirmed or confirmation error) so a
// possibly-sent command is never silently dropped.
func SetPowerStateContext(ctx context.Context, station *BaseStation, target PowerState) (PowerControlResult, error) {
	if station == nil {
		return PowerControlResult{}, fmt.Errorf("station is nil")
	}
	if target != PowerStateOn && target != PowerStateStandby && target != PowerStateSleep {
		return PowerControlResult{}, fmt.Errorf("invalid stable target state %s", target)
	}
	ctx = normalizeContext(ctx)
	if err := ctx.Err(); err != nil {
		return PowerControlResult{}, err
	}
	station.mutex.Lock()
	defer station.mutex.Unlock()
	// A new command can legitimately reboot or transition an already-on
	// station. Re-arm boot-state observation before writing so the previous
	// connection's compatibility inference cannot hide that transition.
	station.bootRawTrustedOn = false
	maxRetries := CurrentTiming().WriteAttempts
	var err error
	var ambiguousWrite error
	ambiguousSleepPrepare := false
	command := byte(0x00)
	switch target {
	case PowerStateOn:
		command = 0x01
	case PowerStateStandby:
		command = 0x02
	case PowerStateSleep:
		command = 0x00
	}
	for i := 0; i < maxRetries; i++ {
		sleepFinalAttempted := false
		if contextErr := ctx.Err(); contextErr != nil {
			return PowerControlResult{}, contextErr
		}
		if err = connectAndDiscoverInternalContext(ctx, station); err != nil {
			log.Printf("Bluetooth: connect/discover failed during power attempt %d/%d for %s: %v", i+1, maxRetries, station.Name, err)
			if i == maxRetries-1 {
				// Cancellation during discovery leaves the session connected;
				// disconnect so the station is not left holding a live GATT
				// session (idempotent when discovery already cleaned up).
				_ = disconnectInternal(station)
				return PowerControlResult{}, fmt.Errorf("failed to connect/discover before power command: %w", err)
			}
			_ = disconnectInternal(station)
			// Retry backoff runs outside the station lock so short readers
			// are not queued behind the wait; the lock is held again before
			// the next attempt touches station state.
			station.mutex.Unlock()
			waitErr := sleepContext(ctx, CurrentTiming().OperationRetryDelay)
			station.mutex.Lock()
			if waitErr != nil {
				return PowerControlResult{}, waitErr
			}
			continue
		}
		if !station.Capabilities.PowerWrite {
			return PowerControlResult{}, unsupportedCapability("power control", nil)
		}
		log.Printf("Bluetooth: Sending %s command to %s", target, station.Name)
		if target == PowerStateSleep {
			// Some Lighthouse 2.0 firmware expects wake/prepare then sleep.
			err = writePowerValueInternal(ctx, station, 0x01)
			if err == nil {
				// Once prepare has been sent, complete this paired write even when
				// shutdown cancels ctx. Leaving a sleeping station prepared can wake it.
				timing := CurrentTiming()
				if timing.PrepareGap > 0 {
					// Release the station lock during the firmware settling gap so
					// short readers are not queued behind it, matching every other
					// wait in this package.
					station.mutex.Unlock()
					waitErr := sleepContext(context.WithoutCancel(ctx), timing.PrepareGap)
					station.mutex.Lock()
					if waitErr != nil {
						return PowerControlResult{}, waitErr
					}
				}
				sleepFinalAttempted = true
				// Once prepare succeeds, the final sleep write is a bounded cleanup
				// action. Give it an independent hard deadline: reusing an expired
				// caller deadline here can leave the station prepared (and awake)
				// without ever attempting the matching sleep command.
				finalBudget := finalSleepWriteTimeout
				if finalBudget <= 0 {
					finalBudget = timing.FinalSleepWrite
				}
				finalContext, cancelFinal := context.WithTimeout(context.WithoutCancel(ctx), finalBudget)
				err = writePowerValueInternal(finalContext, station, command)
				cancelFinal()
			}
		} else {
			err = writePowerValueInternal(ctx, station, command)
		}
		if err == nil {
			break
		}
		if IsPossiblySent(err) {
			ambiguousWrite = err
			ambiguousSleepPrepare = target == PowerStateSleep && !sleepFinalAttempted
			if ambiguousSleepPrepare {
				// The final sleep command was not attempted, so observing the old
				// sleeping state cannot confirm completion of the sequence.
				_ = readPowerStateInternalContext(ctx, station)
			}
			break
		}
		var protocolErr bluetooth.AttributeProtocolError
		if target == PowerStateStandby &&
			errors.As(err, &protocolErr) &&
			protocolErr == bluetooth.ErrAttValueNotAllowed {
			station.Capabilities.Standby = false
			station.setOperationErrorInternal(err)
			return PowerControlResult{}, unsupportedCapability("standby", err)
		}
		if IsCapabilityUnsupported(err) {
			station.Capabilities.PowerWrite = false
			station.Capabilities.Standby = false
			station.setOperationErrorInternal(err)
			return PowerControlResult{}, unsupportedCapability("power control", err)
		}
		log.Printf("Bluetooth: Write %s failed for %s: %v. Retrying...", target, station.Name, err)
		_ = disconnectInternal(station)
		if i < maxRetries-1 {
			station.mutex.Unlock()
			waitErr := sleepContext(ctx, CurrentTiming().OperationRetryDelay)
			station.mutex.Lock()
			if waitErr != nil {
				return PowerControlResult{}, waitErr
			}
		}
	}
	if err != nil {
		if ambiguousWrite != nil {
			if station.Capabilities.PowerRead && !ambiguousSleepPrepare {
				if confirmationErr := confirmPowerStateInternalContext(ctx, station, target); confirmationErr == nil {
					station.setPowerErrorInternal(nil)
					station.setOperationErrorInternal(nil)
					return PowerControlResult{State: target, Confirmed: true}, nil
				} else {
					err = errors.Join(ambiguousWrite, confirmationErr)
				}
			} else if !station.Capabilities.PowerRead {
				err = errors.Join(ambiguousWrite, unsupportedCapability("power confirmation read", nil))
			} else {
				err = errors.Join(ambiguousWrite, fmt.Errorf("sleep prepare write was ambiguous before the final sleep command"))
			}
			station.setPowerErrorInternal(err)
			station.setOperationErrorInternal(nil)
			return PowerControlResult{State: station.PowerState, Confirmed: false}, &PowerConfirmationError{
				Target: target,
				Actual: station.PowerState,
				Raw:    station.RawPowerState,
				Err:    fmt.Errorf("possibly-sent command could not be confirmed for %s: %w", station.Name, err),
			}
		}
		station.setOperationErrorInternal(err)
		return PowerControlResult{}, fmt.Errorf("failed to write %s command after %d retries: %w", target, maxRetries, err)
	}
	if !station.Capabilities.PowerRead {
		station.setPowerStateInternal(PowerStateUnknown, RawPowerStateUnknown)
		station.setPowerErrorInternal(nil)
		station.setOperationErrorInternal(nil)
		return PowerControlResult{State: target, Confirmed: false}, nil
	}
	if err = confirmPowerStateInternalContext(ctx, station, target); err != nil {
		station.setPowerErrorInternal(err)
		station.setOperationErrorInternal(nil)
		return PowerControlResult{State: station.PowerState, Confirmed: false}, &PowerConfirmationError{
			Target: target,
			Actual: station.PowerState,
			Raw:    station.RawPowerState,
			Err:    fmt.Errorf("state confirmation failed for %s: %w", station.Name, err),
		}
	}
	station.LastReadAt = time.Now()
	station.setPowerErrorInternal(nil)
	station.setOperationErrorInternal(nil)
	return PowerControlResult{State: target, Confirmed: true}, nil
}
func PowerOn(station *BaseStation) error {
	_, err := SetPowerState(station, PowerStateOn)
	return err
}
func PowerOff(station *BaseStation) error {
	_, err := SetPowerState(station, PowerStateSleep)
	return err
}
func Identify(station *BaseStation) error {
	return IdentifyContext(context.Background(), station)
}

// IdentifyContext is the cancellable form of Identify. Cancellation before
// the write aborts the request; a possibly-sent identify signal is never
// retried, matching Identify.
