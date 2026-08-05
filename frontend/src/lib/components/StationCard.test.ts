import '@testing-library/jest-dom/vitest';
import { cleanup, fireEvent, render, screen } from '@testing-library/svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { StationInfo } from '../types';
import StationCard from './StationCard.svelte';

afterEach(cleanup);

function station(): StationInfo {
  return {
    name: 'LHB-TEST',
    originalName: 'LHB-TEST',
    address: '11:22:33:44:55:66',
    powerState: 1,
    powerStateName: 'on',
    powerStateConfirmed: true,
    rawPowerState: 0x0b,
    channel: 3,
    channelConflict: false,
    isPresent: true,
    seenInLatestScan: true,
    scanFresh: true,
    missedScans: 0,
    lastSeenAt: '',
    lastReadAt: '',
    lastPowerReadAt: '',
    lastChannelReadAt: '',
    metadataReadAt: '',
    lastError: '',
    statusFresh: true,
    powerFresh: true,
    channelFresh: true,
    metadataFresh: false,
    connectionState: 'connected',
    capabilitiesKnown: true,
    capabilities: {
      powerRead: true,
      powerWrite: true,
      powerNotify: false,
      standby: true,
      channelRead: true,
      channelWrite: true,
      channelNotify: false,
      identify: true,
      deviceInformation: false
    },
    metadata: {
      manufacturer: '',
      model: '',
      serialNumber: '',
      hardwareRevision: '',
      firmwareRevision: ''
    }
  } as StationInfo;
}

function cardProps(callbacks: Record<string, unknown>) {
  return {
    station: station(),
    renaming: true,
    gattBusy: false,
    configBusy: false,
    gattLocked: false,
    renameLocked: false,
    onPower: vi.fn(),
    onOpenDetails: vi.fn(),
    onStartRename: vi.fn(),
    onSaveRename: vi.fn(),
    onCancelRename: vi.fn(),
    ...callbacks
  };
}

describe('StationCard rename submission', () => {
  it('does not save the draft when Escape triggers the removal blur', async () => {
    const onSaveRename = vi.fn();
    const onCancelRename = vi.fn();
    render(StationCard, { props: cardProps({ onSaveRename, onCancelRename }) });
    const input = screen.getByRole('textbox', { name: 'Station name' });
    await fireEvent.input(input, { target: { value: 'Discarded draft' } });
    await fireEvent.keyDown(input, { key: 'Escape' });
    expect(onCancelRename).toHaveBeenCalledOnce();
    // Chromium synchronously fires blur on an input that was already removed
    // from the DOM; detach first to simulate the removal-triggered blur.
    input.remove();
    fireEvent.blur(input);
    expect(onSaveRename).not.toHaveBeenCalled();
  });

  it('does not save the draft when the Cancel button triggers the removal blur', async () => {
    const onSaveRename = vi.fn();
    const onCancelRename = vi.fn();
    render(StationCard, { props: cardProps({ onSaveRename, onCancelRename }) });
    const input = screen.getByRole('textbox', { name: 'Station name' });
    await fireEvent.input(input, { target: { value: 'Discarded draft' } });
    await fireEvent.click(screen.getByRole('button', { name: 'Cancel rename' }));
    expect(onCancelRename).toHaveBeenCalledOnce();
    input.remove();
    fireEvent.blur(input);
    expect(onSaveRename).not.toHaveBeenCalled();
  });

  it('saves exactly once when Enter is followed by the removal blur', async () => {
    const onSaveRename = vi.fn();
    render(StationCard, { props: cardProps({ onSaveRename }) });
    const input = screen.getByRole('textbox', { name: 'Station name' });
    await fireEvent.input(input, { target: { value: 'New name' } });
    await fireEvent.keyDown(input, { key: 'Enter' });
    input.remove();
    fireEvent.blur(input);
    expect(onSaveRename).toHaveBeenCalledOnce();
    expect(onSaveRename.mock.calls[0][1]).toBe('New name');
  });

  it('saves exactly once when the Save button is followed by the removal blur', async () => {
    const onSaveRename = vi.fn();
    render(StationCard, { props: cardProps({ onSaveRename }) });
    const input = screen.getByRole('textbox', { name: 'Station name' });
    await fireEvent.input(input, { target: { value: 'Saved name' } });
    await fireEvent.click(screen.getByRole('button', { name: 'Save name' }));
    input.remove();
    fireEvent.blur(input);
    expect(onSaveRename).toHaveBeenCalledOnce();
    expect(onSaveRename.mock.calls[0][1]).toBe('Saved name');
  });

  it('still saves the draft when focus moves away without an explicit action', async () => {
    const onSaveRename = vi.fn();
    render(StationCard, { props: cardProps({ onSaveRename }) });
    const input = screen.getByRole('textbox', { name: 'Station name' });
    await fireEvent.input(input, { target: { value: 'Blur saved' } });
    await fireEvent.blur(input);
    expect(onSaveRename).toHaveBeenCalledOnce();
    expect(onSaveRename.mock.calls[0][1]).toBe('Blur saved');
  });

  it('keeps blur saving alive when a rejected save leaves the rename row open', async () => {
    const onSaveRename = vi.fn();
    // The parent refuses the commit and keeps renaming true; the input stays
    // connected, so a later genuine focus loss must still save the draft.
    render(StationCard, { props: cardProps({ onSaveRename }) });
    const input = screen.getByRole('textbox', { name: 'Station name' });
    await fireEvent.input(input, { target: { value: 'Blocked draft' } });
    await fireEvent.click(screen.getByRole('button', { name: 'Save name' }));
    expect(onSaveRename).toHaveBeenCalledOnce();

    await fireEvent.blur(input);
    expect(onSaveRename).toHaveBeenCalledTimes(2);
    expect(onSaveRename.mock.calls[1][1]).toBe('Blocked draft');
  });

  it('keeps blur saving alive when a rejected cancel leaves the rename row open', async () => {
    const onSaveRename = vi.fn();
    const onCancelRename = vi.fn();
    render(StationCard, { props: cardProps({ onSaveRename, onCancelRename }) });
    const input = screen.getByRole('textbox', { name: 'Station name' });
    await fireEvent.input(input, { target: { value: 'Held draft' } });
    await fireEvent.keyDown(input, { key: 'Escape' });
    expect(onCancelRename).toHaveBeenCalledOnce();
    expect(onSaveRename).not.toHaveBeenCalled();

    await fireEvent.blur(input);
    expect(onSaveRename).toHaveBeenCalledOnce();
    expect(onSaveRename.mock.calls[0][1]).toBe('Held draft');
  });

  it('rearms blur saving for a new rename session after a cancel', async () => {
    const onSaveRename = vi.fn();
    const props = cardProps({ onSaveRename });
    const view = render(StationCard, { props });
    const input = screen.getByRole('textbox', { name: 'Station name' });
    await fireEvent.input(input, { target: { value: 'Discarded draft' } });
    await fireEvent.keyDown(input, { key: 'Escape' });

    await view.rerender({ ...props, renaming: false });
    await view.rerender({ ...props, renaming: true });

    const nextInput = screen.getByRole('textbox', { name: 'Station name' });
    await fireEvent.input(nextInput, { target: { value: 'Second draft' } });
    fireEvent.blur(nextInput);
    expect(onSaveRename).toHaveBeenCalledOnce();
    expect(onSaveRename.mock.calls[0][1]).toBe('Second draft');
  });
});
