<script lang="ts">
  import { setContext } from 'svelte';
  import { cubicOut } from 'svelte/easing';
  import { fly } from 'svelte/transition';
  import { X } from 'lucide-svelte';
  import { focusTrap } from '../actions';
  import { dur } from '../motion';
  import { t } from '../i18n.svelte';
  import AdvancedSettings from './settings/AdvancedSettings.svelte';
  import BluetoothDiagnostics from './settings/BluetoothDiagnostics.svelte';
  import OperationSettings from './settings/OperationSettings.svelte';
  import PreferenceSettings from './settings/PreferenceSettings.svelte';
  import { settingsDrawerContextKey } from './settings/context';
  import type { SettingsDrawerProps } from './settings/types';
  import './settings/settings-drawer.css';

  const props: SettingsDrawerProps = $props();
  setContext(settingsDrawerContextKey, {
    get props() {
      return props;
    }
  });
</script>

<div
  class="drawer settings-drawer"
  role="dialog"
  aria-modal={props.inactive ? undefined : 'true'}
  aria-hidden={props.inactive ? 'true' : undefined}
  inert={props.inactive}
  aria-label={t('Settings')}
  tabindex="-1"
  use:focusTrap
  in:fly={dur({ x: 64, duration: 320, easing: cubicOut })}
  out:fly={dur({ x: 64, duration: 180 })}
>
  <div class="drawer-head">
    <div>
      <small>{t('Settings')}</small>
      <div class="drawer-title"><h2>{t('Preferences')}</h2></div>
    </div>
    <button type="button" class="icon-btn" title={t('Close')} aria-label={t('Close settings')} onclick={props.onClose}><X size={18} /></button>
  </div>

  <PreferenceSettings />
  <OperationSettings />
  <AdvancedSettings />
  <BluetoothDiagnostics />
</div>
