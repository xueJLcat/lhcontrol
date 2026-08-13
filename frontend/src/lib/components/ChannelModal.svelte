<script lang="ts">
  import { cubicOut } from 'svelte/easing';
  import { scale } from 'svelte/transition';
  import { Eye, LoaderCircle, X } from 'lucide-svelte';
  import type { StationInfo } from '../types';
  import { channelChangeBlockedReason } from '../station';
  import { focusTrap } from '../actions';
  import { dur } from '../motion';
  import { t } from '../i18n.svelte';

  let {
    station,
    occupiedChannels,
    hasUnknownVisibleChannel,
    error,
    warning = false,
    busy,
    saving = false,
    locked,
    onClose,
    onSave,
    onIdentify
  }: {
    station: StationInfo;
    occupiedChannels: Map<number, string[]>;
    hasUnknownVisibleChannel: boolean;
    error: string;
    warning?: boolean;
    busy: boolean;
    saving?: boolean;
    locked: boolean;
    onClose: () => void;
    onSave: (channel: number, allowUnknownConflictRisk: boolean) => void;
    onIdentify: (station: StationInfo) => void;
  } = $props();

  function initialTargetChannel(channel: number, occupied: Map<number, string[]>): number {
    if (channel > 0 && !occupied.has(channel)) return channel;
    for (let candidate = 1; candidate <= 16; candidate += 1) {
      if (!occupied.has(candidate)) return candidate;
    }
    return channel > 0 ? channel : 1;
  }

  // The modal remounts on every open, so the initial-value capture in the
  // state initializers below is intentional: targetChannel starts at the
  // (possibly stale) current channel or the first free one. Until the user
  // picks a channel the default follows the live value, so a background
  // refresh while the modal is open cannot turn the default Confirm from a
  // no-op into a real change.
  // svelte-ignore state_referenced_locally
  let targetChannel = $state(initialTargetChannel(station.channel, occupiedChannels));
  let userTouched = $state(false);
  let confirmUnknownChannelRisk = $state(false);

  $effect(() => {
    if (!userTouched) {
      targetChannel = initialTargetChannel(station.channel, occupiedChannels);
    }
  });

  const unchanged = $derived(station.isPresent && station.scanFresh && station.channelOperationallyFresh &&
    station.channel > 0 && station.channel === targetChannel);
  const blockedReason = $derived(channelChangeBlockedReason(station));
  const allChannelsOccupied = $derived(occupiedChannels.size >= 16);
  const saveDisabled = $derived(Boolean(blockedReason) || unchanged || occupiedChannels.has(targetChannel) ||
    (hasUnknownVisibleChannel && !confirmUnknownChannelRisk) || busy || locked);

  function save() {
    if (!saveDisabled) onSave(targetChannel, confirmUnknownChannelRisk);
  }
</script>

<!-- svelte-ignore a11y_click_events_have_key_events -->
<div
  class="modal"
  role="dialog"
  aria-modal="true"
  aria-label={t('Change channel')}
  tabindex="-1"
  use:focusTrap
  transition:scale={dur({ start: 0.96, duration: 180, easing: cubicOut })}
  onclick={(event) => event.stopPropagation()}
>
  <div class="drawer-head">
    <div><small>{t('Safe channel change')}</small><h2>{station.name}</h2></div>
    <button type="button" class="icon-btn" title={t('Close')} aria-label={t('Close channel editor')} onclick={onClose} disabled={busy}><X size={18} /></button>
  </div>
  <dl class="def-list">
    <dt>{t('Original name')}</dt><dd>{station.originalName}</dd>
    <dt>{t('Address')}</dt><dd class="mono">{station.address}</dd>
    <dt>{t('Current channel')}</dt><dd class="mono">{station.channel || t('Unknown')}</dd>
  </dl>
  <fieldset class="ch-field">
    <legend>{t('Target channel')}</legend>
    <div class="ch-grid" role="group" aria-label={t('Target channel')}>
      {#each Array.from({ length: 16 }, (_, index) => index + 1) as channel}
        <!-- Occupied cells stay focusable and keep their tooltip: Chromium
             suppresses hover events and title tooltips on truly disabled
             buttons, which would hide who occupies the channel. -->
        <button
          type="button"
          class="ch-cell mono"
          class:selected={targetChannel === channel}
          class:current={station.channel > 0 && station.channel === channel}
          class:occupied={occupiedChannels.has(channel)}
          disabled={busy || locked}
          aria-disabled={occupiedChannels.has(channel)}
          aria-label={occupiedChannels.has(channel)
            ? t('Channel {channel}, occupied by {names}', { channel, names: occupiedChannels.get(channel)?.join(', ') ?? '' })
            : undefined}
          title={occupiedChannels.has(channel) ? t('Occupied by {names}', { names: occupiedChannels.get(channel)?.join(', ') ?? '' }) : t('Channel {channel}', { channel })}
          aria-pressed={targetChannel === channel}
          onclick={() => { if (!occupiedChannels.has(channel)) { targetChannel = channel; userTouched = true; } }}
        >{channel}{#if station.channel > 0 && station.channel === channel}<span class="ch-dot" aria-hidden="true"></span>{/if}</button>
      {/each}
    </div>
    <p class="hint ch-hint">{t('Struck-through channels are occupied by a visible station. The dot marks the current channel.')}</p>
    {#if allChannelsOccupied}
      <div class="alert warning ch-all-occupied" role="status">{t('All 16 channels are occupied by visible stations, so there is no free channel to switch to.')}</div>
    {/if}
  </fieldset>
  {#if hasUnknownVisibleChannel}
    <label class="risk"><input type="checkbox" bind:checked={confirmUnknownChannelRisk} disabled={busy || locked} /> {t('I understand that a visible station has an unknown channel, so a conflict cannot be fully ruled out.')}</label>
  {/if}
  {#if blockedReason}<div class="alert warning" role="status">{blockedReason}</div>{/if}
  {#if error}<div class="alert" class:danger={!warning} class:warning role="status">{error}</div>{/if}
  {#if busy}
    <p class="busy-note" role="status"><LoaderCircle class="spin" size={12} /> {saving ? t('Writing channel and verifying the readback...') : t('Bluetooth operation in progress')}</p>
  {/if}
  <p class="hint">{t('The value is only accepted after the base station reads back the requested channel. Failure will not trigger an automatic rollback.')}</p>
  <div class="modal-actions">
    <button class="btn" onclick={() => onIdentify(station)} disabled={busy || locked}><Eye size={15} /> {t('Identify this station')}</button>
    <button class="btn primary" onclick={save} disabled={saveDisabled}>{t('Confirm change')}</button>
  </div>
</div>

<style>
  .modal {
    width: min(440px, 100%);
    max-height: calc(100vh - 2rem);
    overflow-y: auto;
    box-sizing: border-box;
    background: var(--bg-surface-solid);
    border: 1px solid var(--color-border);
    border-radius: var(--radius-lg);
    padding: 1rem;
    box-shadow: var(--shadow-lg), inset 0 1px 0 rgba(255, 255, 255, 0.9);
  }
  .def-list { margin: 0.8rem 0 0; }

  .ch-field { border: 0; padding: 0; margin: 0.95rem 0 0; min-width: 0; }
  .ch-field legend {
    padding: 0;
    margin-bottom: 0.45rem;
    font-size: 0.8rem;
    font-weight: 800;
    color: var(--text-secondary);
  }
  .ch-field legend::before {
    content: '';
    display: inline-block;
    width: 3px;
    height: 11px;
    margin-right: 0.35rem;
    vertical-align: -1px;
    border-radius: 2px;
    background: linear-gradient(180deg, var(--color-primary), var(--color-sleep));
  }
  .ch-grid {
    display: grid;
    grid-template-columns: repeat(4, 1fr);
    gap: 0.4rem;
  }
  .ch-cell {
    position: relative;
    min-height: 40px;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    border: 1px solid var(--color-border-strong);
    border-radius: var(--radius-sm);
    background: var(--bg-input);
    color: var(--text-primary);
    font-size: 0.85rem;
    font-weight: 800;
    cursor: pointer;
    box-shadow: var(--shadow-sm);
    transition: background-color var(--dur-1) var(--ease), border-color var(--dur-1) var(--ease),
      color var(--dur-1) var(--ease), box-shadow var(--dur-1) var(--ease),
      transform 80ms var(--ease);
  }
  /* Inset ring: the outward outline would slip under the adjacent opaque
     cells in the tight grid. */
  .ch-cell:focus-visible { outline-offset: -2px; }
  .ch-cell:hover:not(:disabled):not(.occupied) {
    background: var(--bg-surface-hover);
    border-color: color-mix(in srgb, var(--color-primary) 35%, var(--color-border-strong));
    transform: translateY(-1px);
    box-shadow: var(--shadow-md);
  }
  .ch-cell:active:not(:disabled):not(.occupied) { transform: translateY(1px); box-shadow: none; }
  .ch-cell.selected {
    border-color: var(--color-primary);
    background: linear-gradient(135deg, color-mix(in srgb, var(--color-primary) 16%, white), color-mix(in srgb, var(--color-primary) 6%, white));
    color: var(--color-primary-deep);
    box-shadow: 0 0 0 3px color-mix(in srgb, var(--color-primary) 16%, transparent), var(--shadow-sm);
  }
  .ch-cell.current:not(.selected) {
    border-color: var(--color-border-strong);
    background: var(--bg-surface-hover);
  }
  .ch-cell:disabled, .ch-cell.occupied {
    cursor: not-allowed;
    opacity: 0.4;
    text-decoration: line-through;
    box-shadow: none;
  }
  .ch-dot {
    position: absolute;
    top: 4px;
    right: 4px;
    width: 5px;
    height: 5px;
    border-radius: var(--radius-pill);
    background: var(--color-primary);
    box-shadow: 0 0 5px color-mix(in srgb, var(--color-primary) 75%, transparent);
  }
  .ch-hint { margin: 0.5rem 0 0; }
  .ch-all-occupied { margin-top: 0.6rem; }

  .busy-note {
    display: flex;
    align-items: center;
    gap: 0.35rem;
    margin: 0.8rem 0 0;
    font-size: var(--fs-sm);
    font-weight: 800;
    color: var(--color-primary-deep);
  }
  .busy-note > :global(svg) { flex-shrink: 0; }

  .risk { display: flex; gap: 0.5rem; align-items: flex-start; margin-top: 0.8rem; font-size: 0.75rem; font-weight: 700; line-height: 1.4; color: var(--color-warning-deep); }
  .risk input { margin-top: 0.15rem; accent-color: var(--color-warning); }
  .ch-hint { margin: 0.5rem 0 0; }
  .modal-actions { display: flex; align-items: center; gap: 0.5rem; flex-wrap: wrap; justify-content: flex-end; margin-top: 0.8rem; }
  @media (max-width: 420px) {
    .ch-grid { grid-template-columns: repeat(4, 1fr); gap: 0.3rem; }
  }
</style>
