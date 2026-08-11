<script lang="ts">
  import type { StationInfo } from '../types';
  import { hasCurrentChannel, stateClass, stateLabel } from '../station';
  import { t } from '../i18n.svelte';

  let {
    stations,
    onSelect,
    selectedAddress = null,
    channelDisplayByAddress = new Map<string, number>()
  }: {
    stations: StationInfo[];
    onSelect: (address: string) => void;
    // Address of the station whose details drawer is open, so its channel
    // cell can stay highlighted while the user inspects the station.
    selectedAddress?: string | null;
    // Display-only channel snapshot. The parent injects a short-lived memory
    // so transient backend channel wipes do not flip cells between occupied
    // and free/disabled. `current` still uses the live station data, so a
    // resolved occupant without fresh data renders as last-known (stale).
    channelDisplayByAddress?: ReadonlyMap<string, number>;
  } = $props();

  interface Occupant {
    name: string;
    state: string;
    // Numeric-derived key for the CSS color variables: backend state labels
    // may carry arbitrary casing that would miss every selector.
    styleKey: string;
    address: string;
    current: boolean;
  }

  const byChannel = $derived(stations.reduce((map, station) => {
    const channel = channelDisplayByAddress.get(station.address) ?? station.channel;
    if (channel > 0) {
      const list = map.get(channel) ?? [];
      list.push({
        name: station.name,
        state: stateLabel(station),
        styleKey: stateClass(station),
        address: station.address,
        current: hasCurrentChannel(station)
      });
      list.sort((left, right) => Number(right.current) - Number(left.current));
      map.set(channel, list);
    }
    return map;
  }, new Map<number, Occupant[]>()));

  const conflictChannels = $derived(new Set(
    stations.filter((station) => station.channelConflict && hasCurrentChannel(station))
      .map((station) => station.channel)
  ));

  function cellLabel(channel: number, occupants: Occupant[]): string {
    if (!occupants.length) return t('CH {channel} — free', { channel });
    const names = occupants.map((occupant) =>
      occupant.current ? `${occupant.name} · ${occupant.state}` : t('{name} · {state} · last-known', { name: occupant.name, state: occupant.state })
    ).join(', ');
    return `CH ${channel} — ${names}`;
  }
</script>

<section class="cm-panel">
  <div class="cm-head">
    <h3>{t('Optical channels')}</h3>
    <div class="cm-legend" aria-hidden="true">
      <span class="lg lg-on">{t('On')}</span>
      <span class="lg lg-standby">{t('Standby')}</span>
      <span class="lg lg-sleep">{t('Sleep')}</span>
      <span class="lg lg-booting">{t('Booting')}</span>
    </div>
  </div>
  <div class="channel-map" role="group" aria-label={t('Channel occupancy')}>
    {#each Array.from({ length: 16 }, (_, index) => index + 1) as channel}
      {@const occupants = byChannel.get(channel) ?? []}
      <button
        type="button"
        class="cm-cell"
        class:occupied={occupants.length > 0}
        class:stale={occupants.length > 0 && occupants.every((occupant) => !occupant.current)}
        class:conflict={occupants.filter((occupant) => occupant.current).length > 1 || conflictChannels.has(channel)}
        class:selected={selectedAddress !== null && occupants.some((occupant) => occupant.address === selectedAddress)}
        style:--cm={occupants.length ? `var(--color-${occupants[0].styleKey}, var(--text-muted))` : null}
        style:--cm-deep={occupants.length ? `var(--color-${occupants[0].styleKey}-deep, var(--text-secondary))` : null}
        disabled={occupants.length === 0}
        aria-label={cellLabel(channel, occupants)}
        title={cellLabel(channel, occupants)}
        onclick={() => occupants.length && onSelect(occupants[0].address)}
      >{channel}</button>
    {/each}
  </div>
</section>

<style>
  .cm-panel {
    background: var(--bg-surface-solid);
    border: 1px solid var(--color-border);
    border-radius: var(--radius-lg);
    box-shadow: var(--shadow-sm);
    padding: 0.6rem 0.65rem 0.55rem;
    margin-bottom: 0.7rem;
  }
  .cm-head { display: flex; align-items: center; justify-content: space-between; gap: 0.5rem; flex-wrap: wrap; margin-bottom: 0.4rem; }
  .cm-head h3 {
    margin: 0;
    display: flex;
    align-items: center;
    gap: 0.35rem;
    font-size: var(--fs-micro);
    font-weight: 800;
    text-transform: uppercase;
    letter-spacing: 0.08em;
    color: var(--text-muted);
  }
  .cm-head h3::before {
    content: '';
    width: 3px;
    height: 11px;
    border-radius: 2px;
    background: linear-gradient(180deg, var(--color-primary), var(--color-sleep));
  }
  .cm-legend { display: flex; align-items: center; gap: 0.55rem; }
  .cm-legend .lg {
    display: inline-flex;
    align-items: center;
    gap: 0.25rem;
    font-size: var(--fs-micro);
    font-weight: 700;
    color: var(--text-muted);
  }
  .cm-legend .lg::before { content: ''; width: 6px; height: 6px; border-radius: var(--radius-pill); }
  .lg-on::before { background: var(--color-on); }
  .lg-standby::before { background: var(--color-standby); }
  .lg-sleep::before { background: var(--color-sleep); }
  .lg-booting::before { background: var(--color-booting); }
  .channel-map {
    display: grid;
    /* minmax(0, ...) lets cells shrink below their text content so the row
       never overflows or wraps in narrow windows. */
    grid-template-columns: repeat(16, minmax(0, 1fr));
    gap: 3px;
  }
  .cm-cell {
    display: flex;
    align-items: center;
    justify-content: center;
    height: 22px;
    padding: 0;
    overflow: hidden;
    font-family: var(--font-mono);
    font-size: 0.7rem;
    font-weight: 700;
    color: var(--text-muted);
    border: 1px solid var(--color-border);
    border-radius: var(--radius-xs);
    background: var(--bg-input);
    cursor: pointer;
    transition: background-color var(--dur-2) var(--ease), border-color var(--dur-2) var(--ease),
      color var(--dur-2) var(--ease), transform var(--dur-1) var(--ease-spring), box-shadow var(--dur-2) var(--ease);
  }
  /* Inset ring: the outward outline would slip under the adjacent opaque
     cells in the tight grid. */
  .cm-cell:focus-visible { outline-offset: -2px; }
  .cm-cell:disabled { cursor: default; background: transparent; border-color: color-mix(in srgb, var(--color-border) 60%, transparent); }
  .cm-cell.occupied:hover:not(:disabled) { transform: translateY(-1px); box-shadow: var(--shadow-sm); }
  .cm-cell.occupied {
    background: linear-gradient(160deg, color-mix(in srgb, var(--cm, var(--text-muted)) 20%, white), color-mix(in srgb, var(--cm, var(--text-muted)) 7%, white));
    border-color: color-mix(in srgb, var(--cm, var(--text-muted)) 48%, transparent);
    color: var(--cm-deep, var(--text-secondary));
  }
  .cm-cell.occupied.stale {
    background: linear-gradient(160deg, color-mix(in srgb, var(--cm, var(--text-muted)) 10%, white), color-mix(in srgb, var(--cm, var(--text-muted)) 3%, white));
    border-style: dashed;
    border-color: color-mix(in srgb, var(--cm, var(--text-muted)) 38%, transparent);
    color: var(--text-secondary);
  }
  .cm-cell.conflict {
    background: linear-gradient(160deg, color-mix(in srgb, var(--color-danger) 15%, white), color-mix(in srgb, var(--color-danger) 6%, white));
    color: var(--fb-error);
    animation: conflict-pulse 2.4s var(--ease) infinite;
  }
  /* Selection ring marks the channel of the station whose details drawer is
     open; stronger than the hover lift so it survives without interaction. */
  .cm-cell.selected {
    border-color: color-mix(in srgb, var(--color-primary) 65%, transparent);
    box-shadow: 0 0 0 2px color-mix(in srgb, var(--color-primary) 28%, transparent);
    color: var(--color-primary-deep);
  }
  /* Always a single row of sixteen: cells shrink with the window via the fr
     grid instead of wrapping, so the channel-to-position mapping stays
     fixed. */
  @media (max-width: 520px) {
    .channel-map { gap: 2px; }
    .cm-cell { height: 20px; }
  }
</style>
