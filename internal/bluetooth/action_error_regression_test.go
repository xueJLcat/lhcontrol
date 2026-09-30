package bluetooth

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	tinybluetooth "tinygo.org/x/bluetooth"
)

type cancelIdentifyConnectAdapter struct {
	*reconnectCountingAdapter
	cancel   context.CancelFunc
	failures []error
	calls    int
}

func (a *cancelIdentifyConnectAdapter) Connect(tinybluetooth.Address, tinybluetooth.ConnectionParams) (tinybluetooth.Device, error) {
	err := a.failures[a.calls]
	a.calls++
	if a.calls == len(a.failures) {
		a.cancel()
	}
	return tinybluetooth.Device{}, err
}

func TestIdentifyCancellationKeepsCurrentConnectFailure(t *testing.T) {
	for _, attempts := range []int{1, 2} {
		t.Run(fmt.Sprintf("%d attempts", attempts), func(t *testing.T) {
			ConfigureTiming(TimingPolicy{IdentifyAttempts: 3, OperationRetryDelay: time.Millisecond})
			t.Cleanup(func() { ConfigureTiming(TimingPolicy{}) })
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			failures := []error{errors.New("first connection failure")}
			if attempts == 2 {
				failures = append(failures, tinybluetooth.ErrDeviceDisconnected)
			}
			fake := &cancelIdentifyConnectAdapter{
				reconnectCountingAdapter: &reconnectCountingAdapter{},
				cancel:                   cancel, failures: failures,
			}
			original := adapter
			adapter = fake
			t.Cleanup(func() { adapter = original })
			err := IdentifyContext(ctx, &BaseStation{Name: "LHB-IDENTIFY"})
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("error = %v, want cancellation", err)
			}
			for _, failure := range failures {
				if !errors.Is(err, failure) {
					t.Errorf("error = %v, lost connection failure %v", err, failure)
				}
			}
			if !RequiresReconnect(err) {
				t.Errorf("error = %v, lost transport recovery classification", err)
			}
		})
	}
}

func TestChannelRejectedWriteKeepsErrorAfterSuccessfulMismatchedRead(t *testing.T) {
	for _, failure := range []error{
		tinybluetooth.ErrAttValueNotAllowed,
		errors.Join(&classifiedWriteError{possiblySent: false}, tinybluetooth.ErrDeviceDisconnected),
	} {
		t.Run(failure.Error(), func(t *testing.T) {
			mode := &fakeCharacteristic{value: []byte{3}, writeErr: failure}
			station := connectedFakeStation(&fakeCharacteristic{}, mode, nil, Capabilities{ChannelRead: true, ChannelWrite: true})
			result, err := SetChannel(station, 5)
			if !errors.Is(err, failure) {
				t.Fatalf("error = %v, lost original rejected write %v", err, failure)
			}
			if result.CommandSent || result.Channel != 3 || result.PreviousChannel != 3 {
				t.Fatalf("result = %+v, want unsent command and observed channel 3", result)
			}
			if RequiresReconnect(err) != RequiresReconnect(transportError("write", failure)) {
				t.Fatalf("error = %v, changed original recovery classification", err)
			}
		})
	}
}
