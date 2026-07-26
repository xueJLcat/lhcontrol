import { describe, expect, it } from 'vitest';
import { RevisionGate } from './revision-gate';

describe('RevisionGate', () => {
  it('rejects an older asynchronous response', () => {
    const gate = new RevisionGate();
    const older = gate.next();
    const newer = gate.next();
    expect(gate.isCurrent(older)).toBe(false);
    expect(gate.isCurrent(newer)).toBe(true);
  });

  it('rejects every response after disposal', () => {
    const gate = new RevisionGate();
    const revision = gate.next();
    gate.dispose();
    expect(gate.isCurrent(revision)).toBe(false);
  });
});
