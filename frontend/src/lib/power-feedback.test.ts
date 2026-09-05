import { afterEach, describe, expect, it, vi } from 'vitest';
import { PowerFeedbackRegistry } from './power-feedback';

afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
});

describe('PowerFeedbackRegistry', () => {
  it('expires a stale pending note for an idle address', async () => {
    vi.useFakeTimers();
    const record = new Map<Record<string, unknown>, unknown>();
    let snapshot: Record<string, unknown> = {};
    const registry = new PowerFeedbackRegistry((next) => { snapshot = next; record.set(snapshot, true); });
    registry.set('AA', { kind: 'pending', text: 'Switching to On…', target: 'on' });

    await vi.advanceTimersByTimeAsync(60_000);
    expect(snapshot['AA']).toBeUndefined();
  });

  it('re-arms a pending note while its operation is verifiably still running', async () => {
    vi.useFakeTimers();
    const busy = vi.fn(() => true);
    let snapshot: Record<string, unknown> = {};
    const registry = new PowerFeedbackRegistry((next) => { snapshot = next; }, busy);
    registry.set('AA', { kind: 'pending', text: 'Switching to On…', target: 'on' });

    // The station operation timeout is user-tunable up to 120s while the
    // pending retention window covers 60s: the note must survive the whole
    // budget while the address owns a live operation.
    await vi.advanceTimersByTimeAsync(60_000);
    await vi.advanceTimersByTimeAsync(60_000);
    expect(snapshot['AA']).toBeDefined();
    expect(busy).toHaveBeenCalled();

    // The operation settles: the next window expiry drops the note.
    busy.mockReturnValue(false);
    await vi.advanceTimersByTimeAsync(60_000);
    expect(snapshot['AA']).toBeUndefined();
  });

  it('drops a pending note past the hard age cap even while the flag stays wedged', async () => {
    vi.useFakeTimers();
    let snapshot: Record<string, unknown> = {};
    const registry = new PowerFeedbackRegistry((next) => { snapshot = next; }, () => true);
    registry.set('AA', { kind: 'pending', text: 'Switching to On…', target: 'on' });

    // A wedged binding never clears its busy flag; the re-arm must stop at
    // the total-age cap instead of extending the note forever.
    await vi.advanceTimersByTimeAsync(60_000);
    expect(snapshot['AA']).toBeDefined();
    await vi.advanceTimersByTimeAsync(60_000);
    expect(snapshot['AA']).toBeDefined();
    await vi.advanceTimersByTimeAsync(60_000);
    expect(snapshot['AA']).toBeUndefined();
  });

  it('expires settled notes regardless of the busy state', async () => {
    vi.useFakeTimers();
    let snapshot: Record<string, unknown> = {};
    const registry = new PowerFeedbackRegistry((next) => { snapshot = next; }, () => true);
    registry.set('AA', { kind: 'success', text: 'On confirmed', target: 'on' });

    await vi.advanceTimersByTimeAsync(20_000);
    expect(snapshot['AA']).toBeUndefined();
  });
});
