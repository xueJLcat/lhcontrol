package bluetooth

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
	tinybluetooth "tinygo.org/x/bluetooth"
)

func TestScanFindsServiceOnlyAdvertisement(t *testing.T) {
	originalAdapter := adapter
	fake := newFakeBLEAdapter()
	adapter = fake
	t.Cleanup(func() { adapter = originalAdapter })
	if err := Initialize(); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	mac, err := tinybluetooth.ParseMAC("11:22:33:44:55:66")
	if err != nil {
		t.Fatalf("ParseMAC() error = %v", err)
	}
	fake.results = []tinybluetooth.ScanResult{{
		Address: tinybluetooth.Address{MACAddress: tinybluetooth.MACAddress{MAC: mac}},
		AdvertisementPayload: &fakeAdvertisementPayload{
			services: []tinybluetooth.UUID{powerControlServiceUUID},
		},
	}}
	results, err := ScanForDuration(time.Millisecond)
	if err != nil {
		t.Fatalf("ScanForDuration() error = %v", err)
	}
	if len(results) != 1 || results[0].Name != "" {
		t.Fatalf("service-only scan results = %+v", results)
	}
}

// TestScanStartFailureBeatsConcurrentCancellation guards the cancel/start
// race: when the platform watcher never accepted a Start (for example the
// radio became unavailable) and a cancellation lands at the same moment, the
// adapter failure is the real outcome and must be reported so callers reach
// the adapter retry path instead of classifying the scan as cancelled.
func TestScanStartFailureBeatsConcurrentCancellation(t *testing.T) {
	originalAdapter := adapter
	fake := newFakeBLEAdapter()
	fake.startErr = tinybluetooth.ErrRadioNotAvailable
	fake.startDelay = make(chan struct{})
	adapter = fake
	t.Cleanup(func() { adapter = originalAdapter })
	if err := Initialize(); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	scanDone := make(chan error, 1)
	go func() {
		_, err := ScanForDurationContext(ctx, time.Second)
		scanDone <- err
	}()
	// Wait until the scan session is registered, then cancel before the
	// blocked platform start is released with its failure.
	registered := false
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); {
		activeScanMutex.Lock()
		registered = activeScan != nil
		activeScanMutex.Unlock()
		if registered {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if !registered {
		t.Fatal("scan session was not registered")
	}
	cancel()
	close(fake.startDelay)
	select {
	case err := <-scanDone:
		if errors.Is(err, ErrScanCancelled) {
			t.Fatalf("ScanForDurationContext() error = %v, want the adapter failure instead of a cancellation", err)
		}
		if !errors.Is(err, tinybluetooth.ErrRadioNotAvailable) {
			t.Fatalf("ScanForDurationContext() error = %v, want ErrRadioNotAvailable", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ScanForDurationContext() did not return after the start failure")
	}
}

func TestScanMergesNamesAcrossDuplicateAdvertisements(t *testing.T) {
	originalAdapter := adapter
	t.Cleanup(func() { adapter = originalAdapter })
	if err := Initialize(); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	mac, err := tinybluetooth.ParseMAC("11:22:33:44:55:67")
	if err != nil {
		t.Fatalf("ParseMAC() error = %v", err)
	}
	address := tinybluetooth.Address{MACAddress: tinybluetooth.MACAddress{MAC: mac}}
	for _, test := range []struct {
		name       string
		firstName  string
		secondName string
		wantName   string
	}{
		{name: "named then blank", firstName: "LHB-NAMED", wantName: "LHB-NAMED"},
		{name: "blank then named", secondName: "LHB-NAMED", wantName: "LHB-NAMED"},
		{name: "new non-empty name wins", firstName: "LHB-OLD", secondName: "LHB-NEW", wantName: "LHB-NEW"},
	} {
		t.Run(test.name, func(t *testing.T) {
			fake := newFakeBLEAdapter()
			adapter = fake
			fake.results = []tinybluetooth.ScanResult{
				{
					Address: address,
					AdvertisementPayload: &fakeAdvertisementPayload{
						name:     test.firstName,
						services: []tinybluetooth.UUID{powerControlServiceUUID},
					},
				},
				{
					Address: address,
					AdvertisementPayload: &fakeAdvertisementPayload{
						name:     test.secondName,
						services: []tinybluetooth.UUID{powerControlServiceUUID},
					},
				},
			}
			results, err := ScanForDuration(time.Millisecond)
			if err != nil {
				t.Fatalf("ScanForDuration() error = %v", err)
			}
			if len(results) != 1 || results[0].Name != test.wantName {
				t.Fatalf("duplicate scan results = %+v, want name %q", results, test.wantName)
			}
		})
	}
}
func TestScanRejectsPartialResultsOnEarlyAdapterFailure(t *testing.T) {
	originalAdapter := adapter
	adapterErr := errors.New("radio failure")
	fake := newFakeBLEAdapter()
	fake.scanErr = adapterErr
	adapter = fake
	t.Cleanup(func() { adapter = originalAdapter })
	if err := Initialize(); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	if _, err := ScanForDuration(time.Second); !errors.Is(err, adapterErr) {
		t.Fatalf("ScanForDuration() error = %v, want adapter failure", err)
	}
}
func TestScanSafelyConvertsPanicToError(t *testing.T) {
	originalAdapter := adapter
	fake := newFakeBLEAdapter()
	fake.panicScan = true
	adapter = fake
	t.Cleanup(func() { adapter = originalAdapter })
	if err := scanSafely(func(*tinybluetooth.Adapter, tinybluetooth.ScanResult) {}, func() {}); err == nil {
		t.Fatal("scanSafely() unexpectedly ignored panic")
	}
}
func TestStopScanSafelyConvertsPanicToError(t *testing.T) {
	originalAdapter := adapter
	fake := newFakeBLEAdapter()
	fake.panicStop = true
	adapter = fake
	t.Cleanup(func() { adapter = originalAdapter })
	if err := stopScanSafely(); err == nil {
		t.Fatal("stopScanSafely() unexpectedly ignored panic")
	}
}

// TestLateStopAfterWatcherEndedIsNotAStopFailure guards against a stop request
// that lands after the platform watcher already ended on its own: the adapter
// reports "no scan in progress", which is the desired end state and must not
// turn a full-duration scan into a failure.
func TestLateStopAfterWatcherEndedIsNotAStopFailure(t *testing.T) {
	originalAdapter := adapter
	fake := newFakeBLEAdapter()
	fake.stopErr = tinybluetooth.ErrNotScanning
	adapter = fake
	t.Cleanup(func() { adapter = originalAdapter })
	if err := Initialize(); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	mac, err := tinybluetooth.ParseMAC("11:22:33:44:55:70")
	if err != nil {
		t.Fatalf("ParseMAC() error = %v", err)
	}
	fake.results = []tinybluetooth.ScanResult{{
		Address: tinybluetooth.Address{MACAddress: tinybluetooth.MACAddress{MAC: mac}},
		AdvertisementPayload: &fakeAdvertisementPayload{
			name:     "LHB-LATE-STOP",
			services: []tinybluetooth.UUID{powerControlServiceUUID},
		},
	}}
	results, err := ScanForDuration(10 * time.Millisecond)
	if err != nil {
		t.Fatalf("ScanForDuration() error = %v, want the late stop treated as success", err)
	}
	if len(results) != 1 || results[0].Name != "LHB-LATE-STOP" {
		t.Fatalf("late-stop scan results = %+v", results)
	}
}

// TestScanKeepsResultsWhenFirstStopFailsButHandshakeRecovers guards the
// session/adapter reconciliation: the session records the first watcher.Stop()
// failure, but when the adapter-level handshake still reaches a clean finish
// (its own retry repaired the stop), the completed duration scan must keep its
// results instead of reporting a stop failure.
func TestScanKeepsResultsWhenFirstStopFailsButHandshakeRecovers(t *testing.T) {
	originalAdapter := adapter
	fake := newFakeBLEAdapter()
	fake.stopErr = errors.New("transient stop failure")
	adapter = fake
	t.Cleanup(func() { adapter = originalAdapter })
	if err := Initialize(); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	mac, err := tinybluetooth.ParseMAC("11:22:33:44:55:71")
	if err != nil {
		t.Fatalf("ParseMAC() error = %v", err)
	}
	fake.results = []tinybluetooth.ScanResult{{
		Address: tinybluetooth.Address{MACAddress: tinybluetooth.MACAddress{MAC: mac}},
		AdvertisementPayload: &fakeAdvertisementPayload{
			name:     "LHB-RECOVERED-STOP",
			services: []tinybluetooth.UUID{powerControlServiceUUID},
		},
	}}
	results, err := ScanForDuration(10 * time.Millisecond)
	if err != nil {
		t.Fatalf("ScanForDuration() error = %v, want results kept after the adapter recovered the stop", err)
	}
	if len(results) != 1 || results[0].Name != "LHB-RECOVERED-STOP" {
		t.Fatalf("recovered-stop scan results = %+v", results)
	}
}

// TestScanKeepsResultsWhenFirstStopFailsAndPlatformDrainsSlowly covers the
// wedge variant where the first watcher.Stop() fails and the platform Scan
// call only returns after the adapter-level retry and drain — later than the
// short abandonment grace. A duration scan keeps its results there: the
// duration stop gets the stop-handshake budget instead of the abandonment
// grace, so the late clean finish clears the stale first-stop error instead
// of discarding a completed scan's discovery results.
func TestScanKeepsResultsWhenFirstStopFailsAndPlatformDrainsSlowly(t *testing.T) {
	originalAdapter := adapter
	originalWait := scanStopWaitLimit
	originalGrace := scanAbandonGrace
	fake := newFakeBLEAdapter()
	fake.stopErr = errors.New("transient first stop failure")
	fake.releaseOn = make(chan struct{})
	adapter = fake
	scanStopWaitLimit = 2 * time.Second
	scanAbandonGrace = 50 * time.Millisecond
	t.Cleanup(func() {
		adapter = originalAdapter
		scanStopWaitLimit = originalWait
		scanAbandonGrace = originalGrace
	})
	if err := Initialize(); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	mac, err := tinybluetooth.ParseMAC("11:22:33:44:55:72")
	if err != nil {
		t.Fatalf("ParseMAC() error = %v", err)
	}
	fake.results = []tinybluetooth.ScanResult{{
		Address: tinybluetooth.Address{MACAddress: tinybluetooth.MACAddress{MAC: mac}},
		AdvertisementPayload: &fakeAdvertisementPayload{
			name:     "LHB-SLOW-DRAIN",
			services: []tinybluetooth.UUID{powerControlServiceUUID},
		},
	}}
	type scanOutcome struct {
		stations []DiscoveredStation
		err      error
	}
	outcome := make(chan scanOutcome, 1)
	go func() {
		stations, scanErr := ScanForDuration(20 * time.Millisecond)
		outcome <- scanOutcome{stations, scanErr}
	}()
	select {
	case <-fake.stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("duration stop was never issued")
	}
	// Release the platform Scan call after the abandonment grace but inside
	// the duration stop's stop-handshake budget.
	time.Sleep(150 * time.Millisecond)
	close(fake.releaseOn)
	select {
	case got := <-outcome:
		if got.err != nil {
			t.Fatalf("ScanForDuration() error = %v, want results kept after the slow platform drain", got.err)
		}
		if len(got.stations) != 1 || got.stations[0].Name != "LHB-SLOW-DRAIN" {
			t.Fatalf("slow-drain scan results = %+v", got.stations)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("slow-drain scan did not finish")
	}
}

// TestScanForDurationContextReportsDeadlineAsTimeout guards the timeout
// classification: a caller budget expiring mid-scan is a timeout, not a user
// cancellation, even though the context watcher records the resulting stop
// as a cancellation.
func TestScanForDurationContextReportsDeadlineAsTimeout(t *testing.T) {
	originalAdapter := adapter
	fake := newFakeBLEAdapter()
	adapter = fake
	t.Cleanup(func() { adapter = originalAdapter })
	if err := Initialize(); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := ScanForDurationContext(ctx, time.Hour)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("ScanForDurationContext() error = %v, want a deadline timeout", err)
	}
	if errors.Is(err, ErrScanCancelled) {
		t.Fatalf("deadline scan misclassified as cancelled: %v", err)
	}
}

// TestCancelScanReportsCancellationWhenFirstStopFails guards the same
// reconciliation for cancellations: a stale first-stop failure must not turn
// a clean cancellation (the adapter handshake recovered) into a stop-failure
// error.
func TestCancelScanReportsCancellationWhenFirstStopFails(t *testing.T) {
	originalAdapter := adapter
	fake := newFakeBLEAdapter()
	fake.stopErr = errors.New("transient stop failure")
	adapter = fake
	t.Cleanup(func() { adapter = originalAdapter })
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := ScanForDurationContext(ctx, time.Hour)
		result <- err
	}()
	select {
	case <-fake.started:
	case <-time.After(time.Second):
		t.Fatal("scan did not start")
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, ErrScanCancelled) {
			t.Fatalf("ScanForDuration() after cancellation error = %v, want ErrScanCancelled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("active scan did not stop after cancellation")
	}
}
func TestScanForDurationRepeatedLifecycle(t *testing.T) {
	originalAdapter := adapter
	t.Cleanup(func() { adapter = originalAdapter })
	for cycle := 0; cycle < 100; cycle++ {
		adapter = newFakeBLEAdapter()
		if _, err := ScanForDuration(time.Millisecond); err != nil {
			t.Fatalf("scan cycle %d error = %v", cycle+1, err)
		}
	}
}
func TestCancelScanStopsActiveScan(t *testing.T) {
	originalAdapter := adapter
	fake := newFakeBLEAdapter()
	adapter = fake
	t.Cleanup(func() { adapter = originalAdapter })
	result := make(chan error, 1)
	go func() {
		_, err := ScanForDuration(time.Hour)
		result <- err
	}()
	select {
	case <-fake.started:
	case <-time.After(time.Second):
		t.Fatal("scan did not start")
	}
	if err := CancelScan(); err != nil {
		t.Fatalf("CancelScan() error = %v", err)
	}
	select {
	case err := <-result:
		if !errors.Is(err, ErrScanCancelled) {
			t.Fatalf("ScanForDuration() after cancellation error = %v, want ErrScanCancelled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("active scan did not stop after cancellation")
	}
}
func TestScanForDurationContextStopsActiveScan(t *testing.T) {
	originalAdapter := adapter
	fake := newFakeBLEAdapter()
	adapter = fake
	t.Cleanup(func() { adapter = originalAdapter })
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := ScanForDurationContext(ctx, time.Hour)
		result <- err
	}()
	<-fake.started
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, ErrScanCancelled) {
			t.Fatalf("ScanForDurationContext() error = %v, want ErrScanCancelled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("context cancellation did not stop active scan")
	}
}
func TestScanForDurationContextKeepsResultsWhenCancelledDuringStopHandshake(t *testing.T) {
	originalAdapter := adapter
	fake := newFakeBLEAdapter()
	fake.stopHold = make(chan struct{})
	adapter = fake
	t.Cleanup(func() { adapter = originalAdapter })
	if err := Initialize(); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	mac, err := tinybluetooth.ParseMAC("11:22:33:44:55:68")
	if err != nil {
		t.Fatalf("ParseMAC() error = %v", err)
	}
	fake.results = []tinybluetooth.ScanResult{{
		Address: tinybluetooth.Address{MACAddress: tinybluetooth.MACAddress{MAC: mac}},
		AdvertisementPayload: &fakeAdvertisementPayload{
			name:     "LHB-LATE",
			services: []tinybluetooth.UUID{powerControlServiceUUID},
		},
	}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type scanOutcome struct {
		results []DiscoveredStation
		err     error
	}
	outcome := make(chan scanOutcome, 1)
	go func() {
		results, scanErr := ScanForDurationContext(ctx, 10*time.Millisecond)
		outcome <- scanOutcome{results, scanErr}
	}()
	// Wait until the duration timer has issued the stop and the fake
	// StopScan is inside its hold window.
	deadline := time.Now().Add(2 * time.Second)
	for fake.stopCalls.Load() == 0 {
		if time.Now().After(deadline) {
			close(fake.stopHold)
			t.Fatal("duration stop was never issued")
		}
		time.Sleep(time.Millisecond)
	}
	// A cancellation landing in the stop-handshake window must not
	// discard the stations discovered during the completed duration.
	cancel()
	close(fake.stopHold)
	select {
	case got := <-outcome:
		if got.err != nil {
			t.Fatalf("ScanForDurationContext() error = %v, want completed results", got.err)
		}
		if len(got.results) != 1 || got.results[0].Name != "LHB-LATE" {
			t.Fatalf("scan results = %+v, want the discovered station", got.results)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("scan did not return after the stop handshake")
	}
}
func TestScanForDurationContextCancelledScanWinsOverAdapterError(t *testing.T) {
	originalAdapter := adapter
	adapterErr := errors.New("radio dropped mid-scan")
	fake := newFakeBLEAdapter()
	fake.scanErr = adapterErr
	fake.startDelay = make(chan struct{})
	adapter = fake
	t.Cleanup(func() { adapter = originalAdapter })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, err := ScanForDurationContext(ctx, time.Hour)
		result <- err
	}()
	deadline := time.Now().Add(time.Second)
	for {
		activeScanMutex.Lock()
		active := activeScan != nil
		activeScanMutex.Unlock()
		if active {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("scan session was not registered")
		}
		time.Sleep(time.Millisecond)
	}
	// Record the cancellation before the watcher is allowed to start so the
	// session has reason=scanStopCancelled while the adapter independently
	// fails: the requested stop must keep its classification instead of being
	// shadowed by the adapter error.
	cancel()
	close(fake.startDelay)
	select {
	case err := <-result:
		if !errors.Is(err, ErrScanCancelled) {
			t.Fatalf("ScanForDurationContext() error = %v, want ErrScanCancelled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled scan did not return after adapter failure")
	}
}
func TestScanForDurationContextDoesNotStartWithCancelledContext(t *testing.T) {
	originalAdapter := adapter
	fake := newFakeBLEAdapter()
	adapter = fake
	t.Cleanup(func() { adapter = originalAdapter })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ScanForDurationContext(ctx, time.Hour); !errors.Is(err, ErrScanCancelled) {
		t.Fatalf("ScanForDurationContext() error = %v, want ErrScanCancelled", err)
	}
	select {
	case <-fake.started:
		t.Fatal("adapter scan started with an already-cancelled context")
	default:
	}
}
func TestRequestScanCancellationBeforeWatcherStartIsNonBlocking(t *testing.T) {
	originalAdapter := adapter
	fake := newFakeBLEAdapter()
	fake.startDelay = make(chan struct{})
	adapter = fake
	t.Cleanup(func() { adapter = originalAdapter })
	result := make(chan error, 1)
	go func() {
		_, err := ScanForDuration(time.Hour)
		result <- err
	}()
	deadline := time.Now().Add(time.Second)
	for {
		activeScanMutex.Lock()
		active := activeScan != nil
		activeScanMutex.Unlock()
		if active {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("scan session was not registered")
		}
		time.Sleep(time.Millisecond)
	}
	returned := make(chan struct{})
	go func() {
		RequestScanCancellation()
		close(returned)
	}()
	select {
	case <-returned:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("cancellation request blocked before watcher startup")
	}
	if got := fake.stopCalls.Load(); got != 0 {
		t.Fatalf("StopScan calls before watcher start = %d, want 0", got)
	}
	close(fake.startDelay)
	select {
	case err := <-result:
		if !errors.Is(err, ErrScanCancelled) {
			t.Fatalf("scan error = %v, want ErrScanCancelled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("scan did not exit after watcher startup")
	}
	if got := fake.stopCalls.Load(); got != 1 {
		t.Fatalf("StopScan calls = %d, want 1", got)
	}
}
func TestCancelScanBeforeWatcherStartReturnsWithoutWaiting(t *testing.T) {
	originalAdapter := adapter
	fake := newFakeBLEAdapter()
	fake.startDelay = make(chan struct{})
	adapter = fake
	t.Cleanup(func() { adapter = originalAdapter })
	result := make(chan error, 1)
	go func() {
		_, err := ScanForDuration(time.Hour)
		result <- err
	}()
	deadline := time.Now().Add(time.Second)
	for {
		activeScanMutex.Lock()
		active := activeScan != nil
		activeScanMutex.Unlock()
		if active {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("scan session was not registered")
		}
		time.Sleep(time.Millisecond)
	}
	cancelled := make(chan error, 1)
	go func() { cancelled <- CancelScan() }()
	select {
	case err := <-cancelled:
		if err != nil {
			t.Fatalf("CancelScan() error = %v", err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("CancelScan blocked before watcher startup")
	}
	close(fake.startDelay)
	select {
	case err := <-result:
		if !errors.Is(err, ErrScanCancelled) {
			t.Fatalf("scan error = %v, want ErrScanCancelled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("scan did not exit after watcher startup")
	}
}
func TestScanRejectsEarlyGracefulReturn(t *testing.T) {
	originalAdapter := adapter
	fake := newFakeBLEAdapter()
	fake.returnEarly = true
	adapter = fake
	t.Cleanup(func() { adapter = originalAdapter })
	if _, err := ScanForDuration(time.Hour); err == nil || !strings.Contains(err.Error(), "before the requested duration") {
		t.Fatalf("ScanForDuration() error = %v, want early-stop error", err)
	}
	if got := fake.stopCalls.Load(); got != 0 {
		t.Fatalf("StopScan calls = %d, want 0", got)
	}
}
func TestScanTimerAndCancellationStopOnlyOnce(t *testing.T) {
	originalAdapter := adapter
	fake := newFakeBLEAdapter()
	adapter = fake
	t.Cleanup(func() { adapter = originalAdapter })
	result := make(chan error, 1)
	go func() {
		_, err := ScanForDuration(10 * time.Millisecond)
		result <- err
	}()
	<-fake.started
	time.Sleep(10 * time.Millisecond)
	_ = CancelScan()
	<-result
	if got := fake.stopCalls.Load(); got != 1 {
		t.Fatalf("StopScan calls = %d, want 1", got)
	}
}
func TestScanDurationStartsAfterWatcherReportsStarted(t *testing.T) {
	originalAdapter := adapter
	fake := newFakeBLEAdapter()
	fake.startDelay = make(chan struct{})
	adapter = fake
	t.Cleanup(func() { adapter = originalAdapter })
	result := make(chan error, 1)
	go func() {
		_, err := ScanForDuration(20 * time.Millisecond)
		result <- err
	}()
	time.Sleep(50 * time.Millisecond)
	if got := fake.stopCalls.Load(); got != 0 {
		t.Fatalf("StopScan calls before watcher start = %d, want 0", got)
	}
	close(fake.startDelay)
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("ScanForDuration() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("scan did not stop after its post-start duration")
	}
	if got := fake.stopCalls.Load(); got != 1 {
		t.Fatalf("StopScan calls = %d, want 1", got)
	}
}

// TestScanForDurationAbandonsHungStopAndKeepsResults guards against a WinRT
// StopScan that never returns (radio removed mid-scan): the scan must finish
// within its bounded stop budget, keep the discovery results from the
// completed duration, and leave the scan slot free for the next scan.
func TestScanForDurationAbandonsHungStopAndKeepsResults(t *testing.T) {
	originalAdapter := adapter
	originalWait := scanStopWaitLimit
	fake := newFakeBLEAdapter()
	fake.stopHold = make(chan struct{}) // never closed: StopScan hangs
	fake.releaseOn = make(chan struct{})
	adapter = fake
	scanStopWaitLimit = 50 * time.Millisecond
	t.Cleanup(func() {
		adapter = originalAdapter
		scanStopWaitLimit = originalWait
	})
	if err := Initialize(); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	mac, err := tinybluetooth.ParseMAC("11:22:33:44:55:6B")
	if err != nil {
		t.Fatalf("ParseMAC() error = %v", err)
	}
	fake.results = []tinybluetooth.ScanResult{{
		Address: tinybluetooth.Address{MACAddress: tinybluetooth.MACAddress{MAC: mac}},
		AdvertisementPayload: &fakeAdvertisementPayload{
			name:     "LHB-HUNG-STOP",
			services: []tinybluetooth.UUID{powerControlServiceUUID},
		},
	}}
	started := time.Now()
	outcome := make(chan error, 1)
	var results []DiscoveredStation
	go func() {
		var scanErr error
		results, scanErr = ScanForDuration(10 * time.Millisecond)
		outcome <- scanErr
	}()
	deadline := time.Now().Add(2 * time.Second)
	for fake.stopCalls.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("duration stop was never issued")
		}
		time.Sleep(time.Millisecond)
	}
	// The platform Scan call returns on its own budget while StopScan hangs.
	close(fake.releaseOn)
	select {
	case scanErr := <-outcome:
		if scanErr != nil {
			t.Fatalf("ScanForDuration() error = %v, want abandoned stop to keep results", scanErr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("scan did not return after the bounded stop budget")
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("scan took %v, want a bounded stop wait", elapsed)
	}
	if len(results) != 1 || results[0].Name != "LHB-HUNG-STOP" {
		t.Fatalf("scan results = %+v, want the discovered station", results)
	}
	activeScanMutex.Lock()
	stillRegistered := activeScan != nil
	activeScanMutex.Unlock()
	if stillRegistered {
		t.Fatal("abandoned stop left the scan session registered")
	}
}

// TestScanForDurationContextCancelButtonAbandonsHungStop covers the same hung
// StopScan for a user cancellation: the scan reports ErrScanCancelled within
// the bounded budget instead of blocking forever.
func TestScanForDurationContextCancelButtonAbandonsHungStop(t *testing.T) {
	originalAdapter := adapter
	originalWait := scanStopWaitLimit
	fake := newFakeBLEAdapter()
	fake.stopHold = make(chan struct{}) // never closed: StopScan hangs
	fake.releaseOn = make(chan struct{})
	adapter = fake
	scanStopWaitLimit = 50 * time.Millisecond
	t.Cleanup(func() {
		adapter = originalAdapter
		scanStopWaitLimit = originalWait
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, err := ScanForDurationContext(ctx, time.Hour)
		result <- err
	}()
	select {
	case <-fake.started:
	case <-time.After(time.Second):
		t.Fatal("scan did not start")
	}
	cancel()
	deadline := time.Now().Add(2 * time.Second)
	for fake.stopCalls.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("cancellation stop was never issued")
		}
		time.Sleep(time.Millisecond)
	}
	close(fake.releaseOn)
	select {
	case err := <-result:
		if !errors.Is(err, ErrScanCancelled) {
			t.Fatalf("ScanForDurationContext() error = %v, want ErrScanCancelled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancelled scan did not return after the bounded stop budget")
	}
}

// TestScanAbandonsWedgedPlatformScanAndReleasesSlot covers the worst-case
// wedge: watcher.Stop() never returns (radio removed mid-scan), which keeps
// both StopScan and the platform Scan call blocked. The cancellation must
// still settle within the bounded stop budget plus the abandon grace, and the
// active-scan slot must be released so the next scan can run instead of every
// scan failing with "scan is already active" until a process restart.
func TestScanAbandonsWedgedPlatformScanAndReleasesSlot(t *testing.T) {
	originalAdapter := adapter
	originalWait := scanStopWaitLimit
	originalGrace := scanAbandonGrace
	fake := newFakeBLEAdapter()
	fake.stopHold = make(chan struct{}) // never closed: StopScan hangs, Scan hangs with it
	adapter = fake
	scanStopWaitLimit = 50 * time.Millisecond
	scanAbandonGrace = 50 * time.Millisecond
	t.Cleanup(func() {
		adapter = originalAdapter
		scanStopWaitLimit = originalWait
		scanAbandonGrace = originalGrace
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, err := ScanForDurationContext(ctx, time.Hour)
		result <- err
	}()
	select {
	case <-fake.started:
	case <-time.After(time.Second):
		t.Fatal("scan did not start")
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, ErrScanCancelled) {
			t.Fatalf("ScanForDurationContext() error = %v, want ErrScanCancelled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("wedged platform scan did not return within the bounded budget")
	}
	activeScanMutex.Lock()
	stillRegistered := activeScan != nil
	activeScanMutex.Unlock()
	if stillRegistered {
		t.Fatal("abandoned platform scan left the scan session registered")
	}
	// Release the wedged stop and wait for the fake StopScan to finish so the
	// abandoned platform goroutine has stopped reading the adapter variable
	// before it is swapped for the next scan.
	close(fake.stopHold)
	select {
	case <-fake.stopped:
	case <-time.After(time.Second):
		t.Fatal("wedged StopScan did not finish after release")
	}
	// The released slot must let the next scan run to completion.
	adapter = newFakeBLEAdapter()
	if _, err := ScanForDuration(time.Millisecond); err != nil {
		t.Fatalf("scan after an abandoned platform scan error = %v", err)
	}
}

func TestScanSessionStopAfterFinishedDoesNotRecordReason(t *testing.T) {
	// A stop request that arrives after the scan ended on its own must not
	// record a stop reason: doing so would misclassify a natural finish as a
	// duration/cancel stop and, via the duration latch, report an early stop
	// as if the full duration had run.
	session := newScanSession()
	session.markStarted()
	session.markFinished()
	session.requestStopAsync(scanStopDuration)
	if session.durationStopIssuedFlag() {
		t.Fatal("stop request after finish latched the duration reason")
	}
	if got := session.stopReason(); got != scanStopNone {
		t.Fatalf("stop reason after finish = %v, want none", got)
	}
}

// TestScanSessionPlatformOutcomeBlocksStopReason guards the duration-timer
// race: once the platform Scan call has delivered its outcome (typically a
// radio removed mid-scan), a duration or cancellation stop that lands before
// markFinished must not record a stop reason or latch the duration flag.
// Latching would reclassify the real platform failure as a completed duration
// scan and keep the adapter-unavailable handling downstream from ever seeing
// it.
func TestScanSessionPlatformOutcomeBlocksStopReason(t *testing.T) {
	session := newScanSession()
	session.markStarted()
	session.markPlatformDone()
	session.requestStopAsync(scanStopDuration)
	if session.durationStopIssuedFlag() {
		t.Fatal("duration stop after the platform outcome latched the duration reason")
	}
	if got := session.stopReason(); got != scanStopNone {
		t.Fatalf("stop reason after the platform outcome = %v, want none", got)
	}
	session.requestStopAsync(scanStopCancelled)
	if got := session.stopReason(); got != scanStopNone {
		t.Fatalf("cancellation after the platform outcome = %v, want none", got)
	}
}

// TestScanMidScanFailureNotMaskedByDurationResults covers a platform failure
// (a radio removed mid-scan) that lands while discovery results were already
// collected: the failure is the real outcome and must be reported instead of
// being masked as a successful full-duration scan by the stop that the
// duration timer issues moments later.
func TestScanMidScanFailureNotMaskedByDurationResults(t *testing.T) {
	originalAdapter := adapter
	adapterErr := errors.New("radio removed mid-scan")
	fake := newFakeBLEAdapter()
	fake.releaseOn = make(chan struct{})
	fake.releaseErr = adapterErr
	adapter = fake
	t.Cleanup(func() { adapter = originalAdapter })
	if err := Initialize(); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	mac, err := tinybluetooth.ParseMAC("11:22:33:44:55:6C")
	if err != nil {
		t.Fatalf("ParseMAC() error = %v", err)
	}
	fake.results = []tinybluetooth.ScanResult{{
		Address: tinybluetooth.Address{MACAddress: tinybluetooth.MACAddress{MAC: mac}},
		AdvertisementPayload: &fakeAdvertisementPayload{
			name:     "LHB-MID-FAIL",
			services: []tinybluetooth.UUID{powerControlServiceUUID},
		},
	}}
	type scanOutcome struct {
		results []DiscoveredStation
		err     error
	}
	outcome := make(chan scanOutcome, 1)
	go func() {
		results, scanErr := ScanForDuration(time.Hour)
		outcome <- scanOutcome{results, scanErr}
	}()
	// Let the scan start and deliver its discovery results, then fail the
	// platform call as a radio removal mid-scan.
	select {
	case <-fake.started:
	case <-time.After(time.Second):
		t.Fatal("scan did not start")
	}
	close(fake.releaseOn)
	select {
	case got := <-outcome:
		if !errors.Is(got.err, adapterErr) {
			t.Fatalf("ScanForDuration() error = %v, want the mid-scan adapter failure", got.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("scan did not return after the mid-scan failure")
	}
}

// TestScanAbandonsWedgedScanStartAndReleasesSlot covers a platform watcher
// whose start sequence never completes (radio removed or driver wedged before
// Start). No stop budget applies because nothing ever started, yet the scan
// must settle within the bounded start budget and release the active-scan slot
// instead of blocking every later scan until a process restart.
func TestScanAbandonsWedgedScanStartAndReleasesSlot(t *testing.T) {
	originalAdapter := adapter
	originalStartWait := scanStartWaitLimit
	fake := newFakeBLEAdapter()
	fake.startDelay = make(chan struct{}) // never closed: the watcher Start hangs
	adapter = fake
	scanStartWaitLimit = 50 * time.Millisecond
	t.Cleanup(func() {
		adapter = originalAdapter
		scanStartWaitLimit = originalStartWait
	})
	result := make(chan error, 1)
	go func() {
		_, err := ScanForDuration(time.Hour)
		result <- err
	}()
	select {
	case err := <-result:
		if !isScanStartTimeout(err) {
			t.Fatalf("ScanForDuration() error = %v, want a scan start timeout", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("wedged scan start did not return within the bounded budget")
	}
	activeScanMutex.Lock()
	stillRegistered := activeScan != nil
	activeScanMutex.Unlock()
	if stillRegistered {
		t.Fatal("abandoned scan start left the scan session registered")
	}
	// Release the wedged start and let the abandoned platform goroutine finish
	// so it stops touching the fake adapter before it is swapped.
	close(fake.startDelay)
	select {
	case <-fake.started:
	case <-time.After(time.Second):
		t.Fatal("abandoned platform scan never reported its late start")
	}
	// The late start must trigger the recorded stop intent: without it the
	// abandoned watcher keeps scanning and blocks every later scan. Wait for
	// the stop to land on the abandoned session's adapter before swapping it.
	select {
	case <-fake.stopped:
	case <-time.After(time.Second):
		t.Fatal("late platform start was not stopped by the abandoned session")
	}
	if calls := fake.stopCalls.Load(); calls == 0 {
		t.Fatal("abandoned session never called StopScan for the late start")
	}
	// The released slot must let the next scan run to completion.
	adapter = newFakeBLEAdapter()
	if _, err := ScanForDuration(time.Millisecond); err != nil {
		t.Fatalf("scan after an abandoned scan start error = %v", err)
	}
}
