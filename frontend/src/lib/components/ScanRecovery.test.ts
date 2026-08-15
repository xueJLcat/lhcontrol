import '@testing-library/jest-dom/vitest';
import { cleanup, render, screen } from '@testing-library/svelte';
import { afterEach, describe, expect, it } from 'vitest';
import ScanRecovery from './ScanRecovery.svelte';

afterEach(cleanup);

function renderRecovery(overrides: Record<string, unknown> = {}) {
  render(ScanRecovery, {
    props: {
      kind: 'bluetooth-off',
      detail: 'Bluetooth is unavailable; turn on Bluetooth and retry',
      ...overrides
    }
  });
}

describe('ScanRecovery', () => {
  it('announces the failure with targeted guidance', () => {
    renderRecovery();
    expect(screen.getByRole('alert')).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: 'Bluetooth is unavailable' })).toBeInTheDocument();
    expect(screen.getAllByRole('listitem').length).toBeGreaterThan(0);
  });

  it('keeps the raw backend error visible for diagnostics', () => {
    renderRecovery();
    expect(screen.getByText('Bluetooth is unavailable; turn on Bluetooth and retry')).toBeInTheDocument();
  });

  it('offers no scan entry point of its own; the header owns scanning', () => {
    renderRecovery();
    expect(screen.queryByRole('button')).not.toBeInTheDocument();
  });

  it('renders targeted copy for every error kind', () => {
    for (const kind of ['adapter-missing', 'permission', 'busy', 'timeout', 'unknown'] as const) {
      cleanup();
      renderRecovery({ kind });
      expect(screen.getByRole('alert')).toBeInTheDocument();
    }
  });

  it('renders the busy guidance with its retry step', () => {
    renderRecovery({ kind: 'busy', detail: 'another Bluetooth operation is already in progress' });
    expect(screen.getByRole('heading', { name: 'Bluetooth is busy' })).toBeInTheDocument();
    expect(screen.getAllByRole('listitem')).toHaveLength(1);
    expect(screen.getByText('another Bluetooth operation is already in progress')).toBeInTheDocument();
  });
});
