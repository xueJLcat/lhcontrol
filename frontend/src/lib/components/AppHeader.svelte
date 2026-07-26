<script lang="ts">
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
</script>

<header>
  <div class="brand">
    <img src={logo} alt="" />
    <div class="brand-text">
      <h1>Lighthouse Control</h1>
      <span>SteamVR base stations</span>
    </div>
  </div>
  <button class="btn primary scan-btn" on:click={scanning ? onStop : onScan} disabled={scanning ? stopping : scanLocked}>
    {#if stopping}<LoaderCircle class="spin" size={15} /> Stopping...
    {:else if scanning}<Square size={13} /> Stop
    {:else}<RefreshCw size={15} /> Scan{/if}
  </button>
  <div class="bulk-power" aria-label="Set all known stations">
    <span class="bulk-label">{#if isBulkLoading}<LoaderCircle class="spin" size={12} />{:else}<Zap size={12} />{/if} All</span>
    <button class="seg-on" class:pending={bulkTarget === 'on'} class:active={allOn} on:click={() => onBulkPower('on')} disabled={scanning || bulkLocked || !canOn} title={!canOn ? 'No actionable station' : bulkLocked ? 'Bluetooth operation in progress' : 'Turn all known stations on'}>On</button>
    <button class="seg-standby" class:pending={bulkTarget === 'standby'} class:active={allStandby} on:click={() => onBulkPower('standby')} disabled={scanning || bulkLocked || !canStandby} title={!canStandby ? 'No actionable station' : bulkLocked ? 'Bluetooth operation in progress' : 'Set all known stations to standby'}>Standby</button>
    <button class="seg-sleep" class:pending={bulkTarget === 'sleep'} class:active={allSleep} on:click={() => onBulkPower('sleep')} disabled={scanning || bulkLocked || !canSleep} title={!canSleep ? 'No actionable station' : bulkLocked ? 'Bluetooth operation in progress' : 'Put all known stations to sleep'}>Sleep</button>
  </div>
  {#if fleetTotal > 0}
    <div class="fleet-summary" aria-label="Fleet power summary">
      {#if onCount > 0}<span class="fleet-chip"><span class="fleet-dot dot-on"></span>{onCount} On</span>{/if}
      {#if standbyCount > 0}<span class="fleet-chip"><span class="fleet-dot dot-standby"></span>{standbyCount} Standby</span>{/if}
      {#if sleepCount > 0}<span class="fleet-chip"><span class="fleet-dot dot-sleep"></span>{sleepCount} Sleep</span>{/if}
    </div>
  {/if}
  {#if scanning}<div class="scan-progress" aria-hidden="true"></div>{/if}
</header>

<style>
  header {
    position: relative;
    display: flex;
    align-items: center;
    flex-wrap: wrap;
    gap: 0.45rem 0.6rem;
    padding: 0.55rem var(--spacing-md);
    background: var(--bg-surface);
    backdrop-filter: blur(12px);
    border-bottom: 1px solid var(--color-border);
  }
  .brand { display: flex; align-items: center; gap: 0.5rem; min-width: 0; }
  .brand img { width: 26px; height: 26px; border-radius: var(--radius-sm); }
  .brand-text { display: flex; flex-direction: column; line-height: 1.15; }
  .brand-text h1 { margin: 0; font-size: 0.95rem; font-weight: 800; letter-spacing: 0.01em; color: var(--text-primary); }
  .brand-text span { font-size: var(--fs-micro); font-weight: 600; color: var(--text-muted); text-transform: uppercase; letter-spacing: 0.08em; }
  .scan-btn { min-width: 84px; margin-left: auto; }
  .fleet-summary {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    font-size: var(--fs-micro);
    font-weight: 700;
    color: var(--text-muted);
    white-space: nowrap;
  }
  .fleet-chip { display: inline-flex; align-items: center; gap: 0.25rem; }
  .fleet-dot { width: 6px; height: 6px; border-radius: var(--radius-pill); flex-shrink: 0; }
  .fleet-dot.dot-on { background: var(--color-on); box-shadow: 0 0 5px color-mix(in srgb, var(--color-on) 60%, transparent); }
  .fleet-dot.dot-standby { background: var(--color-standby); box-shadow: 0 0 5px color-mix(in srgb, var(--color-standby) 60%, transparent); }
  .fleet-dot.dot-sleep { background: var(--color-sleep); }
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
    width: 38%;
    border-radius: var(--radius-pill);
    background: linear-gradient(90deg, transparent, var(--color-primary), transparent);
    animation: scan-slide 1.2s var(--ease) infinite;
  }
  @keyframes scan-slide {
    from { left: -38%; }
    to { left: 100%; }
  }
</style>
