import '@testing-library/jest-dom/vitest';
import { cleanup, fireEvent, render, screen } from '@testing-library/svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';
import ScanRecovery from './ScanRecovery.svelte';

afterEach(cleanup);

function renderRecovery(overrides: Record<string, unknown> = {}) {
  const onRetry = vi.fn();
  render(ScanRecovery, {
    props: {
      kind: 'bluetooth-off',
      detail: 'Bluetooth is unavailable; turn on Bluetooth and retry',
      onRetry,
      ...overrides
    }
  });
  return onRetry;
}

describe('ScanRecovery', () => {
  it('announces the failure and offers a retry action', async () => {
    const onRetry = renderRecovery();
    expect(screen.getByRole('alert')).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: 'Bluetooth is unavailable' })).toBeInTheDocument();
    await fireEvent.click(screen.getByRole('button', { name: 'Retry scan' }));
    expect(onRetry).toHaveBeenCalledOnce();
  });

  it('keeps the raw backend error visible for diagnostics', () => {
    renderRecovery();
    expect(screen.getByText('Bluetooth is unavailable; turn on Bluetooth and retry')).toBeInTheDocument();
  });

  it('disables retry while another scan is locked', () => {
    renderRecovery({ retryDisabled: true });
    expect(screen.getByRole('button', { name: 'Retry scan' })).toBeDisabled();
  });

  it('renders targeted copy for every error kind', () => {
    for (const kind of ['adapter-missing', 'permission', 'timeout', 'unknown'] as const) {
      cleanup();
      renderRecovery({ kind });
      expect(screen.getByRole('alert')).toBeInTheDocument();
      expect(screen.getByRole('button', { name: 'Retry scan' })).toBeEnabled();
    }
  });
});
