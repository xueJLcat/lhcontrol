import '@testing-library/jest-dom/vitest';
import { cleanup, fireEvent, render, screen } from '@testing-library/svelte';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import BulkConfirmModal from './BulkConfirmModal.svelte';

beforeEach(() => {
  Object.defineProperty(Element.prototype, 'animate', {
    configurable: true,
    value: vi.fn(() => {
      const animation = {
        cancel: vi.fn(),
        finished: Promise.resolve(),
        set onfinish(callback: ((event: AnimationPlaybackEvent) => void) | null) {
          if (callback) Promise.resolve().then(() => callback({} as AnimationPlaybackEvent));
        }
      };
      return animation;
    })
  });
});

afterEach(cleanup);

function renderModal(overrides: Record<string, unknown> = {}) {
  const onConfirm = vi.fn();
  const onCancel = vi.fn();
  render(BulkConfirmModal, {
    props: {
      target: 'sleep',
      visibleCount: 2,
      invisibleCount: 1,
      uncertainCount: 0,
      actionableCount: 3,
      busy: false,
      onConfirm,
      onCancel,
      ...overrides
    }
  });
  return { onConfirm, onCancel };
}

describe('BulkConfirmModal', () => {
  it('describes the bulk scope before confirming', async () => {
    const { onConfirm } = renderModal();
    const dialog = screen.getByRole('dialog', { name: 'Confirm bulk power' });
    expect(dialog).toBeInTheDocument();
    expect(screen.getByText('Visible & verified')).toBeInTheDocument();
    expect(screen.getByText('Not seen in latest scan')).toBeInTheDocument();
    await fireEvent.click(screen.getByRole('button', { name: 'Put to sleep 3 stations' }));
    expect(onConfirm).toHaveBeenCalledOnce();
  });

  it('uses a full action verb in the confirm button', () => {
    renderModal({ target: 'on', actionableCount: 1 });
    expect(screen.getByRole('button', { name: 'Turn on 1 station' })).toBeEnabled();
    cleanup();
    renderModal({ target: 'standby', actionableCount: 2 });
    expect(screen.getByRole('button', { name: 'Set to standby 2 stations' })).toBeEnabled();
  });

  it('cancels without confirming', async () => {
    const { onConfirm, onCancel } = renderModal();
    await fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    expect(onCancel).toHaveBeenCalledOnce();
    expect(onConfirm).not.toHaveBeenCalled();
  });

  it('blocks confirmation while the operation is busy or nothing is actionable', () => {
    renderModal({ busy: true });
    expect(screen.getByRole('button', { name: 'Put to sleep 3 stations' })).toBeDisabled();
    cleanup();
    renderModal({ actionableCount: 0 });
    expect(screen.getByRole('button', { name: 'Put to sleep 0 stations' })).toBeDisabled();
  });

  it('never traps closing while another Bluetooth operation holds the backend', async () => {
    // busy inside this dialog can only mean an external lock (the dialog
    // closes before its own bulk runs): Confirm is blocked, but every close
    // path stays available, and the note must describe the lock instead of a
    // bulk that has not started.
    const { onConfirm, onCancel } = renderModal({ busy: true });
    expect(screen.getByText('Bluetooth operation in progress')).toBeInTheDocument();
    expect(screen.queryByText('Applying bulk power...')).not.toBeInTheDocument();
    await fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    expect(onCancel).toHaveBeenCalledOnce();
    await fireEvent.click(screen.getByRole('button', { name: 'Close bulk power confirmation' }));
    expect(onCancel).toHaveBeenCalledTimes(2);
    expect(onConfirm).not.toHaveBeenCalled();
  });

  it('omits empty breakdown rows', () => {
    renderModal({ invisibleCount: 0 });
    expect(screen.queryByText('Not seen in latest scan')).not.toBeInTheDocument();
    expect(screen.queryByText('Presence uncertain')).not.toBeInTheDocument();
  });
});
