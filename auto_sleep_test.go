package main

import (
	"context"
	"testing"

	"lhcontrol/internal/station"
)

func TestRunAutoSleepEmitsTerminalCancellationBetweenScanAndPower(t *testing.T) {
	app := NewApp()
	ctx, cancel := context.WithCancel(context.Background())
	var events []autoSleepEvent
	app.autoSleepEventSink = func(event autoSleepEvent) {
		events = append(events, event)
	}
	app.scanForAutoSleep = func(context.Context) ([]station.StationInfo, error) {
		cancel()
		return nil, nil
	}
	app.setPowerForAutoSleep = func(context.Context, string) (station.BulkPowerResult, error) {
		t.Fatal("bulk power started after automatic sleep was cancelled")
		return station.BulkPowerResult{}, nil
	}

	app.runAutoSleep(ctx)

	if len(events) != 2 || events[0].Phase != "started" || events[1].Phase != "cancelled" {
		t.Fatalf("auto-sleep events = %+v, want started then cancelled", events)
	}
	if events[1].Error != "cancelled after scanning and before power commands were sent" {
		t.Fatalf("cancellation reason = %q", events[1].Error)
	}
}
