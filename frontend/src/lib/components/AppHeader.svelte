<script lang="ts">
  import { onMount } from 'svelte';
  import { fade } from 'svelte/transition';
  import { LoaderCircle, RefreshCw, Square, Zap } from 'lucide-svelte';
  import type { PowerTarget } from '../types';
  import logo from '../../assets/images/logo-universal.png';

  export let scanning: boolean;
  export let isBulkLoading: boolean;
  export let scanLocked: boolean;
  export let bulkLocked: boolean;
  export let bulkTarget: PowerTarget | null;
  export let canOn: boolean;
  export let canStandby: boolean;
  export let canSleep: boolean;
  export let allOn: boolean;
  export let allStandby: boolean;
  export let allSleep: boolean;
  export let onCount = 0;
  export let standbyCount = 0;
  export let sleepCount = 0;
  export let onScan: () => void;
  export let onStop: () => void;
  export let stopping = false;
  export let onBulkPower: (state: PowerTarget) => void;

  $: fleetTotal = onCount + standbyCount + sleepCount;

  // Bulk segment thumb + confirmation pop. allOn/allStandby/allSleep are
  // mutually exclusive, so a single index describes the active segment.
  $: bulkActiveIndex = allOn ? 0 : allStandby ? 1 : allSleep ? 2 : -1;

  let mounted = false;
  let bulkPopIndex = -1;
  let bulkPopTimer: ReturnType<typeof setTimeout> | null = null;
  let prevAllOn = false;
  let prevAllStandby = false;
  let prevAllSleep = false;
  onMount(() => {
    mounted = true;
  });
  $: {
    const previousIndex = prevAllOn ? 0 : prevAllStandby ? 1 : prevAllSleep ? 2 : -1;
    if (mounted && bulkActiveIndex !== -1 && bulkActiveIndex !== previousIndex) {
      bulkPopIndex = bulkActiveIndex;
      if (bulkPopTimer) clearTimeout(bulkPopTimer);
      bulkPopTimer = setTimeout(() => {
        bulkPopIndex = -1;
        bulkPopTimer = null;
      }, 700);
    }
    prevAllOn = allOn;
    prevAllStandby = allStandby;
    prevAllSleep = allSleep;
  }
</script>

<header>
  <div class="row-top">
    <div class="brand">
      <span class="brand-logo"><img src={logo} alt="" /></span>
      <div class="brand-text">
        <h1>Lighthouse Control</h1>
        <span>SteamVR base stations</span>
      </div>
    </div>
    {#if fleetTotal > 0}
      <div class="fleet-summary" aria-label="Fleet power summary">
        {#if onCount > 0}<span class="fleet-chip chip-on" in:fade={{ duration: 160 }}><span class="fleet-dot dot-on"></span>{onCount} On</span>{/if}
        {#if standbyCount > 0}<span class="fleet-chip chip-standby" in:fade={{ duration: 160 }}><span class="fleet-dot dot-standby"></span>{standbyCount} Standby</span>{/if}
        {#if sleepCount > 0}<span class="fleet-chip chip-sleep" in:fade={{ duration: 160 }}><span class="fleet-dot dot-sleep"></span>{sleepCount} Sleep</span>{/if}
      </div>
    {/if}
  </div>
  <div class="row-actions">
    <button class="btn primary scan-btn" on:click={scanning ? onStop : onScan} disabled={scanning ? stopping : scanLocked}>
      {#if stopping}<LoaderCircle class="spin" size={15} /> Stopping...
      {:else if scanning}<Square size={15} /> Stop
      {:else}<RefreshCw size={15} /> Scan{/if}
    </button>
    <div class="bulk-power" aria-label="Set all known stations">
      <span class="bulk-label">{#if isBulkLoading}<LoaderCircle class="spin" size={12} />{:else}<Zap size={12} />{/if} All</span>
      <div class="bulk-seg" class:pop={bulkPopIndex >= 0}>
        <div
          class="seg-thumb"
          class:seg-thumb-on={bulkActiveIndex === 0}
          class:seg-thumb-standby={bulkActiveIndex === 1}
          class:seg-thumb-sleep={bulkActiveIndex === 2}
          style:transform={`translateX(${bulkActiveIndex * 100}%)`}
          style:opacity={bulkActiveIndex >= 0 ? 1 : 0}
          aria-hidden="true"
        ></div>
        <button class="seg-on" class:pending={bulkTarget === 'on'} class:active={allOn} on:click={() => onBulkPower('on')} disabled={scanning || bulkLocked || !canOn} title={!canOn ? 'No actionable station' : bulkLocked ? 'Bluetooth operation in progress' : 'Turn all known stations on'}>On</button>
        <button class="seg-standby" class:pending={bulkTarget === 'standby'} class:active={allStandby} on:click={() => onBulkPower('standby')} disabled={scanning || bulkLocked || !canStandby} title={!canStandby ? 'No actionable station' : bulkLocked ? 'Bluetooth operation in progress' : 'Set all known stations to standby'}>Standby</button>
        <button class="seg-sleep" class:pending={bulkTarget === 'sleep'} class:active={allSleep} on:click={() => onBulkPower('sleep')} disabled={scanning || bulkLocked || !canSleep} title={!canSleep ? 'No actionable station' : bulkLocked ? 'Bluetooth operation in progress' : 'Put all known stations to sleep'}>Sleep</button>
      </div>
    </div>
  </div>
  {#if scanning}<div class="scan-progress" aria-hidden="true"></div>{/if}
</header>

<style>
  header {
    position: relative;
    display: flex;
    flex-direction: column;
    gap: 0.6rem;
    padding: 0.75rem var(--spacing-md) 0.7rem;
    background: var(--bg-surface);
    backdrop-filter: blur(14px);
    border-bottom: 1px solid var(--color-border);
  }
  /* Colorful signature hairline. The gradient carries the full palette
     twice (ending on the first color), and the 200%-wide image slides
     exactly one palette-width per loop, so the full spectrum is always
     visible and the loop is seamless. */
  header::before {
    content: '';
    position: absolute;
    top: 0;
    left: 0;
    right: 0;
    height: 3px;
    background: linear-gradient(90deg, var(--color-primary), var(--color-on), var(--color-standby), var(--color-sleep), var(--color-danger), var(--color-primary));
    background-size: 200% 100%;
    animation: shift 14s linear infinite;
  }
  @keyframes shift {
    from { background-position: 0% 0; }
    to { background-position: 100% 0; }
  }
  .row-top { display: flex; align-items: center; justify-content: space-between; gap: 0.6rem; min-width: 0; }
  .row-actions { display: flex; align-items: stretch; gap: 0.55rem; }
  .brand { display: flex; align-items: center; gap: 0.55rem; min-width: 0; }
  .brand-logo {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 34px;
    height: 34px;
    flex-shrink: 0;
    border-radius: var(--radius-sm);
    background: linear-gradient(135deg, color-mix(in srgb, var(--color-primary) 14%, white), color-mix(in srgb, var(--color-sleep) 16%, white));
    border: 1px solid color-mix(in srgb, var(--color-primary) 22%, transparent);
    box-shadow: var(--shadow-sm);
  }
  .brand-logo img { width: 24px; height: 24px; border-radius: calc(var(--radius-sm) - 4px); }
  .brand-text { display: flex; flex-direction: column; line-height: 1.15; min-width: 0; }
  .brand-text h1 {
    margin: 0;
    font-size: 0.95rem;
    font-weight: 800;
    letter-spacing: 0.01em;
    color: var(--text-primary);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .brand-text span { font-size: var(--fs-micro); font-weight: 700; color: var(--text-muted); text-transform: uppercase; letter-spacing: 0.08em; }
  /* Fixed width covers the longest state ("Stopping...") so the bulk-power
     group on the right never shifts when the scan state changes. */
  .scan-btn { min-width: 118px; flex-shrink: 0; }
  .bulk-power { flex: 1; min-width: 0; }
  .bulk-seg { flex: 1; display: flex; min-width: 0; }
  .bulk-power button { flex: 1; min-width: 0; }
  .fleet-summary {
    display: flex;
    align-items: center;
    gap: 0.35rem;
    font-size: var(--fs-micro);
    font-weight: 800;
    white-space: nowrap;
    flex-shrink: 0;
  }
  .fleet-chip {
    display: inline-flex;
    align-items: center;
    gap: 0.28rem;
    padding: 0.14rem 0.5rem;
    border-radius: var(--radius-pill);
    border: 1px solid var(--color-border);
    background: var(--bg-surface-solid);
    box-shadow: var(--shadow-sm);
  }
  .fleet-chip.chip-on { color: var(--color-on-deep); border-color: color-mix(in srgb, var(--color-on) 40%, transparent); background: linear-gradient(135deg, color-mix(in srgb, var(--color-on) 14%, white), color-mix(in srgb, var(--color-on) 6%, white)); }
  .fleet-chip.chip-standby { color: var(--color-standby-deep); border-color: color-mix(in srgb, var(--color-standby) 40%, transparent); background: linear-gradient(135deg, color-mix(in srgb, var(--color-standby) 14%, white), color-mix(in srgb, var(--color-standby) 6%, white)); }
  .fleet-chip.chip-sleep { color: var(--color-sleep-deep); border-color: color-mix(in srgb, var(--color-sleep) 38%, transparent); background: linear-gradient(135deg, color-mix(in srgb, var(--color-sleep) 13%, white), color-mix(in srgb, var(--color-sleep) 6%, white)); }
  .fleet-dot { width: 6px; height: 6px; border-radius: var(--radius-pill); flex-shrink: 0; }
  .fleet-dot.dot-on { background: var(--color-on); box-shadow: 0 0 5px color-mix(in srgb, var(--color-on) 70%, transparent); }
  .fleet-dot.dot-standby { background: var(--color-standby); box-shadow: 0 0 5px color-mix(in srgb, var(--color-standby) 70%, transparent); }
  .fleet-dot.dot-sleep { background: var(--color-sleep); box-shadow: 0 0 5px color-mix(in srgb, var(--color-sleep) 60%, transparent); }
  .scan-progress {
    position: absolute;
    left: 0; right: 0; bottom: -1px;
    height: 2px;
    overflow: hidden;
  }
  .scan-progress::after {
    content: '';
    position: absolute;
    top: 0; bottom: 0;
    left: 0;
    width: 38%;
    border-radius: var(--radius-pill);
    background: linear-gradient(90deg, transparent, var(--color-primary), var(--color-sleep), transparent);
    /* transform-based slide: compositor-friendly, no per-frame layout. */
    animation: scan-slide 1.2s var(--ease) infinite;
  }
  @keyframes scan-slide {
    from { transform: translateX(-110%); }
    to { transform: translateX(275%); }
  }
</style>
