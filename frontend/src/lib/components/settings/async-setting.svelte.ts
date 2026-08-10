import { pushToast } from '../../toast';
import { withDetail, type TranslationKey } from '../../i18n.svelte';

export interface AsyncSettingOptions<T> {
  getter: () => Promise<T>;
  setter: (value: T) => Promise<void>;
  loadMessage: TranslationKey;
  saveMessage: TranslationKey;
  map?: (value: T) => T;
  afterSave?: (value: T) => void | Promise<void>;
}

export class AsyncSetting<T> {
  value = $state<T | null>(null);
  error = $state<string | null>(null);
  busy = $state(false);

  constructor(private readonly options: AsyncSettingOptions<T>) {}

  load = async (): Promise<void> => {
    this.error = null;
    try {
      const value = await this.options.getter();
      this.value = this.options.map ? this.options.map(value) : value;
    } catch (error) {
      this.value = null;
      this.error = String(error);
      pushToast(withDetail(this.options.loadMessage, error));
    }
  };

  change = async (next: T): Promise<void> => {
    if (this.busy || this.value === null) return;
    const previous = this.value;
    this.value = next;
    this.busy = true;
    try {
      await this.options.setter(next);
      await this.options.afterSave?.(next);
    } catch (error) {
      this.value = previous;
      pushToast(withDetail(this.options.saveMessage, error));
    } finally {
      this.busy = false;
    }
  };
}
