<script lang="ts">
  import { flip } from 'svelte/animate';
  import { cubicOut } from 'svelte/easing';
  import { fade } from 'svelte/transition';
  import { Activity, CircleAlert, Radar } from 'lucide-svelte';
  import type { PowerFeedback, PowerTarget, StationInfo } from '../types';
  import type { ScanErrorInfo } from '../scan-error';
  import { dur } from '../motion';
  import ChannelMap from './ChannelMap.svelte';
  import ScanRecovery from './ScanRecovery.svelte';
  import StationCard from './StationCard.svelte';

  let {
    stations,
    channelOf,
    selectedAddress,
    conflictDetails,
    scanError,
    isLoading,
    externalScanning,
    scanElapsed,
    editingAddress,
    feedbackByAddress,
    pendingTargetByAddress,
    gattBusyAddresses,
    configBusyAddresses,
    gattLockedByAddress,
    stationLocked,
    onSelect,
    onPower,
    onOpenDetails,
    onStartRename,
    onSaveRename,
    onCancelRename
  }: {
    stations: StationInfo[];
    channelOf: (station: StationInfo) => number;
    selectedAddress: string | null;
    conflictDetails: string;
    scanError: ScanErrorInfo | null;
    isLoading: boolean;
    externalScanning: boolean;
    scanElapsed: number;
    editingAddress: string | null;
    feedbackByAddress: Record<string, PowerFeedback | undefined>;
    pendingTargetByAddress: Record<string, PowerTarget | undefined>;
    gattBusyAddresses: Set<string>;
    configBusyAddresses: Set<string>;
    gattLockedByAddress: Map<string, boolean>;
    stationLocked: boolean;
    onSelect: (address: string) => void;
    onPower: (station: StationInfo, state: PowerTarget) => void;
    onOpenDetails: (station: StationInfo) => void;
    onStartRename: (station: StationInfo) => void;
    onSaveRename: (station: StationInfo, name: string) => void;
    onCancelRename: () => void;
  } = $props();
</script>

{#if conflictDetails}
  <div class="alert danger" title={conflictDetails} transition:fade={dur({ duration: 180 })}><CircleAlert size={18} /> <span class="alert-text">Channel conflict: {conflictDetails}</span></div>
{/if}
{#if scanError && !isLoading && !externalScanning}
  <ScanRecovery
    kind={scanError.kind}
    detail={scanError.detail}
  />
{/if}
{#if stations.length}
  <ChannelMap stations={stations} {channelOf} {selectedAddress} onSelect={onSelect} />
  <div class="station-grid">
    {#each stations as station, index (station.address)}
      <div
        animate:flip={dur({ duration: 300, easing: cubicOut })}
        in:fade={dur({ duration: 180, delay: Math.min(index * 30, 240) })}
        out:fade={dur({ duration: 120 })}
      >
        <StationCard
          {station}
          channelDisplay={channelOf(station)}
          renaming={editingAddress === station.address}
          feedback={feedbackByAddress[station.address]}
          pendingTarget={pendingTargetByAddress[station.address]}
          gattBusy={gattBusyAddresses.has(station.address)}
          configBusy={configBusyAddresses.has(station.address)}
          gattLocked={gattLockedByAddress.get(station.address) ?? false}
          renameLocked={stationLocked}
          onPower={onPower}
          onOpenDetails={onOpenDetails}
          onStartRename={onStartRename}
          onSaveRename={onSaveRename}
          onCancelRename={onCancelRename}
        />
      </div>
    {/each}
  </div>
{:else if isLoading || externalScanning}
  <div class="empty scan" in:fade={dur({ duration: 180 })}>
    <div class="empty-icon"><Radar size={40} /></div>
    <p>{isLoading ? 'Scanning for base stations...' : 'External scan in progress...'}{scanElapsed >= 1 ? ` ${scanElapsed}s` : ''}</p>
    <p class="scan-sub">{scanElapsed >= 6 ? 'Reading station states...' : 'Discovering nearby stations...'}</p>
  </div>
{:else if !scanError}
  <div class="empty">
    <div class="empty-icon"><Activity size={40} /></div>
    <p>No base stations found.</p>
  </div>
{/if}

<style>
  .station-grid {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(280px, 1fr));
    gap: var(--spacing-md);
    /* Stop cards from stretching into sparse wide stripes on ultra-wide
       windows. */
    max-width: 1720px;
    margin-inline: auto;
    width: 100%;
  }
  .empty {
    height: 100%;
    display: flex;
    flex-direction: column;
    justify-content: center;
    align-items: center;
    gap: 0.7rem;
    color: var(--text-muted);
  }
  .empty-icon {
    position: relative;
    width: 92px;
    height: 92px;
    display: flex;
    align-items: center;
    justify-content: center;
    border-radius: var(--radius-pill);
    border: 1px solid color-mix(in srgb, var(--color-primary) 25%, transparent);
    background:
      linear-gradient(135deg, color-mix(in srgb, var(--color-primary) 10%, white), color-mix(in srgb, var(--color-sleep) 12%, white));
    color: var(--color-primary-deep);
    box-shadow: 0 14px 34px -14px color-mix(in srgb, var(--color-primary) 45%, transparent), var(--shadow-sm);
  }
  .empty-icon::after {
    content: '';
    position: absolute;
    inset: -1px;
    border-radius: var(--radius-pill);
    border: 1px solid color-mix(in srgb, var(--color-primary) 40%, transparent);
  }
  /* Only a genuinely running scan pulses; the idle "no stations" state keeps
     a static ring so it does not suggest activity that is not happening. */
  .empty.scan .empty-icon::after {
    animation: ping-ring 2.2s var(--ease) infinite;
  }
  @keyframes ping-ring {
    0% { transform: scale(0.9); opacity: 1; }
    70% { transform: scale(1.35); opacity: 0; }
    100% { transform: scale(1.35); opacity: 0; }
  }
  .empty p { margin: 0; font-size: 0.85rem; font-weight: 700; }
  .empty .scan-sub { font-size: var(--fs-sm); font-weight: 600; color: var(--text-muted); }
</style>
