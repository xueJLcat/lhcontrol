<script lang="ts">
  import type { StationInfo } from '../types';
  import { stateLabel } from '../station';

  export let stations: StationInfo[];
  export let onSelect: (address: string) => void;

  interface Occupant {
    name: string;
    state: string;
    address: string;
  }

  $: byChannel = stations.reduce((map, station) => {
    if (station.channel > 0) {
      const list = map.get(station.channel) ?? [];
      list.push({ name: station.name, state: stateLabel(station), address: station.address });
      map.set(station.channel, list);
    }
    return map;
  }, new Map<number, Occupant[]>());

  $: conflictChannels = new Set(
    stations.filter((station) => station.channelConflict && station.channel > 0)
      .map((station) => station.channel)
  );

  function cellLabel(channel: number, occupants: Occupant[]): string {
    if (!occupants.length) return `CH ${channel} — free`;
    const names = occupants.map((occupant) => `${occupant.name} · ${occupant.state}`).join(', ');
    return `CH ${channel} — ${names}`;
  }
</script>

<div class="channel-map" role="group" aria-label="Channel occupancy">
  {#each Array.from({ length: 16 }, (_, index) => index + 1) as channel}
    {@const occupants = byChannel.get(channel) ?? []}
    <button
      type="button"
      class="cm-cell"
      class:occupied={occupants.length > 0}
      class:conflict={occupants.length > 1 || conflictChannels.has(channel)}
      style:--cm={occupants.length ? `var(--color-${occupants[0].state}, var(--text-muted))` : null}
      disabled={occupants.length === 0}
      aria-label={cellLabel(channel, occupants)}
      title={cellLabel(channel, occupants)}
      on:click={() => occupants.length && onSelect(occupants[0].address)}
    >{channel}</button>
  {/each}
</div>

<style>
  .channel-map {
    display: grid;
    grid-template-columns: repeat(16, 1fr);
    gap: 2px;
    margin-bottom: 0.6rem;
  }
  .cm-cell {
    display: flex;
    align-items: center;
    justify-content: center;
    height: 20px;
    padding: 0;
    font-family: var(--font-mono);
    font-size: 0.6rem;
    font-weight: 700;
    color: var(--text-muted);
    border: 1px solid var(--color-border);
    border-radius: var(--radius-xs);
    background: rgba(7, 11, 18, 0.4);
    cursor: pointer;
    transition: background-color var(--dur-2) var(--ease), border-color var(--dur-2) var(--ease),
      color var(--dur-2) var(--ease);
  }
  .cm-cell:disabled { cursor: default; }
  .cm-cell.occupied:hover:not(:disabled) { filter: brightness(1.3); }
  .cm-cell.occupied {
    background: color-mix(in srgb, var(--cm, var(--text-muted)) 28%, transparent);
    border-color: color-mix(in srgb, var(--cm, var(--text-muted)) 55%, transparent);
    color: #fff;
  }
  .cm-cell.conflict {
    background: color-mix(in srgb, var(--color-danger) 18%, transparent);
    border-color: color-mix(in srgb, var(--color-danger) 60%, transparent);
    color: var(--fb-error);
  }
</style>
