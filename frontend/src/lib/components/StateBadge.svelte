<script lang="ts">
  import { LoaderCircle } from 'lucide-svelte';

  export let label: string;
  export let unverified = false;
  export let stale = false;
  export let booting = false;
</script>

<span class="state-badge state-{label}" class:unverified class:stale>
  {#if booting}<LoaderCircle class="spin" size={10} />{/if}
  {label}
</span>
