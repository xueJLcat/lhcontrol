<script lang="ts">
  import { onMount } from 'svelte';
  import { cubicOut } from 'svelte/easing';
  import { fly } from 'svelte/transition';
  import { CircleCheck, Eye, RefreshCw, Settings2, X } from 'lucide-svelte';
  import type { Capabilities, StationInfo } from '../types';
  import { channelChangeBlockedReason, stateLabel } from '../station';
  import { relativeTime } from '../relative-time';
  import { focusTrap } from '../actions';
  import StateBadge from './StateBadge.svelte';

  export let station: StationInfo;
  export let busy: boolean;
  export let locked: boolean;
  export let inactive = false;
  export let onClose: () => void;
  export let onRefresh: (station: StationInfo) => void;
  export let onIdentify: (station: StationInfo) => void;
  export let onOpenChannelEditor: (station: StationInfo) => void;

  let now = Date.now();

  onMount(() => {
    const timer = setInterval(() => {
      now = Date.now();
    }, 30000);
    return () => clearInterval(timer);
  });

  const capabilityGroups: { label: string; entries: [keyof Capabilities, string][] }[] = [
    { label: 'Power', entries: [['powerRead', 'read'], ['powerWrite', 'write'], ['powerNotify', 'notify'], ['standby', 'standby']] },
    { label: 'Channel', entries: [['channelRead', 'read'], ['channelWrite', 'write'], ['channelNotify', 'notify']] },
    { label: 'Other', entries: [['identify', 'identify'], ['deviceInformation', 'device info']] }
  ];

  $: hasCachedMetadata = Boolean(
    station.metadataReadAt ||
    station.metadata.manufacturer ||
    station.metadata.model ||
    station.metadata.serialNumber ||
    station.metadata.hardwareRevision ||
    station.metadata.firmwareRevision
  );
  $: metadataStatus = station.metadataFresh
    ? 'loaded and fresh'
    : hasCachedMetadata ? 'cached, stale' : 'unavailable';
  $: channelBlockedReason = channelChangeBlockedReason(station);
</script>

<div
  class="drawer"
  role="dialog"
  aria-modal={inactive ? undefined : 'true'}
  aria-hidden={inactive ? 'true' : undefined}
  inert={inactive}
  aria-label="Station details"
  tabindex="-1"
  use:focusTrap
  in:fly={{ x: 64, duration: 280, easing: cubicOut }}
  out:fly={{ x: 64, duration: 180 }}
>
  <div class="drawer-head">
    <div>
      <small>Station details</small>
      <div class="drawer-title">
        <h2>{station.name}</h2>
        <StateBadge
          label={stateLabel(station)}
          unverified={station.powerState >= 0 && station.powerFresh && !station.powerStateConfirmed}
          booting={station.powerFresh && station.powerState === 3}
        />
      </div>
    </div>
    <button class="icon-btn" title="Close" on:click={onClose}><X size={18} /></button>
  </div>

  <section>
    <h4>Status</h4>
    <dl>
      <dt>Power</dt><dd><span class="state-text state-text-{stateLabel(station)}">{stateLabel(station)}</span> · {station.powerFresh ? station.powerStateConfirmed ? 'confirmed' : 'unverified' : 'last known, stale'} (raw {station.rawPowerState})</dd>
      <dt>Channel</dt><dd class="mono">{station.channel || 'Unable to verify'}</dd>
      <dt>Connection</dt><dd>{station.connectionState}</dd>
      <dt>Last seen</dt><dd title={station.lastSeenAt || undefined}>{relativeTime(station.lastSeenAt, now) || '—'}</dd>
      <dt>Last status read</dt><dd title={station.lastReadAt || undefined}>{relativeTime(station.lastReadAt, now) || '—'}</dd>
      <dt>Power data</dt><dd title={station.lastPowerReadAt || undefined}>{station.powerFresh ? 'fresh' : 'stale or unavailable'} · {relativeTime(station.lastPowerReadAt, now) || 'never'}</dd>
      <dt>Channel data</dt><dd title={station.lastChannelReadAt || undefined}>{station.channelFresh ? 'fresh' : 'stale or unavailable'} · {relativeTime(station.lastChannelReadAt, now) || 'never'}</dd>
    </dl>
    {#if station.lastError}<div class="alert danger">{station.lastError}</div>{/if}
    {#if !station.capabilitiesKnown}
      <div class="alert">Capabilities could not be verified. Power commands will retry discovery; unsupported operations will be reported.</div>
    {/if}
    <div class="drawer-actions">
      <button class="btn" on:click={() => onRefresh(station)} disabled={busy || locked}><RefreshCw size={15} /> Refresh capabilities</button>
      <button class="btn" on:click={() => onIdentify(station)} disabled={busy || locked} title={station.capabilities.identify ? 'Send the identify signal' : 'Recheck support and identify'}><Eye size={15} /> Identify</button>
      <button
        class="btn primary"
        on:click={() => onOpenChannelEditor(station)}
        disabled={Boolean(channelBlockedReason) || busy || locked}
        title={channelBlockedReason || 'Recheck support and change Channel safely'}
      ><Settings2 size={15} /> Change Channel</button>
    </div>
    {#if station.capabilities.identify}<p class="hint">Identify may wake a sleeping base station.</p>{/if}
    {#if channelBlockedReason}<p class="hint warning-text">{channelBlockedReason}</p>{/if}
  </section>

  <section>
    <h4>Identity</h4>
    <dl>
      <dt>Original name</dt><dd>{station.originalName}</dd>
      <dt>Address</dt><dd class="mono">{station.address}</dd>
      <dt>Metadata</dt><dd title={station.metadataReadAt || undefined}>{metadataStatus} · {relativeTime(station.metadataReadAt, now) || 'never'}</dd>
    </dl>
  </section>

  <section>
    <h4>Device information</h4>
    <dl>
      <dt>Manufacturer</dt><dd>{station.metadata.manufacturer || '—'}</dd>
      <dt>Model</dt><dd>{station.metadata.model || '—'}</dd>
      <dt>Serial number</dt><dd class="mono">{station.metadata.serialNumber || '—'}</dd>
      <dt>Hardware</dt><dd class="mono">{station.metadata.hardwareRevision || '—'}</dd>
      <dt>Firmware</dt><dd class="mono">{station.metadata.firmwareRevision || '—'}</dd>
    </dl>
  </section>

  <section>
    <h4>Capabilities</h4>
    <div class="capability-groups">
      {#each capabilityGroups as group}
        <div class="capability-group">
          <span class="group-label">{group.label}</span>
          <div class="capabilities">
            {#each group.entries as [key, label]}
              <span class:supported={station.capabilitiesKnown && station.capabilities[key]}>
                {#if station.capabilitiesKnown && station.capabilities[key]}<CircleCheck size={10} />{/if}{label}
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
    /* Colorful signature edge pinned to the box so it does not scroll
       away with the drawer content. */
    background:
      linear-gradient(180deg, var(--color-primary), var(--color-on), var(--color-standby), var(--color-sleep))
        0 0 / 3px 100% no-repeat,
      var(--bg-surface-solid);
    border-left: 1px solid var(--color-border);
    padding: 1rem 1rem 1.25rem;
    overflow: auto;
    box-shadow: var(--shadow-lg);
  }
  .drawer-head { display: flex; align-items: center; justify-content: space-between; gap: 0.5rem; }
  .drawer-head small {
    color: var(--color-primary-deep);
    font-size: var(--fs-micro);
    font-weight: 800;
    text-transform: uppercase;
    letter-spacing: 0.08em;
  }
  .drawer-head h2 { margin: 0.1rem 0 0; font-size: var(--fs-h2); font-weight: 800; color: var(--text-primary); overflow-wrap: anywhere; }
  .drawer-title { display: flex; align-items: center; gap: 0.5rem; flex-wrap: wrap; }
  section { border-top: 1px solid var(--color-border); padding-top: 0.85rem; margin-top: 0.85rem; }
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
  dl { display: grid; grid-template-columns: 7.5rem minmax(0, 1fr); gap: 0.42rem; margin: 0.5rem 0; font-size: var(--fs-sm); }
  dt { color: var(--text-muted); font-weight: 600; }
  dd { margin: 0; color: var(--text-primary); font-weight: 700; overflow-wrap: anywhere; }
  .state-text { font-weight: 800; text-transform: capitalize; }
  .state-text-on { color: var(--color-on-deep); }
  .state-text-standby { color: var(--color-standby-deep); }
  .state-text-sleep { color: var(--color-sleep-deep); }
  .state-text-booting { color: var(--color-booting-deep); }
  .drawer-actions {
    display: flex;
    align-items: center;
    gap: 0.3rem;
    flex-wrap: nowrap;
    margin-top: 0.8rem;
  }
  .drawer-actions .btn {
    flex: 0 1 auto;
    min-width: 0;
    min-height: 30px;
    gap: 0.25rem;
    padding: 0.35rem 0.45rem;
    font-size: var(--fs-micro);
    white-space: nowrap;
  }
  .drawer-actions .btn :global(svg) { flex: 0 0 auto; }
  .hint { font-size: var(--fs-sm); color: var(--text-muted); line-height: 1.45; }
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
