<script lang="ts">
  import type { StationInfo } from '../types';
  import { hasCurrentChannel, stateLabel } from '../station';

  export let stations: StationInfo[];
  export let onSelect: (address: string) => void;
  // Display-only channel resolver. The parent injects a short-lived memory so
  // transient backend channel wipes do not flip cells between occupied and
  // free/disabled. `current` still uses the live station data, so a resolved
  // occupant without fresh data renders as last-known (stale).
  export let channelOf: (station: StationInfo) => number = (station) => station.channel;

  interface Occupant {
    name: string;
    state: string;
    address: string;
    current: boolean;
  }

  $: byChannel = stations.reduce((map, station) => {
    const channel = channelOf(station);
    if (channel > 0) {
      const list = map.get(channel) ?? [];
      list.push({
        name: station.name,
        state: stateLabel(station),
        address: station.address,
        current: hasCurrentChannel(station)
      });
      list.sort((left, right) => Number(right.current) - Number(left.current));
      map.set(channel, list);
    }
    return map;
  }, new Map<number, Occupant[]>());

  $: conflictChannels = new Set(
    stations.filter((station) => station.channelConflict && hasCurrentChannel(station))
      .map((station) => station.channel)
  );

  function cellLabel(channel: number, occupants: Occupant[]): string {
    if (!occupants.length) return `CH ${channel} — free`;
    const names = occupants.map((occupant) =>
      `${occupant.name} · ${occupant.state}${occupant.current ? '' : ' · last-known'}`
    ).join(', ');
    return `CH ${channel} — ${names}`;
  }
</script>

<section class="cm-panel">
  <div class="cm-head">
    <h3>Optical channels</h3>
    <div class="cm-legend" aria-hidden="true">
      <span class="lg lg-on">On</span>
      <span class="lg lg-standby">Standby</span>
      <span class="lg lg-sleep">Sleep</span>
    </div>
  </div>
  <div class="channel-map" role="group" aria-label="Channel occupancy">
    {#each Array.from({ length: 16 }, (_, index) => index + 1) as channel}
      {@const occupants = byChannel.get(channel) ?? []}
      <button
        type="button"
        class="cm-cell"
        class:occupied={occupants.length > 0}
        class:stale={occupants.length > 0 && occupants.every((occupant) => !occupant.current)}
        class:conflict={occupants.filter((occupant) => occupant.current).length > 1 || conflictChannels.has(channel)}
        style:--cm={occupants.length ? `var(--color-${occupants[0].state}, var(--text-muted))` : null}
        style:--cm-deep={occupants.length ? `var(--color-${occupants[0].state}-deep, var(--text-secondary))` : null}
        disabled={occupants.length === 0}
        aria-label={cellLabel(channel, occupants)}
        title={cellLabel(channel, occupants)}
        on:click={() => occupants.length && onSelect(occupants[0].address)}
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
  .cm-head { display: flex; align-items: center; justify-content: space-between; gap: 0.5rem; margin-bottom: 0.4rem; }
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
  .channel-map {
    display: grid;
    grid-template-columns: repeat(16, 1fr);
    gap: 3px;
  }
  .cm-cell {
    display: flex;
    align-items: center;
    justify-content: center;
    height: 22px;
    padding: 0;
    font-family: var(--font-mono);
    font-size: 0.6rem;
    font-weight: 700;
    color: var(--text-muted);
    border: 1px solid var(--color-border);
    border-radius: var(--radius-xs);
    background: var(--bg-input);
    cursor: pointer;
    transition: background-color var(--dur-2) var(--ease), border-color var(--dur-2) var(--ease),
      color var(--dur-2) var(--ease), transform var(--dur-1) var(--ease-spring), box-shadow var(--dur-2) var(--ease);
  }
  .cm-cell:disabled { cursor: default; background: transparent; border-color: color-mix(in srgb, var(--color-border) 60%, transparent); }
  .cm-cell.occupied:hover:not(:disabled) { transform: translateY(-1px); box-shadow: var(--shadow-sm); }
  .cm-cell.occupied {
    background: color-mix(in srgb, var(--cm, var(--text-muted)) 15%, white);
    border-color: color-mix(in srgb, var(--cm, var(--text-muted)) 48%, transparent);
    color: var(--cm-deep, var(--text-secondary));
  }
  .cm-cell.occupied.stale {
    background: color-mix(in srgb, var(--cm, var(--text-muted)) 8%, white);
    border-style: dashed;
    border-color: color-mix(in srgb, var(--cm, var(--text-muted)) 38%, transparent);
    color: var(--text-secondary);
  }
  .cm-cell.conflict {
    background: color-mix(in srgb, var(--color-danger) 12%, white);
    border-color: color-mix(in srgb, var(--color-danger) 55%, transparent);
    color: var(--fb-error);
  }
</style>
