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
