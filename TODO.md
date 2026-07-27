# TODO

Last reviewed: 2026-07-27

The original review fixes are still present in the current local history; the
working tree was clean when the follow-up audit started, and local `main` was
18 commits ahead of `origin/main`. The items below are residual defects or
follow-up regressions found after those fixes, not evidence of a wholesale code
rollback.

## High priority

### Reconnect after a channel-only transport failure

Status: Pending

Problem:

- `recordStructuredReadResult` disconnects a station when the power read fails,
  but a channel-only failure merely schedules another channel retry.
- When the channel failure is a transport or GATT communication error, the
  cached mode characteristic can be invalid even though the power
  characteristic and device still appear connected.
- `connectAndDiscoverInternal` treats that connection as healthy when the power
  characteristic is present, so channel recovery can repeatedly reuse the
  invalid mode characteristic without rediscovering GATT services.
- The channel can remain unknown or stale indefinitely, which blocks safe
  conflict detection and channel changes.

Relevant code:

- `internal/station/manager.go`: `recordStructuredReadResult` and channel status
  recovery scheduling.
- `internal/bluetooth/bluetooth.go`: `RequiresReconnect`,
  `connectAndDiscoverInternal`, and `FetchInitialPowerState`.
- `internal/station/manager_test.go`: structured power/channel recovery tests.

Proposed solution:

1. In the channel-error branch of `recordStructuredReadResult`, disconnect the
   station when `bluetooth.RequiresReconnect(channelErr)` or
   `bluetooth.IsAdapterUnavailable(channelErr)` is true.
2. Continue to schedule the independent channel retry so the next recovery
   attempt reconnects and rediscovers the mode characteristic.
3. Preserve the existing connection for explicit unsupported-capability errors
   and other channel errors that do not invalidate cached GATT handles.
4. Keep power and channel retry accounting independent; a successful power read
   must not turn a channel transport failure into a connection retry failure.

Acceptance criteria:

- A successful power read plus channel `DeviceTransportError` disconnects the
  stale station and leaves a channel retry scheduled.
- The next recovery attempt creates or discovers fresh GATT handles and can
  clear the channel retry after a successful read.
- `ErrAttReadNotPermitted`, `ErrAttRequestNotSupported`, and equivalent explicit
  unsupported errors retain a healthy connection and do not retry forever.
- Tests cover channel-only GATT communication failure, adapter loss, unsupported
  channel reads, and successful recovery after reconnect.

### Serialize spontaneous disconnect cleanup before reconnect

Status: Pending

Problem:

- The Windows connection-status callback starts `Device.Disconnect()` and the
  application connection handler in separate goroutines with no ordering.
- The application handler can clear `BaseStation.device`, remove the station
  from `connectedStations`, and allow a replacement connection before the old
  WinRT device and GATT session have finished cleanup.
- The stale-callback identity check prevents an old callback from clearing a
  replacement device, but it does not prevent overlapping old and new sessions.
- Unlike explicit disconnect failures, this path does not retain the old device
  in `pendingCleanup`.

Relevant code:

- `third_party/tinygo-bluetooth/gap_windows.go`: connection-status callback and
  asynchronous `Device.Disconnect()` dispatch.
- `internal/bluetooth/bluetooth.go`: `handleAdapterConnectionChange`,
  `invalidateDisconnectedDevice`, `pendingCleanup`, and reconnect logic.

Proposed solution:

1. Make spontaneous-disconnect notification carry a cleanup completion result,
   or expose a connection-generation cleanup barrier to the application layer.
2. Immediately mark the matching station disconnected, but retain the old
   device as pending cleanup until `Device.Disconnect()` finishes.
3. Block replacement connection creation while cleanup for the same address and
   connection generation is incomplete.
4. Preserve the current device-identity/generation check so a late old callback
   cannot invalidate a replacement connection.
5. Route retryable cleanup failures through the existing cleanup retry path
   instead of dropping ownership.

Acceptance criteria:

- A spontaneous disconnect cannot overlap cleanup of the old GATT session with
  creation of a replacement session for the same station.
- Station state becomes disconnected promptly, but reconnect waits for cleanup
  completion or returns a structured busy/cleanup error.
- A delayed callback from an old connection cannot clear a newer connection.
- Tests cover cleanup blocked in progress, cleanup failure and retry, and a
  reconnect request racing with the disconnect callback.

### Restore the full StopScan completion barrier without callback deadlocks

Status: Pending

Problem:

- `finishScan` currently closes the scan lifecycle channel before invoking the
  `Cancelled`, `Failed`, or `Completed` callback.
- `StopScan` can therefore return before terminal event publication finishes,
  contrary to its method comment and the HTTP API documentation.
- The early close was introduced so a terminal callback can call `StopScan`
  without waiting for itself, but it permits Wails or HTTP callers to start
  subsequent work while terminal callback work is still running.
- `beginScan` also invokes `Started` while holding `scanTransitionMutex` and
  before the asynchronous scan goroutine starts. A `Started` callback calling
  `StopScan` can deadlock permanently.
- Terminal callbacks run while `scanTransitionMutex` is held, so synchronously
  starting another scan from a terminal callback can also deadlock.

Relevant code:

- `internal/station/manager.go`: `beginScan`, `finishScan`,
  `completeScanLifecycle`, `StartScan`, and `StopScan`.
- `internal/station/manager_test.go`: callback ordering and reentry tests.
- `app.go`: scan callbacks that emit Wails events.

Proposed solution:

1. Split scan completion into two explicit phases: Bluetooth workflow terminal
   state and terminal callback/event completion.
2. Publish `isScanning=false`, terminal status, and release the Bluetooth
   operation lock before callbacks, but close the public `StopScan` completion
   barrier only after callbacks finish.
3. Move user callbacks outside `scanTransitionMutex`; use an operation identity
   or generation to serialize lifecycle publication without holding the mutex
   across arbitrary callback code.
4. Make callback reentry explicit: `StopScan` called from the same terminal
   callback should be an idempotent no-op, while external callers still wait for
   callback completion.
5. Start the asynchronous scan worker before exposing a `Started` callback, or
   dispatch `Started` outside lifecycle locks after cancellation can be serviced.
6. Define whether terminal callbacks may synchronously start the next scan and
   enforce that contract in tests.

Acceptance criteria:

- External `StopScan` callers return only after exactly one terminal callback or
  event has completed.
- `Started -> StopScan`, `Cancelled -> StopScan`, and terminal callback ->
  `StartScan` do not deadlock.
- Concurrent and repeated `StopScan` calls share one completion barrier.
- An operation started immediately after Wails or HTTP `StopScan` returns cannot
  overlap terminal callback work from the previous scan.
- Callback panic handling still closes the lifecycle and releases all locks.

### Preserve explicit definitely-not-sent write classification

Status: Pending

Problem:

- The Windows transport marks failures after asynchronous
  `WriteWithoutResponse` operation creation as possibly sent and leaves
  operation-creation failures unmarked because no command was submitted.
- The application layer currently converts every unclassified non-ATT
  `WriteWithoutResponse` error into `PossiblySentError`.
- A synchronous operation-creation failure is therefore incorrectly treated as
  ambiguous, preventing a safe reconnect/retry and reporting a false
  command-may-have-been-sent result.

Relevant code:

- `third_party/tinygo-bluetooth/gattc_windows.go`: asynchronous operation
  creation, `WritePossiblySentError`, and ATT result handling.
- `internal/bluetooth/bluetooth.go`:
  `writeCharacteristicValueInternal`, `possiblySentClassification`, power,
  Sleep, Identify, and channel write handling.
- `third_party/tinygo-bluetooth/gattc_windows_test.go` and
  `internal/bluetooth/operations_test.go`: write classification tests.

Proposed solution:

1. Add an explicit transport classification contract, for example
   `DeliveryState()`, `DefinitelyNotSent()`, or a shared typed write error with
   `NotSent`, `PossiblySent`, and `Rejected` states.
2. Mark failures that occur before WinRT returns an async operation as
   definitely not sent.
3. Mark completion timeout, cancellation, result retrieval failure, and
   communication failure after operation creation as possibly sent.
4. Keep explicit ATT rejection precise and non-ambiguous.
5. Make the application trust the explicit transport classification instead of
   inferring ambiguity from the absence of an ATT error.

Acceptance criteria:

- Operation-creation failure can use the existing safe retry path.
- Failure after async operation creation never causes a second write.
- ATT protocol errors retain their exact `AttributeProtocolError` and capability
  downgrade behavior.
- Sleep never sends the final command after an ambiguous prepare write and never
  replays either write after an ambiguous result.
- Identify and channel writes preserve their current non-replay guarantees.

### Retain COM ownership when connection registration cleanup is retryable

Status: Pending

Problem:

- The connection-registration failure path now has one COM owner and avoids the
  original double release.
- If `Device.Disconnect()` cannot initialize its WinRT cleanup thread, cleanup
  is deliberately marked retryable, but the registration failure path discards
  the cleanup error and returns an empty `Device`.
- The last owner can therefore be lost while still holding the Bluetooth device,
  GATT session, and event handler.

Relevant code:

- `third_party/tinygo-bluetooth/gap_windows.go`: ownership transfer before
  `AddConnectionStatusChanged`, registration failure, `Device.Disconnect`, and
  retryable `deviceState.cleanup`.

Proposed solution:

1. Keep construction ownership local until event registration succeeds, and on
   registration failure perform a single synchronous local unwind that disables
   `MaintainConnection`, closes, and releases each object exactly once.
2. Alternatively, retain a dedicated cleanup owner when transferring ownership
   before registration and do not discard it until cleanup reaches a terminal
   state.
3. Preserve both the registration error and cleanup error with `errors.Join`.
4. Do not return an externally usable connected `Device` after failed event
   registration.

Acceptance criteria:

- Device, session, and handler are released exactly once on every registration
  failure path.
- WinRT cleanup initialization failure does not leak ownership.
- The returned error includes both registration and cleanup failures where
  applicable.
- Tests inject registration failure and retryable cleanup failure and verify
  final ownership counts.

### Make active Bluetooth operations cancellable

Status: Pending

Problem:

- Scan cancellation prevents queued station initialization work from starting,
  but it cannot interrupt a GATT connection, service discovery, or read that
  has already entered WinRT.
- `StopScan` and application shutdown wait for the active scan workflow to
  cross its completion barrier. A stalled Bluetooth driver can therefore keep
  the UI in `Stopping...` and delay process exit until the underlying timeout
  expires.
- `Manager.Shutdown` waits for `activeOperations` without a timeout, so a WinRT
  call that never returns can prevent process exit indefinitely and retain the
  single-instance mutex and BLE session.
- Power confirmation and reconnect paths also use fixed `time.Sleep` calls that
  cannot react promptly to shutdown.
- The current operation function signatures do not accept a `context.Context`,
  so cancellation cannot propagate through the complete Bluetooth call chain.

Relevant code:

- `internal/station/manager.go`: scan initialization workers, `StopScan`,
  `BeginShutdown`, active-operation draining, and `Shutdown`.
- `internal/bluetooth/bluetooth.go`: connect, discovery, initial reads, and
  power confirmation/reconnect polling.
- `third_party/tinygo-bluetooth`: Windows asynchronous GATT operations.

Proposed solution:

1. Add context-aware variants for connect, service discovery, characteristic
   discovery, power reads, channel reads, and initial station reads.
2. Propagate the scan context from `Manager` into each station initialization
   worker and every underlying Bluetooth operation.
3. When cancellation is requested, stop waiting for WinRT completion without
   releasing COM objects or device handles while callbacks can still access
   them.
4. Preserve the existing write-safety rule: cancellation after a write has
   entered the asynchronous submission stage must return a possibly-sent
   result and must not replay the command.
5. Replace fixed retry and confirmation sleeps with context-aware timers so
   shutdown can interrupt waits that are owned by the application.
6. Keep shutdown bounded. If a driver cannot be cancelled safely, apply an
   explicit final timeout and log which operation prevented immediate exit.

Acceptance criteria:

- With two active initialization reads and a third queued read, cancelling the
  scan prevents the third read from starting.
- Active reads terminate or detach safely within a documented maximum delay.
- `StopScan` remains idempotent and produces exactly one terminal scan state.
- Shutdown does not start new Bluetooth work and completes within the defined
  timeout.
- Tests cover shutdown during blocked connection, discovery, initial read, and
  power confirmation operations.
- Race tests pass without use-after-release, double-release, or goroutine leaks.
- Ambiguous power, Identify, Sleep, and channel writes remain non-replayable.

## Medium priority

### Make status recovery round behavior explicit

Status: Pending

Problem:

- `runStatusRecoveryRound` sorts all eligible recovery candidates and enters a
  loop, but every success and failure path returns after processing the first
  candidate.
- Later stations therefore require another scheduler wake or retry round even
  when a GATT slot is available, increasing recovery latency when several
  stations disconnect together.
- The loop implies batch processing while the implementation behaves as a
  single-candidate scheduler, making future changes prone to incorrect
  assumptions.

Relevant code:

- `internal/station/manager.go`: `runStatusRecoveryRound`, recovery candidate
  sorting, and recovery wake scheduling.
- `internal/station/manager_test.go`: multi-station recovery scheduling tests.

Proposed solution:

1. Decide and document whether one scheduler round should process one candidate
   or all currently eligible candidates.
2. If single-candidate fairness is intentional, remove the misleading loop,
   process `candidates[0]`, and explicitly schedule the next eligible candidate.
3. If batch recovery is intended, replace per-device terminal `return` paths
   with `continue`, while still returning immediately for shutdown, adapter
   unavailability, or global-operation contention.
4. Preserve the existing two-device GATT concurrency cap and per-address
   exclusion; do not introduce unbounded recovery goroutines.

Acceptance criteria:

- Multiple eligible stations recover in deterministic order without depending
  on an unrelated external wake event.
- One station's read failure does not indefinitely delay another eligible
  station.
- Foreground operations still take precedence over background recovery and
  shutdown still stops additional candidates from starting.
- Tests cover at least three simultaneous recovery candidates with mixed
  success and failure results.

### Keep cancellation outcome and StopScan response consistent

Status: Pending

Problem:

- If watcher shutdown fails or times out after a user cancellation request, the
  scan can be classified as `failed` while `Manager.StopScan` still returns
  `nil`.
- `POST /scan/stop` can therefore return success while `/scan/status` reports a
  failure and the frontend receives `external-scan-failed`.

Proposed solution:

1. Store the terminal scan error in the per-scan lifecycle object.
2. Have `StopScan` return that terminal error after waiting for the complete
   barrier.
3. Distinguish an expected cancellation with cleanup warnings from a failure
   that means watcher ownership or cleanup could not be completed safely.
4. Keep HTTP status, Wails promise result, scan status, and emitted event based
   on the same terminal classification.

Acceptance criteria:

- Successful cancellation produces `cancelled`, a cancelled event, and a
  successful Wails/HTTP response.
- Watcher stop failure or timeout produces one consistent structured failure at
  every API boundary.
- Repeated stop callers receive the same terminal result.

### Make external scan polling recover every terminal state

Status: Pending

Problem:

- Polling recovery distinguishes `cancelled` but currently formats `failed` as
  “External scan completed”.
- The failure-event path displays only the event error and omits accumulated
  scan warnings.
- Polling clears `externalScanning` before station refresh succeeds. A transient
  `GetCurrentStationInfo` failure prevents later polls from retrying terminal
  result recovery.

Relevant code:

- `frontend/src/App.svelte`: external scan event listeners,
  `periodicStatusCheck`, and `handleStopScan`.
- `frontend/src/lib/result-format.ts`: scan result formatting.

Proposed solution:

1. Add one shared terminal-scan formatter for `completed`, `cancelled`, and
   `failed`, including `found`, known count, warnings, and error.
2. Use it in completion events, failure events, cancellation events, local scan
   completion, and polling recovery.
3. Keep a separate `externalScanRecoveryPending` flag until station data and
   terminal scan status have both been refreshed successfully.
4. Retry terminal recovery on the next poll after a transient station or status
   request failure without relocking GATT status reads.
5. Guard late `StopScan` rejection with the scan epoch/request identity so it
   cannot overwrite a newer terminal event or subsequent scan.

Acceptance criteria:

- A missed failed event is recovered as failed, never completed.
- Failure and completion paths display accumulated warnings consistently.
- A transient station refresh failure is retried and eventually recovers the
  terminal result.
- A late stop promise success or rejection cannot overwrite a newer scan state.

### Add real lifecycle integration tests for scan stop APIs

Status: Pending

Problem:

- HTTP tests currently verify only forwarding to a fake `StopScan` method.
- Frontend tests mock the Wails promise and cannot prove the Go completion
  barrier, event ordering, or immediate-next-operation guarantee.

Proposed solution:

1. Add manager-backed HTTP tests for active, idle, concurrent, repeated, failed,
   and shutdown-overlap stop requests.
2. Assert response timing relative to terminal callback completion.
3. Verify event selection and final `/scan/status` for cancelled and failed
   cleanup cases.
4. Start a status or power operation immediately after the stop response and
   assert it cannot overlap the previous scan lifecycle.

Acceptance criteria:

- Tests fail if the HTTP or Wails stop boundary returns before its documented
  completion barrier.
- Event ordering is exactly `started` followed by one terminal event.
- No-active-scan and repeated stop remain idempotent.

## Hardware verification

### Validate cancellation and ambiguous writes on Windows hardware

Status: Pending

Problem:

- Automated tests emulate Bluetooth behavior but cannot reproduce every WinRT,
  adapter-driver, radio-disable, or Lighthouse firmware timing condition.

Proposed validation:

1. Run at least ten consecutive scans with two or more Lighthouse stations.
2. Stop scans during advertisement discovery, connection, service discovery,
   and initial state reads.
3. Exit the application during each of those phases and record shutdown time.
4. Disable and re-enable Bluetooth during reads and writes, then verify recovery
   without restarting the application.
5. Exercise On, Standby, Sleep, Identify, and channel changes while capturing
   `%APPDATA%\lhcontrol\lhcontrol.log`.
6. Confirm that a transport completion error never causes the same command to
   be written twice.

Acceptance criteria:

- No crash, deadlock, duplicate command, or stale permanent busy state.
- Cancelled scans end as `cancelled`, not `failed` or `completed`.
- Device state shown after an unconfirmed command is marked unconfirmed rather
  than reported as a definite failure or success.
- A subsequent scan and device operation recover without restarting the app.

## Regression checklist

Keep these guarantees covered while implementing the pending work:

- Invalid configuration files are preserved before a new configuration can be
  saved; preservation failure blocks saving.
- WinRT completion failures after asynchronous write creation are classified as
  possibly sent unless an explicit ATT rejection proves otherwise.
- Possibly-sent power, Sleep, Identify, and channel commands are not replayed.
- Offline connection and channel recovery schedules stop independently at
  their retry limits.
- Queued bulk power work does not start after shutdown begins.
- Future timestamps are not treated as fresh.
- Presence-uncertain stations cannot bypass channel safety checks.
- Wails and HTTP preserve structured channel confirmation results.
- External scan terminal refreshes cannot overwrite newer station operations.
- Cached state is labelled last-known, not actual readback.
- Scan stopping state remains stable regardless of promise completion order.
- Stale channel assignments remain visible as last-known but do not count as
  confirmed conflicts.
- Scan beam and scan progress animations remain removed.
