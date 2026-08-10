export type NumericDraftValue = string | number | null;

export function clampedDraft(draft: NumericDraftValue, fallback: number, min: number, max: number): number {
  const text = String(draft ?? '').trim();
  if (text === '') return fallback;
  const parsed = Number(text);
  if (!Number.isFinite(parsed)) return fallback;
  return Math.min(max, Math.max(min, Math.round(parsed)));
}

export function numericDraft(
  getValue: () => number | null,
  min: number,
  max: number,
  apply: (value: number) => void
) {
  let draft = $state<NumericDraftValue>('');
  let known: number | null = null;

  $effect(() => {
    const current = getValue();
    if (current === null || current === known) return;
    if (known === null || String(draft ?? '') === String(known)) {
      draft = String(current);
    }
    known = current;
  });

  return {
    get draft(): NumericDraftValue {
      return draft;
    },
    set draft(value: NumericDraftValue) {
      draft = value;
    },
    commit() {
      const current = getValue();
      if (current === null) return;
      const value = clampedDraft(draft, current, min, max);
      draft = String(value);
      if (value !== current) apply(value);
    }
  };
}
