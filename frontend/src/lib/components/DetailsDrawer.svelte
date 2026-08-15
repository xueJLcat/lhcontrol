<script lang="ts">
  import { onMount } from 'svelte';
  import { cubicOut } from 'svelte/easing';
  import { fly } from 'svelte/transition';
  import { CircleCheck, Eye, LoaderCircle, RefreshCw, Settings2, X } from 'lucide-svelte';
  import type { Capabilities, StationInfo } from '../types';
  import { backendCopy } from '../backend-copy';
  import { channelChangeBlockedReason, stateClass, stateLabel } from '../station';
  import { relativeTime } from '../relative-time';
  import { focusTrap } from '../actions';
  import { dur } from '../motion';
  import { t, type TranslationKey } from '../i18n.svelte';
  import StateBadge from './StateBadge.svelte';

  let {
    station,
    busy,
    locked,
    inactive = false,
    onClose,
    onRefresh,
    onIdentify,
    onOpenChannelEditor
  }: {
    station: StationInfo;
    busy: boolean;
    locked: boolean;
    inactive?: boolean;
    onClose: () => void;
    onRefresh: (station: StationInfo) => void;
    onIdentify: (station: StationInfo) => void;
    onOpenChannelEditor: (station: StationInfo) => void;
  } = $props();

  let now = $state(Date.now());

  onMount(() => {
    const timer = setInterval(() => {
      now = Date.now();
    }, 30000);
    return () => clearInterval(timer);
  });

  const capabilityGroups: { label: TranslationKey; entries: [keyof Capabilities, TranslationKey][] }[] = [
    { label: 'Power', entries: [['powerRead', 'read'], ['powerWrite', 'write'], ['powerNotify', 'notify'], ['standby', 'standby']] },
    { label: 'Channel', entries: [['channelRead', 'read'], ['channelWrite', 'write'], ['channelNotify', 'notify']] },
    { label: 'Other', entries: [['identify', 'identify'], ['deviceInformation', 'device info']] }
  ];

  const hasCachedMetadata = $derived(Boolean(
    station.metadataReadAt ||
    station.metadata.manufacturer ||
    station.metadata.model ||
    station.metadata.serialNumber ||
    station.metadata.hardwareRevision ||
    station.metadata.firmwareRevision
  ));
  const metadataStatus = $derived(t(station.metadataFresh
    ? 'loaded and fresh'
    : hasCachedMetadata ? 'cached, stale' : 'unavailable'));
  const channelBlockedReason = $derived(channelChangeBlockedReason(station));
  // Rendered like the backend diagnostics (0x09); -1 means no readback yet.
  const rawPowerLabel = $derived(station.rawPowerState < 0
    ? '—'
    : `0x${station.rawPowerState.toString(16).toUpperCase().padStart(2, '0')}`);
</script>

<div
  class="drawer"
  role="dialog"
  aria-modal={inactive ? undefined : 'true'}
  aria-hidden={inactive ? 'true' : undefined}
  inert={inactive}
  aria-label={t('Station details')}
  tabindex="-1"
  use:focusTrap
  in:fly={dur({ x: 64, duration: 320, easing: cubicOut })}
  out:fly={dur({ x: 64, duration: 180 })}
>
  <div class="drawer-head">
    <div>
      <small>{t('Station details')}</small>
      <div class="drawer-title">
        <h2>{station.name}</h2>
        <StateBadge
          label={stateLabel(station)}
          state={stateClass(station)}
          unverified={station.powerState >= 0 && station.powerFresh && !station.powerStateConfirmed}
          stale={station.powerState >= 0 && !station.powerFresh}
          booting={station.powerFresh && station.powerState === 3}
        />
      </div>
    </div>
    <button type="button" class="icon-btn" title={t('Close')} aria-label={t('Close station details')} onclick={onClose}><X size={18} /></button>
  </div>

  <section>
    <h4>{t('Status')}</h4>
    <dl class="def-list">
      <dt>{t('Power')}</dt><dd><span class="state-text state-text-{stateClass(station)}">{stateLabel(station)}</span> · {t(station.powerFresh ? station.powerStateConfirmed ? 'confirmed' : 'unverified' : 'last known, stale')} ({t('raw {value}', { value: rawPowerLabel })})</dd>
      <dt>{t('Channel')}</dt><dd class="mono">{station.channel || t('Unable to verify')}</dd>
      <dt>{t('Connection')}</dt><dd>{station.connectionState === 'connected' ? t('connected') : station.connectionState === 'disconnected' ? t('disconnected') : t('Unknown')}</dd>
      <dt>{t('Last seen')}</dt><dd title={station.lastSeenAt || undefined}>{relativeTime(station.lastSeenAt, now) || '—'}</dd>
      <dt>{t('Last status read')}</dt><dd title={station.lastReadAt || undefined}>{relativeTime(station.lastReadAt, now) || '—'}</dd>
      <dt>{t('Power data')}</dt><dd title={station.lastPowerReadAt || undefined}>{t(station.powerFresh ? 'fresh' : 'stale or unavailable')} · {relativeTime(station.lastPowerReadAt, now) || t('never')}</dd>
      <dt>{t('Channel data')}</dt><dd title={station.lastChannelReadAt || undefined}>{t(station.channelFresh ? 'fresh' : 'stale or unavailable')} · {relativeTime(station.lastChannelReadAt, now) || t('never')}</dd>
    </dl>
    {#if station.lastError}<div class="alert danger">{backendCopy(station.lastError)}</div>{/if}
    {#if !station.capabilitiesKnown}
      <div class="alert">{t('Capabilities could not be verified. Power commands will retry discovery; unsupported operations will be reported.')}</div>
    {/if}
    <div class="drawer-actions">
      <button class="btn" onclick={() => onRefresh(station)} disabled={busy || locked}>{#if busy}<LoaderCircle class="spin" size={15} />{:else}<RefreshCw size={15} />{/if} {t('Refresh capabilities')}</button>
      <button class="btn" onclick={() => onIdentify(station)} disabled={busy || locked} title={t(station.capabilities.identify ? 'Send the identify signal' : 'Recheck support and identify')}>{#if busy}<LoaderCircle class="spin" size={15} />{:else}<Eye size={15} />{/if} {t('Identify')}</button>
      <button
        class="btn primary"
        onclick={() => onOpenChannelEditor(station)}
        disabled={Boolean(channelBlockedReason) || busy || locked}
        title={channelBlockedReason || t('Recheck support and change Channel safely')}
      ><Settings2 size={15} /> {t('Change Channel')}</button>
    </div>
    {#if station.capabilities.identify}<p class="hint">{t('Identify may wake a sleeping base station.')}</p>{/if}
    {#if channelBlockedReason}<p class="hint warning-text">{channelBlockedReason}</p>{/if}
  </section>

  <section>
    <h4>{t('Identity')}</h4>
    <dl class="def-list">
      <dt>{t('Original name')}</dt><dd>{station.originalName}</dd>
      <dt>{t('Address')}</dt><dd class="mono">{station.address}</dd>
      <dt>{t('Metadata')}</dt><dd title={station.metadataReadAt || undefined}>{metadataStatus} · {relativeTime(station.metadataReadAt, now) || t('never')}</dd>
    </dl>
  </section>

  <section>
    <h4>{t('Device information')}</h4>
    <dl class="def-list">
      <dt>{t('Manufacturer')}</dt><dd>{station.metadata.manufacturer || '—'}</dd>
      <dt>{t('Model')}</dt><dd>{station.metadata.model || '—'}</dd>
      <dt>{t('Serial number')}</dt><dd class="mono">{station.metadata.serialNumber || '—'}</dd>
      <dt>{t('Hardware')}</dt><dd class="mono">{station.metadata.hardwareRevision || '—'}</dd>
      <dt>{t('Firmware')}</dt><dd class="mono">{station.metadata.firmwareRevision || '—'}</dd>
    </dl>
  </section>

  <section>
    <h4>{t('Capabilities')}</h4>
    <div class="capability-groups">
      {#each capabilityGroups as group}
        <div class="capability-group">
          <span class="group-label">{t(group.label)}</span>
          <div class="capabilities">
            {#each group.entries as [key, label]}
              <span class:supported={station.capabilitiesKnown && station.capabilities[key]}>
                {#if station.capabilitiesKnown && station.capabilities[key]}<CircleCheck size={10} />{/if}{t(label)}
              </span>
            {/each}
          </div>
        </div>
      {/each}
    </div>
  </section>
</div>

<style>
  .drawer {
    position: fixed;
    z-index: 11;
    right: 0; top: 0; bottom: 0;
    width: min(390px, 92vw);
    /* Colorful signature edge, styled after the card accent strip: 2px wide,
       inset 12px from top/bottom, at 65% softness (baked into the stops,
       because a background layer has no opacity of its own). Kept as a
       background layer so it stays pinned while the drawer content scrolls. */
    background:
      linear-gradient(180deg,
        color-mix(in srgb, var(--color-primary) 65%, transparent),
        color-mix(in srgb, var(--color-on) 65%, transparent) 33%,
        color-mix(in srgb, var(--color-standby) 65%, transparent) 66%,
        color-mix(in srgb, var(--color-sleep) 65%, transparent))
        0 12px / 2px calc(100% - 24px) no-repeat,
      var(--bg-surface-solid);
    border-left: 1px solid var(--color-border);
    /* Rounded left edge; the right side stays flush with the window. */
    border-radius: var(--radius-lg) 0 0 var(--radius-lg);
    padding: 1rem 1rem 1.25rem;
    overflow: auto;
    box-shadow: var(--shadow-lg);
  }
  section {
    border-top: 1px solid var(--color-border);
    padding-top: 0.85rem;
    margin-top: 0.85rem;
    /* Staggered reveal timed with the drawer's 320ms entry (--dur-3). */
    animation: rise var(--dur-3) var(--ease) backwards;
  }
  section:nth-of-type(1) { animation-delay: 60ms; }
  section:nth-of-type(2) { animation-delay: 110ms; }
  section:nth-of-type(3) { animation-delay: 160ms; }
  section:nth-of-type(4) { animation-delay: 210ms; }
  h4 {
    margin: 0 0 0.55rem;
    display: flex;
    align-items: center;
    gap: 0.35rem;
    font-size: var(--fs-micro);
    font-weight: 800;
    text-transform: uppercase;
    letter-spacing: 0.08em;
    color: var(--text-muted);
  }
  h4::before {
    content: '';
    width: 3px;
    height: 11px;
    border-radius: 2px;
    background: linear-gradient(180deg, var(--color-primary), var(--color-sleep));
  }
  .def-list { margin: 0.5rem 0; }
  .state-text { font-weight: 800; text-transform: capitalize; }
  .state-text-on { color: var(--color-on-deep); }
  .state-text-standby { color: var(--color-standby-deep); }
  .state-text-sleep { color: var(--color-sleep-deep); }
  .state-text-booting { color: var(--color-booting-deep); }
  .drawer-actions {
    display: flex;
    align-items: center;
    gap: 0.3rem;
    /* Wrap instead of squeezing: the three labels exceed the 390px drawer's
       content width on some fonts/DPI, and shrinking nowrap buttons would
       spill text past their boxes. */
    flex-wrap: wrap;
    margin-top: 0.8rem;
  }
  .drawer-actions .btn {
    flex: 0 0 auto;
    min-height: 30px;
    gap: 0.25rem;
    padding: 0.35rem 0.45rem;
    font-size: var(--fs-micro);
    white-space: nowrap;
  }
  .drawer-actions .btn :global(svg) { flex: 0 0 auto; }
  .warning-text { color: var(--color-warning-deep); font-weight: 700; }
  .capability-groups { display: flex; flex-direction: column; gap: 0.55rem; }
  .capability-group { display: flex; align-items: baseline; gap: 0.5rem; }
  .group-label { min-width: 3.6rem; font-size: var(--fs-micro); font-weight: 800; color: var(--text-muted); text-transform: uppercase; letter-spacing: 0.06em; }
  .capabilities { display: flex; flex-wrap: wrap; gap: 0.35rem; }
  .capabilities span {
    display: inline-flex;
    align-items: center;
    gap: 0.2rem;
    font-size: var(--fs-micro);
    font-weight: 700;
    padding: 0.2rem 0.45rem;
    border: 1px solid var(--color-border);
    border-radius: var(--radius-pill);
    background: var(--bg-input);
    color: var(--text-muted);
  }
  .capabilities span.supported {
    color: var(--fb-success);
    border-color: color-mix(in srgb, var(--color-on) 42%, transparent);
    background: color-mix(in srgb, var(--color-on) 10%, white);
  }
</style>
