import '@testing-library/jest-dom/vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import StatusFooter from './StatusFooter.svelte';

beforeEach(() => {
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

afterEach(cleanup);

function renderFooter(overrides: Record<string, unknown> = {}) {
  render(StatusFooter, {
    props: {
      statusMessage: 'Ready to scan.',
      apiRunning: true,
      apiError: '',
      apiAddress: '127.0.0.1:7575',
      configWarnings: [],
      configWritable: true,
      ...overrides
    }
  });
}

describe('StatusFooter', () => {
  it('shows the current status line in a live region', () => {
    renderFooter({ statusMessage: 'Scanning for base stations...' });
    expect(screen.getByRole('status')).toHaveTextContent('Scanning for base stations...');
  });

  it('exposes config warnings through a focusable, expandable control', async () => {
    renderFooter({ configWarnings: ['disk almost full'], configWritable: false });
    const control = screen.getByRole('button', { name: 'Config read-only' });
    expect(control).toHaveAttribute('aria-expanded', 'false');

    await fireEvent.click(control);
    expect(control).toHaveAttribute('aria-expanded', 'true');
    expect(screen.getByText('disk almost full')).toBeInTheDocument();
    expect(screen.getByText('Configuration changes cannot be saved.')).toBeInTheDocument();

    await fireEvent.click(control);
    await waitFor(() => expect(screen.queryByText('disk almost full')).not.toBeInTheDocument());
  });

  it('hides the config pill while the API is offline', () => {
    renderFooter({ apiRunning: false, apiError: 'gone', configWarnings: ['old warning'], configWritable: false });
    expect(screen.queryByText('Config read-only')).not.toBeInTheDocument();
    expect(screen.queryByText('Config warning')).not.toBeInTheDocument();
  });

  it('drops an open config panel when the API goes offline', async () => {
    const view = render(StatusFooter, {
      props: {
        statusMessage: 'Ready.',
        apiRunning: true,
        apiError: '',
        apiAddress: '127.0.0.1:7575',
        configWarnings: ['stale warning'],
        configWritable: false
      }
    });
    await fireEvent.click(screen.getByRole('button', { name: 'Config read-only' }));
    expect(screen.getByText('stale warning')).toBeInTheDocument();

    await view.rerender({
      statusMessage: 'Ready.',
      apiRunning: false,
      apiError: 'gone',
      apiAddress: '',
      configWarnings: ['stale warning'],
      configWritable: false
    });
    await waitFor(() => expect(screen.queryByText('stale warning')).not.toBeInTheDocument());
  });

  it('reopens the config panel on the first click after the warnings return', async () => {
    const base = {
      statusMessage: 'Ready.',
      apiRunning: true,
      apiError: '',
      apiAddress: '127.0.0.1:7575',
      configWritable: true
    };
    const view = render(StatusFooter, { props: { ...base, configWarnings: ['disk almost full'] } });
    await fireEvent.click(screen.getByRole('button', { name: 'Config warning' }));
    expect(screen.getByText('disk almost full')).toBeInTheDocument();

    // The warning clears: the panel hides and its open state must reset.
    await view.rerender({ ...base, configWarnings: [] });
    await waitFor(() => expect(screen.queryByText('disk almost full')).not.toBeInTheDocument());

    // The warning returns: a single click must open the panel again.
    await view.rerender({ ...base, configWarnings: ['disk almost full again'] });
    const control = await screen.findByRole('button', { name: 'Config warning' });
    await fireEvent.click(control);
    expect(control).toHaveAttribute('aria-expanded', 'true');
    expect(screen.getByText('disk almost full again')).toBeInTheDocument();
  });

  it('expands the API pill to reveal the error or address', async () => {
    renderFooter({ apiRunning: false, apiError: 'API crashed' });
    const control = screen.getByRole('button', { name: 'API offline' });
    await fireEvent.click(control);
    expect(screen.getByText('API crashed')).toBeInTheDocument();

    cleanup();
    renderFooter({ apiRunning: true, apiAddress: '127.0.0.1:9000' });
    const ok = screen.getByRole('button', { name: 'API ready' });
    await fireEvent.click(ok);
    expect(screen.getByText('HTTP API 127.0.0.1:9000')).toBeInTheDocument();
  });
});
