<script lang="ts">
  import { Bluetooth, LoaderCircle, RefreshCw } from 'lucide-svelte';
  import { t } from '../../i18n.svelte';
  import { useBluetoothDiagnostics } from './context';

  const settings = useBluetoothDiagnostics();
</script>

<section>
    <h4><Bluetooth size={12} /> {t('Bluetooth diagnostics')}</h4>
    {#if settings.loading}
      <p class="hint loading"><LoaderCircle class="spin" size={14} /> {t('Detecting Bluetooth adapters...')}</p>
    {:else if settings.loadError}
      <div class="alert danger">{settings.loadError}</div>
      <div class="drawer-actions"><button class="btn" onclick={settings.onRefresh}><RefreshCw size={15} /> {t('Retry')}</button></div>
    {:else}
      <div class="adapter-list" aria-label={t('Detected Bluetooth adapters')}>
        {#each settings.adapters as adapter (adapter.deviceId)}
          <div class="adapter-option"><span class="adapter-text"><span class="adapter-name">{adapter.name}</span><span class="adapter-id">{adapter.deviceId}</span></span></div>
        {:else}
          <p class="hint">{t('No Bluetooth adapters were detected on this system.')}</p>
        {/each}
      </div>
      <div class="drawer-actions">
        <button class="btn" onclick={settings.onRefresh}><RefreshCw size={15} /> {t('Refresh adapters')}</button>
      </div>
      <p class="hint">{t('Windows controls which radio handles BLE discovery and connections. The application cannot route a Lighthouse operation through one specific adapter.')}</p>
    {/if}
  </section>
