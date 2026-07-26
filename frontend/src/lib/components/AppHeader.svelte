<script lang="ts">
  import { LoaderCircle, RefreshCw, Zap } from 'lucide-svelte';
  import type { PowerTarget } from '../types';
  import logo from '../../assets/images/logo-universal.png';

  export let isLoading: boolean;
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
  export let onScan: () => void;
  export let onBulkPower: (state: PowerTarget) => void;
</script>

<header>
  <div class="brand">
    <img src={logo} alt="" />
    <div class="brand-text">
      <h1>Lighthouse Control</h1>
      <span>SteamVR base stations</span>
    </div>
  </div>
  <div class="global-controls">
    <button class="btn primary scan-btn" on:click={onScan} disabled={isLoading || scanLocked}>
      {#if isLoading}<LoaderCircle class="spin" size={15} /> Scanning...{:else}<RefreshCw size={15} /> Scan{/if}
    </button>
    <div class="bulk-power" aria-label="Set all visible stations">
      <span class="bulk-label">{#if isBulkLoading}<LoaderCircle class="spin" size={12} />{:else}<Zap size={12} />{/if} All</span>
      <button class="seg-on" class:pending={bulkTarget === 'on'} class:active={allOn} on:click={() => onBulkPower('on')} disabled={isLoading || bulkLocked || !canOn} title={!canOn ? 'No actionable station' : bulkLocked ? 'Bluetooth operation in progress' : 'Turn all visible stations on'}>On</button>
      <button class="seg-standby" class:pending={bulkTarget === 'standby'} class:active={allStandby} on:click={() => onBulkPower('standby')} disabled={isLoading || bulkLocked || !canStandby} title={!canStandby ? 'No actionable station' : bulkLocked ? 'Bluetooth operation in progress' : 'Set all visible stations to standby'}>Standby</button>
      <button class="seg-sleep" class:pending={bulkTarget === 'sleep'} class:active={allSleep} on:click={() => onBulkPower('sleep')} disabled={isLoading || bulkLocked || !canSleep} title={!canSleep ? 'No actionable station' : bulkLocked ? 'Bluetooth operation in progress' : 'Put all visible stations to sleep'}>Sleep</button>
    </div>
  </div>
  {#if isLoading}<div class="scan-progress" aria-hidden="true"></div>{/if}
</header>

<style>
  header {
    position: relative;
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: var(--spacing-sm);
    flex-wrap: wrap;
    row-gap: var(--spacing-xs);
    padding: 0.55rem var(--spacing-md);
    background: var(--bg-surface);
    backdrop-filter: blur(12px);
    border-bottom: 1px solid var(--color-border);
  }
  .brand { display: flex; align-items: center; gap: 0.5rem; min-width: 0; }
  .brand img { width: 26px; height: 26px; border-radius: var(--radius-sm); }
  .brand-text { display: flex; flex-direction: column; line-height: 1.15; }
  .brand-text h1 { margin: 0; font-size: 0.95rem; font-weight: 800; letter-spacing: 0.01em; color: var(--text-primary); }
  .brand-text span { font-size: 0.66rem; font-weight: 600; color: var(--text-muted); text-transform: uppercase; letter-spacing: 0.08em; }
  .global-controls { display: flex; align-items: center; gap: 0.45rem; flex-wrap: wrap; }
  .scan-btn { min-width: 84px; }
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
    border-radius: 999px;
    background: linear-gradient(90deg, transparent, var(--color-primary), transparent);
    animation: scan-slide 1.2s var(--ease) infinite;
  }
  @keyframes scan-slide {
    from { left: -38%; }
    to { left: 100%; }
  }
</style>
