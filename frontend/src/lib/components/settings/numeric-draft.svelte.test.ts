import { flushSync } from 'svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { clampedDraft, numericDraft } from './numeric-draft.svelte';

describe('clampedDraft', () => {
  it('falls back for empty, blank, null, and non-numeric drafts', () => {
    expect(clampedDraft('', 7, 1, 10)).toBe(7);
    expect(clampedDraft('   ', 7, 1, 10)).toBe(7);
    expect(clampedDraft(null, 7, 1, 10)).toBe(7);
    expect(clampedDraft('abc', 7, 1, 10)).toBe(7);
    expect(clampedDraft('12x', 7, 1, 10)).toBe(7);
  });

  it('rounds parsed values', () => {
    expect(clampedDraft('3.4', 7, 1, 10)).toBe(3);
    expect(clampedDraft('3.5', 7, 1, 10)).toBe(4);
  });

  it('clamps to the allowed range', () => {
    expect(clampedDraft('99', 7, 1, 10)).toBe(10);
    expect(clampedDraft('0', 7, 1, 10)).toBe(1);
  });
});

function harness(initial: number | null, min = 1, max = 10) {
  let value = $state<number | null>(initial);
  const apply = vi.fn((next: number) => {
    value = next;
  });
  let model!: ReturnType<typeof numericDraft>;
  const cleanup = $effect.root(() => {
    model = numericDraft(() => value, min, max, apply);
  });
  flushSync();
  return {
    get value() {
      return value;
    },
    set value(next: number | null) {
      value = next;
    },
    apply,
    model,
    cleanup
  };
}

describe('numericDraft', () => {
  const cleanups: Array<() => void> = [];

  function create(initial: number | null, min = 1, max = 10) {
    const instance = harness(initial, min, max);
    cleanups.push(instance.cleanup);
    return instance;
  }

  afterEach(() => {
    for (const cleanup of cleanups.splice(0)) cleanup();
  });

  it('mirrors the initial value into the draft', () => {
    const { model } = create(5);
    expect(model.draft).toBe('5');
  });

  it('follows external changes while the draft is untouched', () => {
    const instance = create(5);
    instance.value = 7;
    flushSync();
    expect(instance.model.draft).toBe('7');
  });

  it('keeps an edited draft across external changes until it matches the known value again', () => {
    const instance = create(5);
    instance.model.draft = '55';
    instance.value = 7;
    flushSync();
    expect(instance.model.draft).toBe('55');
    // Once the draft equals the known value again, external updates flow.
    instance.model.draft = '7';
    instance.value = 8;
    flushSync();
    expect(instance.model.draft).toBe('8');
  });

  it('does not overwrite a draft when the external value becomes null', () => {
    const instance = create(5);
    instance.model.draft = '55';
    instance.value = null;
    flushSync();
    expect(instance.model.draft).toBe('55');
  });

  it('reverts an unparseable commit to the current value without applying', () => {
    const instance = create(5);
    instance.model.draft = '';
    instance.model.commit();
    expect(instance.model.draft).toBe('5');
    expect(instance.apply).not.toHaveBeenCalled();
    instance.model.draft = 'abc';
    instance.model.commit();
    expect(instance.model.draft).toBe('5');
    expect(instance.apply).not.toHaveBeenCalled();
  });

  it('clamps committed values into range', () => {
    const instance = create(5, 1, 10);
    instance.model.draft = '99';
    instance.model.commit();
    expect(instance.apply).toHaveBeenCalledWith(10);
    expect(instance.model.draft).toBe('10');
    instance.model.draft = '0';
    instance.model.commit();
    expect(instance.apply).toHaveBeenLastCalledWith(1);
    expect(instance.model.draft).toBe('1');
  });

  it('only applies when the committed value changes', () => {
    const instance = create(5);
    instance.model.draft = '5';
    instance.model.commit();
    expect(instance.apply).not.toHaveBeenCalled();
    expect(instance.model.draft).toBe('5');
  });

  it('is a no-op commit while the external value is null', () => {
    const instance = create(null);
    instance.model.draft = '3';
    instance.model.commit();
    expect(instance.apply).not.toHaveBeenCalled();
    expect(instance.model.draft).toBe('3');
  });
});
