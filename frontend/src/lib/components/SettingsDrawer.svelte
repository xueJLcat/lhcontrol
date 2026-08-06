<script lang="ts">
  import { cubicOut } from 'svelte/easing';
  import { fly } from 'svelte/transition';
  import { Bluetooth, CircleAlert, LoaderCircle, RefreshCw, X } from 'lucide-svelte';
  import type { bluetooth } from '../../../wailsjs/go/models';
  import { focusTrap } from '../actions';
  import { dur } from '../motion';

  let {
    adapters,
    loading,
    loadError,
    selectedDeviceId,
    busy,
    inactive = false,
    onClose,
    onRefresh,
    onSelect
  }: {
    adapters: bluetooth.AdapterInfo[];
    loading: boolean;
    loadError: string | null;
    selectedDeviceId: string;
    busy: boolean;
    inactive?: boolean;
    onClose: () => void;
    onRefresh: () => void;
    onSelect: (deviceId: string) => void;
  } = $props();

  // A persisted selection may point at a radio that is currently detached
  // (USB dongle unplugged). Keep it visible so the user can see and replace
  // it instead of silently snapping back to the default.
  const selectedMissing = $derived(
    selectedDeviceId !== '' && !adapters.some((adapter) => adapter.deviceId === selectedDeviceId)
  );
</script>

<div
  class="drawer"
  role="dialog"
  aria-modal={inactive ? undefined : 'true'}
  aria-hidden={inactive ? 'true' : undefined}
  inert={inactive}
  aria-label="Settings"
  tabindex="-1"
  use:focusTrap
  in:fly={dur({ x: 64, duration: 320, easing: cubicOut })}
  out:fly={dur({ x: 64, duration: 180 })}
>
  <div class="drawer-head">
    <div>
      <small>Settings</small>
      <div class="drawer-title"><h2>Preferences</h2></div>
    </div>
    <button type="button" class="icon-btn" title="Close" aria-label="Close settings" onclick={onClose}><X size={18} /></button>
  </div>

  <section>
    <h4><Bluetooth size={12} /> Bluetooth adapter</h4>
    {#if loading}
      <p class="hint loading"><LoaderCircle class="spin" size={14} /> Detecting Bluetooth adapters...</p>
    {:else if loadError}
      <div class="alert danger">{loadError}</div>
      <div class="drawer-actions">
        <button class="btn" onclick={onRefresh}><RefreshCw size={15} /> Retry</button>
      </div>
    {:else}
      <div class="adapter-list" role="radiogroup" aria-label="Preferred Bluetooth adapter">
        <label class="adapter-option" class:selected={selectedDeviceId === ''}>
          <input
            type="radio"
            name="bluetooth-adapter"
            value=""
            checked={selectedDeviceId === ''}
            disabled={busy}
            onchange={() => onSelect('')}
          />
          <span class="adapter-text">
            <span class="adapter-name">Default</span>
            <span class="adapter-desc">No preference — use the system-wide scan</span>
          </span>
        </label>
        {#if selectedMissing}
          <label class="adapter-option selected missing">
            <input
              type="radio"
              name="bluetooth-adapter"
              value={selectedDeviceId}
              checked
              disabled={busy}
            />
            <span class="adapter-text">
              <span class="adapter-name"><CircleAlert size={12} /> Not currently detected</span>
              <span class="adapter-desc">The saved adapter is unavailable right now</span>
              <span class="adapter-id">{selectedDeviceId}</span>
            </span>
          </label>
        {/if}
        {#each adapters as adapter (adapter.deviceId)}
          <label class="adapter-option" class:selected={selectedDeviceId === adapter.deviceId}>
            <input
              type="radio"
              name="bluetooth-adapter"
              value={adapter.deviceId}
              checked={selectedDeviceId === adapter.deviceId}
              disabled={busy}
              onchange={() => onSelect(adapter.deviceId)}
            />
            <span class="adapter-text">
              <span class="adapter-name">{adapter.name}</span>
              <span class="adapter-id">{adapter.deviceId}</span>
            </span>
          </label>
        {:else}
          <p class="hint">No Bluetooth adapters were detected on this system.</p>
        {/each}
      </div>
      <div class="drawer-actions">
        <button class="btn" onclick={onRefresh} disabled={busy || loading}>
          {#if loading}<LoaderCircle class="spin" size={15} />{:else}<RefreshCw size={15} />{/if}
          Refresh adapters
        </button>
      </div>
      <p class="hint">
        The selection is saved and restored on the next start. Windows performs
        Bluetooth discovery across all radios at once, so this preference
        cannot restrict the scan to a single adapter; it is kept for systems
        and future versions that support radio selection.
      </p>
    {/if}
  </section>
</div>

<style>
  .drawer {
    position: fixed;
    z-index: 11;
    right: 0; top: 0; bottom: 0;
    width: min(390px, 92vw);
    background:
      linear-gradient(180deg,
        color-mix(in srgb, var(--color-primary) 65%, transparent),
        color-mix(in srgb, var(--color-on) 65%, transparent) 33%,
        color-mix(in srgb, var(--color-standby) 65%, transparent) 66%,
        color-mix(in srgb, var(--color-sleep) 65%, transparent))
        0 12px / 2px calc(100% - 24px) no-repeat,
      var(--bg-surface-solid);
    border-left: 1px solid var(--color-border);
    border-radius: var(--radius-lg) 0 0 var(--radius-lg);
    padding: 1rem 1rem 1.25rem;
    overflow: auto;
    box-shadow: var(--shadow-lg);
  }
  section {
    border-top: 1px solid var(--color-border);
    padding-top: 0.85rem;
    margin-top: 0.85rem;
    animation: rise var(--dur-3) var(--ease) backwards;
    animation-delay: 60ms;
  }
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
  .adapter-list { display: flex; flex-direction: column; gap: 0.4rem; }
  .adapter-option {
    display: flex;
    align-items: flex-start;
    gap: 0.55rem;
    padding: 0.5rem 0.6rem;
    border: 1px solid var(--color-border);
    border-radius: var(--radius-md);
    background: var(--bg-input);
    cursor: pointer;
    transition:
      border-color var(--dur-1) var(--ease),
      background-color var(--dur-1) var(--ease),
      box-shadow var(--dur-1) var(--ease);
  }
  .adapter-option:hover { border-color: var(--color-border-strong); }
  .adapter-option.selected {
    border-color: color-mix(in srgb, var(--color-primary) 45%, transparent);
    background: color-mix(in srgb, var(--color-primary) 7%, white);
    box-shadow: var(--shadow-sm);
  }
  .adapter-option.missing {
    border-color: color-mix(in srgb, var(--color-warning-deep) 55%, transparent);
    background: color-mix(in srgb, var(--color-warning-deep) 8%, white);
  }
  .adapter-option input { margin-top: 0.2rem; accent-color: var(--color-primary); flex-shrink: 0; }
  .adapter-option:has(input:disabled) { cursor: wait; }
  .adapter-text { display: flex; flex-direction: column; gap: 0.1rem; min-width: 0; }
  .adapter-name {
    display: inline-flex;
    align-items: center;
    gap: 0.3rem;
    font-size: var(--fs-sm);
    font-weight: 700;
    color: var(--text-primary);
  }
  .adapter-desc { font-size: var(--fs-micro); font-weight: 600; color: var(--text-muted); }
  .adapter-id {
    font-family: var(--font-mono);
    font-size: 0.65rem;
    color: var(--text-muted);
    overflow-wrap: anywhere;
  }
  .drawer-actions { display: flex; flex-wrap: wrap; gap: 0.3rem; margin-top: 0.8rem; }
  .drawer-actions .btn {
    flex: 0 0 auto;
    min-height: 30px;
    gap: 0.25rem;
    padding: 0.35rem 0.55rem;
    font-size: var(--fs-micro);
    white-space: nowrap;
  }
  .hint {
    margin: 0.6rem 0 0;
    font-size: var(--fs-micro);
    font-weight: 600;
    color: var(--text-muted);
    line-height: 1.5;
  }
  .hint.loading { display: flex; align-items: center; gap: 0.4rem; }
</style>
