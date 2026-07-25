<script lang="ts">
  import { cubicOut } from 'svelte/easing';
  import { fly } from 'svelte/transition';
  import { Eye, RefreshCw, Settings2, X } from 'lucide-svelte';
  import type { Capabilities, StationInfo } from '../types';
  import { stateLabel } from '../station';
  import { focusTrap } from '../actions';

  export let station: StationInfo;
  export let busy: boolean;
  export let locked: boolean;
  export let onClose: () => void;
  export let onRefresh: (station: StationInfo) => void;
  export let onIdentify: (station: StationInfo) => void;
  export let onOpenChannelEditor: (station: StationInfo) => void;

  const capabilityGroups: { label: string; entries: [keyof Capabilities, string][] }[] = [
    { label: 'Power', entries: [['powerRead', 'read'], ['powerWrite', 'write'], ['powerNotify', 'notify'], ['standby', 'standby']] },
    { label: 'Channel', entries: [['channelRead', 'read'], ['channelWrite', 'write'], ['channelNotify', 'notify']] },
    { label: 'Other', entries: [['identify', 'identify'], ['deviceInformation', 'device info']] }
  ];
</script>

<aside
  class="drawer"
  aria-label="Station details"
  use:focusTrap
  in:fly={{ x: 64, duration: 280, easing: cubicOut }}
  out:fly={{ x: 64, duration: 180 }}
>
  <div class="drawer-head">
    <div><small>Station details</small><h2>{station.name}</h2></div>
    <button class="icon-btn" title="Close" on:click={onClose}><X size={18} /></button>
  </div>

  <section>
    <h4>Identity</h4>
    <dl>
      <dt>Original name</dt><dd>{station.originalName}</dd>
      <dt>Address</dt><dd class="mono">{station.address}</dd>
      <dt>Power</dt><dd>{stateLabel(station)} · {station.powerStateConfirmed ? 'confirmed' : 'unverified'} (raw {station.rawPowerState})</dd>
      <dt>Channel</dt><dd class="mono">{station.channel || 'Unable to verify'}</dd>
      <dt>Connection</dt><dd>{station.connectionState}</dd>
      <dt>Last seen</dt><dd>{station.lastSeenAt || '—'}</dd>
      <dt>Last status read</dt><dd>{station.lastReadAt || '—'}</dd>
      <dt>Power data</dt><dd>{station.powerFresh ? 'fresh' : 'stale or unavailable'} · {station.lastPowerReadAt || 'never'}</dd>
      <dt>Channel data</dt><dd>{station.channelFresh ? 'fresh' : 'stale or unavailable'} · {station.lastChannelReadAt || 'never'}</dd>
      <dt>Metadata</dt><dd>{station.metadataFresh ? 'loaded and cached' : 'unavailable'} · {station.metadataReadAt || 'never'}</dd>
    </dl>
    {#if station.lastError}<div class="alert danger">{station.lastError}</div>{/if}
    {#if !station.capabilitiesKnown}
      <div class="alert">Capabilities could not be verified. Refresh before using device controls.</div>
    {/if}
    <div class="drawer-actions">
      <button class="btn" on:click={() => onRefresh(station)} disabled={busy || locked}><RefreshCw size={15} /> Refresh capabilities</button>
      {#if station.capabilitiesKnown && station.capabilities.identify}
        <button class="btn" on:click={() => onIdentify(station)} disabled={busy || locked}><Eye size={15} /> Identify</button>
      {/if}
      {#if station.capabilitiesKnown && station.capabilities.channelRead && station.capabilities.channelWrite}
        <button class="btn primary" on:click={() => onOpenChannelEditor(station)} disabled={!station.scanFresh || busy || locked} title={station.scanFresh ? 'Change Channel safely' : 'Run a new scan before changing Channel'}><Settings2 size={15} /> Change Channel</button>
      {/if}
    </div>
    {#if station.capabilities.identify}<p class="hint">Identify may wake a sleeping base station.</p>{/if}
    {#if !station.scanFresh}<p class="hint warning-text">Run a new scan before changing Channel so conflicts are checked against the current room.</p>{/if}
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
              <span class:supported={station.capabilities[key]}>{label}</span>
            {/each}
          </div>
        </div>
      {/each}
    </div>
  </section>
</aside>

<style>
  .drawer {
    position: fixed;
    z-index: 11;
    right: 0; top: 0; bottom: 0;
    width: min(390px, 92vw);
    background: var(--bg-surface-solid);
    border-left: 1px solid var(--color-border);
    padding: 1rem;
    overflow: auto;
    box-shadow: var(--shadow-lg);
  }
  .drawer-head { display: flex; align-items: center; justify-content: space-between; gap: 0.5rem; }
  .drawer-head small {
    color: var(--text-muted);
    font-size: 0.66rem;
    font-weight: 800;
    text-transform: uppercase;
    letter-spacing: 0.08em;
  }
  .drawer-head h2 { margin: 0.1rem 0 0; font-size: 1.05rem; font-weight: 800; color: var(--text-primary); overflow-wrap: anywhere; }
  section { border-top: 1px solid var(--color-border); padding-top: 0.8rem; margin-top: 0.8rem; }
  h4 {
    margin: 0 0 0.55rem;
    font-size: 0.7rem;
    font-weight: 800;
    text-transform: uppercase;
    letter-spacing: 0.08em;
    color: var(--text-muted);
  }
  dl { display: grid; grid-template-columns: 7.5rem minmax(0, 1fr); gap: 0.4rem; margin: 0.5rem 0; font-size: 0.78rem; }
  dt { color: var(--text-muted); }
  dd { margin: 0; color: var(--text-primary); overflow-wrap: anywhere; }
  .drawer-actions { display: flex; align-items: center; gap: 0.5rem; flex-wrap: wrap; margin-top: 0.8rem; }
  .hint { font-size: 0.72rem; color: var(--text-muted); line-height: 1.45; }
  .warning-text { color: var(--color-warning); }
  .capability-groups { display: flex; flex-direction: column; gap: 0.55rem; }
  .capability-group { display: flex; align-items: baseline; gap: 0.5rem; }
  .group-label { min-width: 3.6rem; font-size: 0.68rem; font-weight: 700; color: var(--text-muted); text-transform: uppercase; letter-spacing: 0.06em; }
  .capabilities { display: flex; flex-wrap: wrap; gap: 0.35rem; }
  .capabilities span {
    font-size: 0.68rem;
    font-weight: 600;
    padding: 0.2rem 0.45rem;
    border: 1px solid var(--color-border);
    border-radius: 999px;
    color: var(--text-muted);
  }
  .capabilities span.supported { color: #86efac; border-color: rgba(52, 211, 153, 0.45); }
</style>
