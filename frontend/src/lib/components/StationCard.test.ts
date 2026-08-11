import '@testing-library/jest-dom/vitest';
import { cleanup, fireEvent, render, screen } from '@testing-library/svelte';
import { tick } from 'svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { StationInfo } from '../types';
import { createOnStation } from '../../test/fixtures';
import StationCard from './StationCard.svelte';

afterEach(cleanup);

function station(): StationInfo {
  return createOnStation();
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

  it('locks the rename input while its save is in progress', async () => {
    const props = cardProps({});
    const view = render(StationCard, { props });
    const input = screen.getByRole('textbox', { name: 'Station name' });
    await fireEvent.input(input, { target: { value: 'Pending name' } });

    await view.rerender({ ...props, configBusy: true });

    expect(input).toBeDisabled();
    expect(input).toHaveValue('Pending name');
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

  it('ignores Enter and Escape while an IME composition is active', async () => {
    const onSaveRename = vi.fn();
    const onCancelRename = vi.fn();
    render(StationCard, { props: cardProps({ onSaveRename, onCancelRename }) });
    const input = screen.getByRole('textbox', { name: 'Station name' });
    await fireEvent.input(input, { target: { value: '候' } });
    await fireEvent.keyDown(input, { key: 'Enter', isComposing: true });
    await fireEvent.keyDown(input, { key: 'Escape', isComposing: true });
    expect(onSaveRename).not.toHaveBeenCalled();
    expect(onCancelRename).not.toHaveBeenCalled();
    await fireEvent.keyDown(input, { key: 'Enter' });
    expect(onSaveRename).toHaveBeenCalledOnce();
    expect(onSaveRename.mock.calls[0][1]).toBe('候');
  });
});

describe('StationCard channel memory', () => {
  it.each([
    ['channel data is stale', { channelFresh: false }],
    ['the latest scan is stale', { scanFresh: false }],
    ['the station is absent', { isPresent: false }]
  ])('marks a positive channel as last-known when %s', (_, overrides) => {
    const stale = Object.assign(station(), overrides);
    render(StationCard, {
      props: { ...cardProps({}), renaming: false, station: stale }
    });

    const chip = screen.getByText('CH 03');
    expect(chip).toHaveClass('stale');
    expect(chip).toHaveAttribute('title', 'Last known channel');
  });

  it('drops the last known channel when the fleet memory expires', async () => {
    const wiped = station();
    wiped.channel = 0;
    wiped.channelFresh = false;
    const props = { ...cardProps({}), renaming: false, station: wiped, channelDisplay: 5 };
    const view = render(StationCard, { props });
    expect(screen.getByText('CH 05')).toBeInTheDocument();

    await view.rerender({ ...props, channelDisplay: 0 });

    expect(screen.getByText('CH --')).toBeInTheDocument();
  });
});

describe('StationCard stale power revalidation', () => {
  it('keeps the cached target selected while allowing it to be verified again', async () => {
    const stale = station();
    stale.powerFresh = false;
    const onPower = vi.fn();
    render(StationCard, {
      props: { ...cardProps({ onPower }), renaming: false, station: stale }
    });

    const onButton = screen.getByRole('button', { name: `Turn ${stale.name} on` });
    expect(onButton).toHaveAttribute('aria-pressed', 'true');
    expect(onButton).toBeEnabled();
    await fireEvent.click(onButton);
    expect(onPower).toHaveBeenCalledWith(stale, 'on');
  });
});
