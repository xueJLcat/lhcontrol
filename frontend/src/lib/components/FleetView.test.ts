import '@testing-library/jest-dom/vitest';
import { cleanup, fireEvent, render, screen } from '@testing-library/svelte';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { StationInfo } from '../types';
import { createStation } from '../../test/fixtures';
import FleetView from './FleetView.svelte';

afterEach(cleanup);

beforeEach(() => {
  Object.defineProperty(Element.prototype, 'animate', {
    configurable: true,
    value: () => ({
      cancel: vi.fn(),
      finish: vi.fn(),
      play: vi.fn(),
      pause: vi.fn(),
      reverse: vi.fn(),
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      finished: Promise.resolve()
    })
  });
});

function defaultProps(overrides: Record<string, unknown> = {}) {
  return {
    stations: [createStation()],
    channelOf: (station: StationInfo) => station.channel,
    selectedAddress: null,
    conflictDetails: '',
    scanError: null,
    isLoading: false,
    externalScanning: false,
    scanElapsed: 0,
    editingAddress: null,
    feedbackByAddress: {},
    pendingTargetByAddress: {},
    gattBusyAddresses: new Set<string>(),
    configBusyAddresses: new Set<string>(),
    gattLockedByAddress: new Map<string, boolean>(),
    stationLocked: false,
    onSelect: vi.fn(),
    onPower: vi.fn(),
    onOpenDetails: vi.fn(),
    onStartRename: vi.fn(),
    onSaveRename: vi.fn(),
    onCancelRename: vi.fn(),
    ...overrides
  };
}

describe('FleetView', () => {
  it('renders the channel map and a card for every station', () => {
    render(FleetView, { props: defaultProps() });
    expect(screen.getByRole('button', { name: 'CH 3 — LHB-TEST · sleep' })).toBeInTheDocument();
    expect(screen.getByText('LHB-TEST')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Turn LHB-TEST on' })).toBeInTheDocument();
    expect(screen.getByText('CH 03')).toBeInTheDocument();
  });

  it('selects a station through the channel map', async () => {
    const props = defaultProps();
    render(FleetView, { props });
    await fireEvent.click(screen.getByRole('button', { name: 'CH 3 — LHB-TEST · sleep' }));
    expect(props.onSelect).toHaveBeenCalledWith('11:22:33:44:55:66');
  });

  it('opens details through the card', async () => {
    const props = defaultProps();
    render(FleetView, { props });
    await fireEvent.click(screen.getByRole('button', { name: 'Details for LHB-TEST' }));
    expect(props.onOpenDetails).toHaveBeenCalledWith(expect.objectContaining({ address: '11:22:33:44:55:66' }));
  });

  it('shows the idle empty state without its own scan entry point', () => {
    render(FleetView, { props: defaultProps({ stations: [] }) });
    expect(screen.getByText('No base stations found.')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Scan Now' })).not.toBeInTheDocument();
  });

  it('shows the local scanning placeholder with elapsed time', () => {
    render(FleetView, { props: defaultProps({ stations: [], isLoading: true, scanElapsed: 12 }) });
    expect(screen.getByText('Scanning for base stations... 12s')).toBeInTheDocument();
    expect(screen.getByText('Reading station states...')).toBeInTheDocument();
  });

  it('shows the external scanning placeholder', () => {
    render(FleetView, { props: defaultProps({ stations: [], externalScanning: true }) });
    expect(screen.getByText('External scan in progress...')).toBeInTheDocument();
    expect(screen.getByText('Discovering nearby stations...')).toBeInTheDocument();
  });

  it('keeps the recovery card instead of the idle empty state after a failed scan', () => {
    render(FleetView, {
      props: defaultProps({
        stations: [],
        scanError: { kind: 'timeout', detail: 'operation timed out' }
      })
    });
    expect(screen.getByRole('heading', { name: 'The scan timed out' })).toBeInTheDocument();
    expect(screen.queryByText('No base stations found.')).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Retry scan' })).not.toBeInTheDocument();
  });

  it('surfaces channel conflicts in a banner', () => {
    render(FleetView, { props: defaultProps({ conflictDetails: 'CH 3: LHB-A + LHB-B' }) });
    const banner = screen.getByText('Channel conflict: CH 3: LHB-A + LHB-B');
    expect(banner.parentElement).toHaveAttribute('title', 'CH 3: LHB-A + LHB-B');
  });
});
