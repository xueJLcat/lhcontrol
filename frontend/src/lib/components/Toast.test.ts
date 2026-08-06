import '@testing-library/jest-dom/vitest';
import { cleanup, fireEvent, render, screen } from '@testing-library/svelte';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { clearToasts, pushToast } from '../toast';
import Toast from './Toast.svelte';

beforeEach(() => {
  vi.useFakeTimers();
  Object.defineProperty(Element.prototype, 'animate', {
    configurable: true,
    value: vi.fn(() => ({
      cancel: vi.fn(),
      finished: Promise.resolve(),
      set onfinish(callback: ((event: AnimationPlaybackEvent) => void) | null) {
        if (callback) Promise.resolve().then(() => callback({} as AnimationPlaybackEvent));
      }
    }))
  });
});

afterEach(() => {
  cleanup();
  clearToasts();
  vi.useRealTimers();
});

describe('Toast stack', () => {
  it('announces errors assertively and other kinds politely', async () => {
    render(Toast);
    pushToast('Power change failed', 'error');
    pushToast('Renamed to LHB-A.', 'success');
    pushToast('Check the adapter', 'warning');
    pushToast('Scan finished', 'info');
    await vi.advanceTimersByTimeAsync(0);

    expect(screen.getByRole('alert')).toHaveTextContent('Power change failed');
    const statuses = screen.getAllByRole('status');
    expect(statuses.map((node) => node.textContent)).toEqual(
      expect.arrayContaining([
        expect.stringContaining('Renamed to LHB-A.'),
        expect.stringContaining('Check the adapter'),
        expect.stringContaining('Scan finished')
      ])
    );
  });

  it('dismisses a toast through its close button', async () => {
    render(Toast);
    pushToast('Bulk Sleep: 2 confirmed.', 'success');
    await vi.advanceTimersByTimeAsync(0);
    await fireEvent.click(screen.getByRole('button', { name: 'Dismiss notification' }));
    await vi.advanceTimersByTimeAsync(400);
    expect(screen.queryByText(/Bulk Sleep/)).not.toBeInTheDocument();
  });

  it('keeps a toast visible until its retention passes', async () => {
    render(Toast);
    pushToast('short note', 'info');
    await vi.advanceTimersByTimeAsync(5999);
    expect(screen.getByText('short note')).toBeInTheDocument();
    await vi.advanceTimersByTimeAsync(2);
    await vi.advanceTimersByTimeAsync(400);
    expect(screen.queryByText('short note')).not.toBeInTheDocument();
  });
});
