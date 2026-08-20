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
	// classifyUnmarkedWriteFailure wraps a write failure that carries neither a
	// definite rejection nor a definitely-unsent context classification as
	// possibly sent. Without this, an unclassified failure would let callers
	// replay a command that may already have reached the station. A bare
	// cancellation from a contextual writer means the transport never
	// submitted the write; isDefinitelyUnsentContextError preserves that so
	// the caller does not report an unsent command as sent-but-unconfirmed.
	// Both the initial write and the Write-capability fallback below run
	// through it so the replay protection does not depend on which branch
	// produced the failure.
	classifyUnmarkedWriteFailure := func(writeErr error) error {
		if writeErr == nil || isDefiniteWriteRejection(writeErr) ||
			isDefinitelyUnsentContextError(ctx, writeErr) {
			return writeErr
		}
		if _, classified := possiblySentClassification(writeErr); classified {
			return writeErr
		}
		return &PossiblySentError{Err: writeErr}
	}
	properties := bluetooth.CharacteristicPermissions(characteristic.Properties())
	var n int
	var err error
	switch {
	case properties.WriteWithoutResponse():
		n, err = writeWithoutResponse()
		if err != nil && properties.Write() && IsCapabilityUnsupported(err) {
			n, err = writeWithResponse()
		}
	case properties.Write():
		n, err = writeWithResponse()
	default:
		return unsupportedCapability("characteristic write", nil)
	}
	// Both write paths run through the replay-protection classification: a
	// transport that does not self-classify a with-response write failure
	// must not let callers replay a command that may already have landed.
	err = classifyUnmarkedWriteFailure(err)
	if err != nil {
		return transportError("write characteristic", err)
	}
	if n != 1 {
		// A short write cannot be proven unsent on either path; classify both
		// as possibly sent so replay protection never re-sends a command that
		// may already have reached the station.
		shortWriteErr := fmt.Errorf("wrote %d bytes instead of 1", n)
		return transportError("write characteristic", &PossiblySentError{Err: shortWriteErr})
	}
	return nil
}
func isDefiniteWriteRejection(err error) bool {
	var protocolErr bluetooth.AttributeProtocolError
	return errors.As(err, &protocolErr)
}

// isDefinitelyUnsentContextError distinguishes cancellation at the write
// boundary from cancellation after transport submission. The Windows
// transport marks every post-submission failure as possibly sent (and may
// explicitly mark pre-submission failures as not sent), so an unmarked or
// definitely-not-sent context error is safe to abort without reconnecting or
// retrying the command.
func isDefinitelyUnsentContextError(ctx context.Context, err error) bool {
	if ctx == nil || err == nil {
		return false
	}
	contextErr := ctx.Err()
	if contextErr == nil || !errors.Is(err, contextErr) {
		return false
	}
	possiblySent, classified := possiblySentClassification(err)
	return !classified || !possiblySent
}

func writePowerValueInternal(ctx context.Context, station *BaseStation, value byte) error {
	if station.characteristic == nil {
		// Match writeCharacteristicValueInternal's transport classification so
		// callers relying on RequiresReconnect see the same semantics for a
		// missing characteristic as for any other unusable handle.
		return transportError("write power characteristic", fmt.Errorf("power characteristic is unavailable for %s", station.Name))
	}
	return writeCharacteristicValueInternal(ctx, station.characteristic, value)
}

// maxConfirmReconnectExtensions caps how many times the confirmation loop may
// extend its attempt budget after consecutive read errors. Without the cap a
// permanently unreachable station (every reconnect fails, each followed by two
// fast read failures and the next reconnect) would extend the budget forever
// and never terminate: the public wrappers such as SetPowerState run on
// context.Background, so termination cannot rely on a caller deadline alone.
const maxConfirmReconnectExtensions = 3

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
		if timing.ConfirmPollInterval > 0 {
			// Firmware that keeps reporting boot-like raw values only decodes to
			// a trusted On once the boot fallback window has elapsed. The poll
			// must cover that window regardless of the configured attempt count,
			// otherwise every power-on is reported unconfirmed even when the
			// station actually turned on. The surrounding context still bounds
			// the real wait. Ceiling division: with a fractional window/poll
			// ratio the final poll still has to reach past the window.
			fallbackAttempts := int((timing.BootFallbackAfter+timing.ConfirmPollInterval-1)/timing.ConfirmPollInterval) + 1
			if fallbackAttempts > attempts {
				attempts = fallbackAttempts
			}
		}
	}
	var lastErr error
	consecutiveReadErrors := 0
	reconnectExtensions := 0
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
			// Keep the diagnostic recorded before this failure (for example a
			// state mismatch observed by an earlier attempt) instead of
			// dropping it, matching the loop's other early exits.
			if IsUnsupportedCapabilityError(err) {
				return errors.Join(lastErr, err)
			}
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return errors.Join(lastErr, err)
			}
			if IsDeviceValueError(err) {
				// Malformed device data cannot change with a reconnect; the
				// disconnect/reconnect fallback would only repeat the same
				// failure until the budget runs out.
				return errors.Join(lastErr, err)
			}
			if IsProtocolRejection(err) {
				// Security-policy and resource rejections are protocol
				// decisions about a healthy link; the classifier never routes
				// them through RequiresReconnect. Counting them toward the
				// reconnect threshold would tear down and rebuild the session
				// on every threshold, only to fail the same way on each poll,
				// so stop polling like the other unrecoverable read failures.
				return errors.Join(lastErr, err)
			}
			if expectedState == PowerStateSleep &&
				(IsStationNotConnected(err) || RequiresReconnect(err) ||
					station.characteristic == nil || !station.isConnected) {
				// Firmware drops the BLE link as the station powers down, so
				// a disconnect-class read failure right after a sleep command
				// is the expected outcome rather than a transport fault. An OS
				// disconnect landing in an unlocked poll window invalidates the
				// session behind the read too (characteristic cleared), which
				// reports as a bare "characteristic unavailable" transport error
				// the classifiers above do not match. Reconnecting against the
			// sleeping device can never produce a readback; stop
			// immediately and report the command as unconfirmed instead of
			// burning the whole retry and reconnect budget while holding
			// the station lock. Join the sleep-transition marker so higher
			// layers skip the connection-failure accounting this expected
			// disconnect would otherwise trigger.
			_ = disconnectInternal(station)
			return errors.Join(lastErr, err, ErrSleepTransitionDisconnect)
			}
			lastErr = err
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
				// The reconnect resets the boot observation window (disconnect
				// clears bootingSince), so firmware reporting boot-like values
				// needs a fresh full fallback window after the reconnect.
				// Extend the budget to cover it; recompute from the current
				// policy because the restarted window follows the policy at
				// decode time, not the snapshot taken before the first
				// attempt. Reconnects require their own threshold of read
				// errors, so extensions stay bounded by real failures. The
				// extension count is itself capped (maxConfirmReconnectExtensions)
				// so a permanently unreachable station terminates even when the
				// caller supplies no deadline, as the context.Background wrappers
				// do.
				if expectedState == PowerStateOn && reconnectExtensions < maxConfirmReconnectExtensions {
					refreshed := CurrentTiming()
					if refreshed.ConfirmPollInterval > 0 {
						fallbackAttempts := int((refreshed.BootFallbackAfter+refreshed.ConfirmPollInterval-1)/refreshed.ConfirmPollInterval) + 1
						if remaining := attempt + 1 + fallbackAttempts; remaining > attempts {
							attempts = remaining
							reconnectExtensions++
						}
					}
				}
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

// ErrSleepTransitionDisconnect marks a sleep confirmation that ended because
// the firmware dropped the BLE link as the station powered down. That drop is
// the expected outcome of a successful sleep command, not a transport fault:
// callers must neither count it as a connection failure nor schedule
// connection recovery against the now-sleeping station (reconnecting there
// can never produce a readback).
var ErrSleepTransitionDisconnect = errors.New("station disconnected while transitioning to sleep")

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
	// Cancellation can land while another station operation owns the lock.
	// Re-check so an operation that never starts cannot change station state.
	if err := ctx.Err(); err != nil {
		return PowerControlResult{}, err
	}
	maxRetries := CurrentTiming().WriteAttempts
	var err error
	var unconfirmedCommand error
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
			if err != nil {
				// A cancellation that lands between attempts must not swallow
				// the previous attempt's observed failure (a bare context
				// error would read upstream as a clean interruption).
				return PowerControlResult{}, errors.Join(err, contextErr)
			}
			return PowerControlResult{}, contextErr
		}
		if err = connectAndDiscoverInternalContext(ctx, station); err != nil {
			if isConnectNotStarted(err) {
				// The attempt never started; keep the cached session intact and
				// report the cancellation without a cleanup disconnect or retry.
				return PowerControlResult{}, err
			}
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
			// the next attempt touches station state. A cancellation that
			// lands during the wait joins the failed attempt's error: a bare
			// context error would read upstream as a clean interruption and
			// drop the connect failure's bookkeeping.
			if waitErr := station.retryBackoff(ctx); waitErr != nil {
				return PowerControlResult{}, errors.Join(err, waitErr)
			}
			continue
		}
		if !station.Capabilities.PowerWrite {
			return PowerControlResult{}, unsupportedCapability("power control", nil)
		}
		if target == PowerStateStandby && !station.Capabilities.Standby {
			// A previous write was rejected with Value Not Allowed, which
			// downgraded standby while keeping the connection. Refuse the
			// command up front instead of replaying the rejected write and
			// collecting the same ATT rejection again.
			return PowerControlResult{}, unsupportedCapability("standby", nil)
		}
		// A command can legitimately reboot or transition an already-on station,
		// but connection/discovery and capability checks do not. Re-arm the
		// compatibility inference only when a write can actually be attempted,
		// and retain the previous inference in case the transport explicitly
		// rejects that first write before applying it.
		previousBootRawTrustedOn := station.bootRawTrustedOn
		previousBootingSince := station.bootingSince
		station.bootRawTrustedOn = false
		station.bootingSince = time.Time{}
		log.Printf("Bluetooth: Sending %s command to %s", target, station.Name)
		if target == PowerStateSleep {
			var gapErr error
			sleepFinalAttempted, gapErr, err = writeSleepCommandPair(ctx, station, command)
			if gapErr != nil {
				return PowerControlResult{}, gapErr
			}
		} else {
			err = writePowerValueInternal(ctx, station, command)
		}
		if err == nil {
			break
		}
		// A cancelled first write never reached the transport. Preserve the
		// healthy cached connection and do not enter the retry path. Once a
		// sleep prepare write succeeds, however, the paired sequence has begun
		// and the final-write outcome must still be handled as command state.
		if !sleepFinalAttempted && isDefinitelyUnsentContextError(ctx, err) {
			station.restoreBootInference(previousBootRawTrustedOn, previousBootingSince)
			return PowerControlResult{}, ctx.Err()
		}
		// A successful sleep prepare is itself an applied power command. Even
		// when the final write is explicitly rejected (and therefore was not
		// sent), report the paired operation as sent but unconfirmed. Retrying or
		// downgrading all power-write support would hide and potentially replay
		// the already-applied prepare command.
		if target == PowerStateSleep && sleepFinalAttempted {
			unconfirmedCommand = fmt.Errorf("sleep prepare command was sent but final sleep write failed: %w", err)
			break
		}
		if IsPossiblySent(err) {
			unconfirmedCommand = err
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
			station.restoreBootInference(previousBootRawTrustedOn, previousBootingSince)
			station.Capabilities.Standby = false
			station.setOperationErrorInternal(err)
			return PowerControlResult{}, unsupportedCapability("standby", err)
		}
		if IsCapabilityUnsupported(err) {
			station.restoreBootInference(previousBootRawTrustedOn, previousBootingSince)
			station.Capabilities.PowerWrite = false
			station.Capabilities.Standby = false
			station.setOperationErrorInternal(err)
			return PowerControlResult{}, unsupportedCapability("power control", err)
		}
		if possiblySent, classified := possiblySentClassification(err); classified && !possiblySent {
			// The transport explicitly reports the command never reached the
			// device: WinRT never submitted the write, or the peer rejected the
			// request. Neither outcome damaged the cached session (a peer
			// rejection even proves the link), so keep the connection like the
			// standby/unsupported branches above and restore the compatibility
			// inference the write reset; the next attempt retries without paying
			// a reconnect, and no read pays a fresh boot-fallback window for a
			// command that never landed.
			station.restoreBootInference(previousBootRawTrustedOn, previousBootingSince)
			log.Printf("Bluetooth: Write %s was not applied for %s: %v. Retrying on the current connection...", target, station.Name, err)
			if i < maxRetries-1 {
				// Join the rejected write with the interruption: upstream
				// classifies pure context errors as clean interruptions and
				// would otherwise drop the observed write rejection.
				if waitErr := station.retryBackoff(ctx); waitErr != nil {
					return PowerControlResult{}, errors.Join(err, waitErr)
				}
			}
			continue
		}
		log.Printf("Bluetooth: Write %s failed for %s: %v. Retrying...", target, station.Name, err)
		_ = disconnectInternal(station)
		if i < maxRetries-1 {
			if waitErr := station.retryBackoff(ctx); waitErr != nil {
				return PowerControlResult{}, errors.Join(err, waitErr)
			}
		}
	}
	return resolvePowerCommandOutcome(ctx, station, target, maxRetries, err, unconfirmedCommand, ambiguousSleepPrepare)
}

// retryBackoff waits one OperationRetryDelay outside the station lock so
// short readers are not queued behind the wait; the lock is held again when
// the caller continues.
func (station *BaseStation) retryBackoff(ctx context.Context) error {
	station.mutex.Unlock()
	defer station.mutex.Lock()
	return sleepContext(ctx, CurrentTiming().OperationRetryDelay)
}

// restoreBootInference puts back the compatibility inference a write attempt
// reset, used when the transport proves the command was never applied.
func (station *BaseStation) restoreBootInference(bootRawTrustedOn bool, bootingSince time.Time) {
	station.bootRawTrustedOn = bootRawTrustedOn
	station.bootingSince = bootingSince
}

// writeSleepCommandPair sends the paired prepare (0x01) and final sleep
// writes that some Lighthouse 2.0 firmware expects. Once the prepare has
// been sent the pair must complete even when the caller context is already
// cancelled: leaving a sleeping station prepared can wake it, so both waits
// detach from ctx cancellation. A non-nil gapErr abandons the pair between
// the two writes and must terminate the whole operation immediately; writeErr
// carries the final-write outcome through the normal retry classification.
func writeSleepCommandPair(ctx context.Context, station *BaseStation, command byte) (finalAttempted bool, gapErr, writeErr error) {
	if prepareErr := writePowerValueInternal(ctx, station, 0x01); prepareErr != nil {
		return false, nil, prepareErr
	}
	timing := CurrentTiming()
	if timing.PrepareGap > 0 {
		// Release the station lock during the firmware settling gap so
		// short readers are not queued behind it, matching every other
		// wait in this package.
		station.mutex.Unlock()
		waitErr := sleepContext(context.WithoutCancel(ctx), timing.PrepareGap)
		station.mutex.Lock()
		if waitErr != nil {
			return false, waitErr, nil
		}
	}
	// Once prepare succeeds, the final sleep write is a bounded cleanup
	// action. Give it an independent hard deadline: reusing an expired
	// caller deadline here can leave the station prepared (and awake)
	// without ever attempting the matching sleep command.
	finalBudget := finalSleepWriteTimeout
	if finalBudget <= 0 {
		finalBudget = timing.FinalSleepWrite
	}
	finalContext, cancelFinal := context.WithTimeout(context.WithoutCancel(ctx), finalBudget)
	defer cancelFinal()
	// An OS disconnect can invalidate the session during the unlocked gap.
	// The pair must still complete, so rebuild the session under the same
	// detached budget before the final write instead of leaving the station
	// prepared (and awake) with the pair abandoned.
	if station.characteristic == nil {
		if reconnectErr := connectAndDiscoverInternalContext(finalContext, station); reconnectErr != nil {
			return true, nil, fmt.Errorf("sleep pair lost the session during the prepare gap: %w", reconnectErr)
		}
	}
	return true, nil, writePowerValueInternal(finalContext, station, command)
}

// resolvePowerCommandOutcome converts the retry loop's terminal state into
// the operation result. Assumes the caller holds station.mutex and keeps it
// held on return.
func resolvePowerCommandOutcome(ctx context.Context, station *BaseStation, target PowerState, maxRetries int, writeErr, unconfirmedCommand error, ambiguousSleepPrepare bool) (PowerControlResult, error) {
	if writeErr != nil {
		if unconfirmedCommand != nil {
			return resolveUnconfirmedPowerCommand(ctx, station, target, unconfirmedCommand, ambiguousSleepPrepare)
		}
		station.setOperationErrorInternal(writeErr)
		return PowerControlResult{}, fmt.Errorf("failed to write %s command after %d retries: %w", target, maxRetries, writeErr)
	}
	if !station.Capabilities.PowerRead {
		station.clearPowerStateInternal()
		station.setPowerErrorInternal(nil)
		station.setOperationErrorInternal(nil)
		return PowerControlResult{State: target, Confirmed: false}, nil
	}
	if confirmErr := confirmPowerStateInternalContext(ctx, station, target); confirmErr != nil {
		station.setPowerErrorInternal(confirmErr)
		station.setOperationErrorInternal(nil)
		return PowerControlResult{State: station.PowerState, Confirmed: false}, &PowerConfirmationError{
			Target: target,
			Actual: station.PowerState,
			Raw:    station.RawPowerState,
			Err:    fmt.Errorf("state confirmation failed for %s: %w", station.Name, confirmErr),
		}
	}
	station.LastReadAt = time.Now()
	station.setPowerErrorInternal(nil)
	station.setOperationErrorInternal(nil)
	return PowerControlResult{State: target, Confirmed: true}, nil
}

// resolveUnconfirmedPowerCommand settles a command the transport may already
// have applied: it is never silently dropped or replayed. A readable power
// characteristic gets a confirmation attempt; the ambiguous sleep prepare and
// unreadable firmware can only report the command as sent but unconfirmed.
func resolveUnconfirmedPowerCommand(ctx context.Context, station *BaseStation, target PowerState, unconfirmedCommand error, ambiguousSleepPrepare bool) (PowerControlResult, error) {
	err := unconfirmedCommand
	if station.Capabilities.PowerRead && !ambiguousSleepPrepare {
		if confirmationErr := confirmPowerStateInternalContext(ctx, station, target); confirmationErr == nil {
			station.setPowerErrorInternal(nil)
			station.setOperationErrorInternal(nil)
			return PowerControlResult{State: target, Confirmed: true}, nil
		} else {
			err = errors.Join(unconfirmedCommand, confirmationErr)
		}
	} else if !station.Capabilities.PowerRead {
		err = errors.Join(unconfirmedCommand, unsupportedCapability("power confirmation read", nil))
	} else {
		err = errors.Join(unconfirmedCommand, fmt.Errorf("sleep prepare write was ambiguous before the final sleep command"))
	}
	station.setPowerErrorInternal(err)
	station.setOperationErrorInternal(nil)
	return PowerControlResult{State: station.PowerState, Confirmed: false}, &PowerConfirmationError{
		Target: target,
		Actual: station.PowerState,
		Raw:    station.RawPowerState,
		Err:    fmt.Errorf("command outcome could not be confirmed for %s: %w", station.Name, err),
	}
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
