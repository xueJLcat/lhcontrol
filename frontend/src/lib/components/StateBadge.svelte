<script lang="ts">
  import { LoaderCircle } from 'lucide-svelte';

  let { label, state = undefined, unverified = false, stale = false, booting = false }: {
    label: string;
    state?: string | undefined;
    unverified?: boolean;
    stale?: boolean;
    booting?: boolean;
  } = $props();

  // Styles depend on the class name, not the display label: a backend label
  // with unexpected casing or spacing would otherwise miss every selector.
  const styleState = $derived(state ?? label);
  const badgeClass = $derived(/^[a-z]+$/.test(styleState) ? styleState : 'unknown');
</script>

<span class="state-badge state-{badgeClass}" class:unverified class:stale>
  {#if booting}<LoaderCircle class="spin" size={10} />{/if}
  {label}
</span>
