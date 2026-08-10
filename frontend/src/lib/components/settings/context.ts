import { getContext } from 'svelte';
import type { SettingsDrawerProps } from './types';

export const settingsDrawerContextKey = Symbol('settings-drawer');

interface SettingsDrawerContext {
  readonly props: SettingsDrawerProps;
}

function useContext(): SettingsDrawerContext {
  return getContext<SettingsDrawerContext>(settingsDrawerContextKey);
}

export function usePreferenceSettings() {
  return useContext().props.model.preferences;
}

export function useOperationSettings() {
  return useContext().props.model.operations;
}

export function useAdvancedSettings() {
  return useContext().props.model.advanced;
}

export function useBluetoothDiagnostics() {
  return useContext().props.model.diagnostics;
}
