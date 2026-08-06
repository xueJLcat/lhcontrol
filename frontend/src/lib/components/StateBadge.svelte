<script lang="ts">
  import { LoaderCircle } from 'lucide-svelte';

  export let label: string;
  export let unverified = false;
  export let stale = false;
  export let booting = false;

  // Styles depend on the class name, not the display label: a backend label
  // with unexpected casing or spacing would otherwise miss every selector.
  $: badgeClass = /^[a-z]+$/.test(label) ? label : 'unknown';
</script>

<span class="state-badge state-{badgeClass}" class:unverified class:stale>
  {#if booting}<LoaderCircle class="spin" size={10} />{/if}
  {label}
</span>
