import { describe, expect, it } from 'vitest';
import { FleetState } from './fleet-state.svelte';
import { createStation } from '../../test/fixtures';

describe('FleetState merge', () => {
  it('dedupes duplicate new addresses instead of adding repeated cards', () => {
    const fleet = new FleetState();
    const dupFirst = createStation({ address: 'AA', name: 'dup-first' });
    const dupLast = createStation({ address: 'AA', name: 'dup-last' });
    const other = createStation({ address: 'BB', name: 'other' });

    fleet.merge([dupFirst, dupLast, other]);

    expect(fleet.stations).toHaveLength(2);
    expect(fleet.stations.map((station) => station.address)).toEqual(['AA', 'BB']);
    // The last occurrence wins for a duplicated new address.
    expect(fleet.stations.find((station) => station.address === 'AA')?.name).toBe('dup-last');
  });

  it('merges updates for existing stations without duplication', () => {
    const fleet = new FleetState();
    fleet.replace([createStation({ address: 'AA', name: 'first' })]);

    fleet.merge([
      createStation({ address: 'AA', name: 'updated' }),
      createStation({ address: 'AA', name: 'updated-again' }),
      createStation({ address: 'BB', name: 'new' })
    ]);

    expect(fleet.stations).toHaveLength(2);
    expect(fleet.stations.find((station) => station.address === 'AA')?.name).toBe('updated-again');
    expect(fleet.stations.map((station) => station.address)).toEqual(['AA', 'BB']);
  });
});
