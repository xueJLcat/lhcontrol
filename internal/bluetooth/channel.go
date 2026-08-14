package bluetooth

import (
	"context"
	"errors"
	"fmt"
	"time"
)

func IdentifyContext(ctx context.Context, station *BaseStation) error {
	if station == nil {
		return fmt.Errorf("station is nil")
	}
	ctx = normalizeContext(ctx)
	if err := ctx.Err(); err != nil {
		return err
	}
	station.mutex.Lock()
	defer station.mutex.Unlock()
	var lastErr error
	maxAttempts := CurrentTiming().IdentifyAttempts
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if contextErr := ctx.Err(); contextErr != nil {
			return contextErr
		}
		if err := connectAndDiscoverInternalContext(ctx, station); err != nil {
			lastErr = err
		} else if !station.Capabilities.Identify || station.identifyCharacteristic == nil {
			return unsupportedCapability("identify", nil)
		} else if err := writeCharacteristicValueInternal(ctx, station.identifyCharacteristic, 0x01); err != nil {
			if isDefinitelyUnsentContextError(ctx, err) {
				return ctx.Err()
			}
			if IsCapabilityUnsupported(err) {
				station.Capabilities.Identify = false
				station.setOperationErrorInternal(err)
				return unsupportedCapability("identify", err)
			}
			lastErr = err
			if IsPossiblySent(err) {
				station.setOperationErrorInternal(err)
				return fmt.Errorf("identify command for %s may have been sent and will not be retried: %w", station.Name, err)
			}
		} else {
			station.setOperationErrorInternal(nil)
			return nil
		}
		if attempt < maxAttempts-1 {
			_ = disconnectInternal(station)
			station.mutex.Unlock()
			err := sleepContext(ctx, CurrentTiming().OperationRetryDelay)
			station.mutex.Lock()
			if err != nil {
				return err
			}
		}
	}
	_ = disconnectInternal(station)
	station.setOperationErrorInternal(lastErr)
	return fmt.Errorf("failed to identify %s after retry: %w", station.Name, lastErr)
}

type ChannelWriteResult struct {
	PreviousChannel int
	Channel         int
	WriteWarning    string
	CommandSent     bool
}

func SetChannel(station *BaseStation, channel int) (ChannelWriteResult, error) {
	return SetChannelContext(context.Background(), station, channel)
}

// SetChannelContext is the cancellable form of SetChannel. Cancellation before
// the write aborts the operation with CommandSent false; cancellation during
// readback keeps CommandSent true and surfaces a confirmation failure.
func SetChannelContext(ctx context.Context, station *BaseStation, channel int) (ChannelWriteResult, error) {
	result := ChannelWriteResult{PreviousChannel: ChannelUnknown, Channel: ChannelUnknown}
	if station == nil {
		return result, fmt.Errorf("station is nil")
	}
	if channel < 1 || channel > 16 {
		return result, fmt.Errorf("channel %d is outside the supported range 1-16", channel)
	}
	ctx = normalizeContext(ctx)
	if err := ctx.Err(); err != nil {
		return result, err
	}
	station.mutex.Lock()
	defer station.mutex.Unlock()
	// Cancellation can land while another operation owns the station lock. A
	// request that never starts must not disconnect an otherwise healthy GATT
	// session in the discovery-error cleanup below.
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if err := connectAndDiscoverInternalContext(ctx, station); err != nil {
		// Cancellation during discovery leaves the session connected;
		// disconnect so the station is not left holding a live GATT session
		// (idempotent when discovery already cleaned up).
		_ = disconnectInternal(station)
		return result, err
	}
	if !station.Capabilities.ChannelWrite || !station.Capabilities.ChannelRead || station.modeCharacteristic == nil {
		return result, unsupportedCapability("safe channel control", nil)
	}
	if err := readChannelInternalContext(ctx, station); err != nil {
		station.setChannelErrorInternal(err)
		station.setOperationErrorInternal(nil)
		if RequiresReconnect(err) {
			_ = disconnectInternal(station)
		}
		return result, fmt.Errorf("failed to read the existing channel for %s: %w", station.Name, err)
	}
	result.PreviousChannel = station.Channel
	if station.Channel == channel {
		result.Channel = station.Channel
		station.LastReadAt = time.Now()
		station.setChannelErrorInternal(nil)
		station.setOperationErrorInternal(nil)
		return result, nil
	}
	if writeErr := writeCharacteristicValueInternal(ctx, station.modeCharacteristic, byte(channel)); writeErr != nil {
		if isDefinitelyUnsentContextError(ctx, writeErr) {
			return result, ctx.Err()
		}
		if IsCapabilityUnsupported(writeErr) {
			station.Capabilities.ChannelWrite = false
			station.setOperationErrorInternal(writeErr)
			return result, unsupportedCapability("channel write", writeErr)
		}
		// Once the transport reports an ambiguous write, a failed readback
		// cannot turn it back into a definitely-unsent command.
		result.CommandSent = IsPossiblySent(writeErr)
		possiblySent, sendClassified := possiblySentClassification(writeErr)
		definitelyNotSent := isDefiniteWriteRejection(writeErr) || (sendClassified && !possiblySent)
		if readErr := readChannelInternalContext(ctx, station); readErr == nil {
			result.Channel = station.Channel
			if station.Channel == channel {
				if definitelyNotSent {
					// The requested outcome was reached independently after the
					// initial read. Report success, but do not claim this operation
					// sent a command the transport explicitly rejected.
					result.CommandSent = false
					result.WriteWarning = fmt.Sprintf("the write was reported as not sent, but channel %d was observed by readback: %v", channel, writeErr)
				} else {
					result.CommandSent = true
					result.WriteWarning = fmt.Sprintf("the write call reported an error, but channel %d was confirmed by readback: %v", channel, writeErr)
				}
				station.LastReadAt = time.Now()
				station.setChannelErrorInternal(nil)
				station.setOperationErrorInternal(nil)
				return result, nil
			}
			writeErr = fmt.Errorf(
				"write reported %v, but readback reported channel %d instead of %d",
				writeErr,
				station.Channel,
				channel,
			)
		} else {
			// Match the initial-read and post-confirmation readback paths: only
			// a genuine transport failure invalidates the cached GATT handles. A
			// capability rejection or an expired read budget is not evidence the
			// link is broken and must not discard an otherwise healthy session.
			if RequiresReconnect(writeErr) || RequiresReconnect(readErr) {
				_ = disconnectInternal(station)
			}
			writeErr = errors.Join(writeErr, fmt.Errorf("final channel read failed: %w", readErr))
		}
		station.setOperationErrorInternal(writeErr)
		return result, fmt.Errorf("failed to write channel %d for %s: %w", channel, station.Name, writeErr)
	}
	result.CommandSent = true
	var confirmationErr error
	consecutiveReadErrors := 0
	confirmTiming := CurrentTiming()
	confirmAttempts := confirmTiming.ChannelConfirmAttempts
	for attempt := 0; attempt < confirmAttempts; attempt++ {
		// Readback waits run outside the station lock so snapshots are not
		// queued behind the confirmation window; the lock is held again
		// before station state is read or written.
		station.mutex.Unlock()
		sleepErr := sleepContext(ctx, confirmTiming.ChannelConfirmInterval)
		station.mutex.Lock()
		if sleepErr != nil {
			confirmationErr = sleepErr
			break
		}
		if err := readChannelInternalContext(ctx, station); err != nil {
			confirmationErr = err
			if IsUnsupportedCapabilityError(err) {
				break
			}
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				break
			}
			consecutiveReadErrors++
			if consecutiveReadErrors >= confirmTiming.ConfirmReconnectThreshold && attempt < confirmAttempts-1 {
				_ = disconnectInternal(station)
				station.mutex.Unlock()
				sleepErr := sleepContext(ctx, confirmTiming.ConfirmReconnectDelay)
				station.mutex.Lock()
				if sleepErr != nil {
					confirmationErr = errors.Join(confirmationErr, sleepErr)
					break
				}
				if reconnectErr := connectAndDiscoverInternalContext(ctx, station); reconnectErr != nil {
					confirmationErr = errors.Join(confirmationErr, fmt.Errorf("channel confirmation reconnect failed: %w", reconnectErr))
					// A failed reconnect must not discard the remaining readback
					// budget; a later attempt can still confirm the write.
				}
				consecutiveReadErrors = 0
			}
			continue
		}
		consecutiveReadErrors = 0
		result.Channel = station.Channel
		if station.Channel == channel {
			station.LastReadAt = time.Now()
			station.setChannelErrorInternal(nil)
			station.setOperationErrorInternal(nil)
			return result, nil
		}
		confirmationErr = fmt.Errorf("reported channel %d, expected %d", station.Channel, channel)
	}
	if err := readChannelInternalContext(ctx, station); err == nil {
		result.Channel = station.Channel
		if station.Channel == channel {
			result.WriteWarning = fmt.Sprintf("channel %d was confirmed by the final readback", channel)
			station.LastReadAt = time.Now()
			station.setChannelErrorInternal(nil)
			station.setOperationErrorInternal(nil)
			return result, nil
		}
		confirmationErr = fmt.Errorf("reported channel %d, expected %d", station.Channel, channel)
	} else {
		confirmationErr = errors.Join(confirmationErr, err)
		if RequiresReconnect(err) {
			_ = disconnectInternal(station)
		}
	}
	if confirmationErr == nil {
		confirmationErr = fmt.Errorf("no channel confirmation was received")
	}
	station.setChannelErrorInternal(confirmationErr)
	station.setOperationErrorInternal(nil)
	return result, fmt.Errorf("channel %d was written but could not be confirmed for %s: %w", channel, station.Name, confirmationErr)
}

// disconnectInternal performs disconnection without locking (must be called within locked context).
// Also removes station from the global tracking list.
