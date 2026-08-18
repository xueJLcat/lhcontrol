import { pushToast } from '../../toast';
import { backendCopy } from '../../backend-copy';
import { withDetail, type TranslationKey } from '../../i18n.svelte';

export interface AsyncSettingOptions<T> {
  getter: () => Promise<T>;
  setter: (value: T) => Promise<void>;
  loadMessage: TranslationKey;
  saveMessage: TranslationKey;
  map?: (value: T) => T;
  afterSave?: (value: T) => void | Promise<void>;
}

const settingOperationTails = new WeakMap<object, Promise<void>>();

function serializeSettingOperation<T>(key: object, operation: () => Promise<T>): Promise<T> {
  const previous = settingOperationTails.get(key);
  const result = previous ? previous.then(operation) : operation();
  const tail = result.then(
    () => undefined,
    () => undefined
  );
  settingOperationTails.set(key, tail);
  void tail.then(() => {
    if (settingOperationTails.get(key) === tail) settingOperationTails.delete(key);
  });
  return result;
}

export class AsyncSetting<T> {
  value = $state<T | null>(null);
  error = $state<string | null>(null);
  busy = $state(false);
  private pendingSaves = 0;

  constructor(private readonly options: AsyncSettingOptions<T>) {}

  load = async (): Promise<void> => {
    if (this.busy) return;
    this.busy = true;
    this.error = null;
    try {
      const value = await serializeSettingOperation(this.options.setter, this.options.getter);
      this.value = this.options.map ? this.options.map(value) : value;
    } catch (error) {
      this.value = null;
      this.error = backendCopy(String(error));
      pushToast(withDetail(this.options.loadMessage, backendCopy(String(error))));
    } finally {
      this.busy = false;
    }
  };

  change = async (next: T): Promise<void> => {
    // A pending load can still replace the displayed value, so a save must
    // not race it. A save already in flight (or queued) must not drop the
    // newer value: queue it instead so the most recent edit wins once the
    // serialized saves drain.
    if (this.value === null || (this.busy && this.pendingSaves === 0)) return;
    this.value = next;
    this.pendingSaves += 1;
    this.busy = true;
    try {
      await serializeSettingOperation(this.options.setter, async () => {
        try {
          await this.options.setter(next);
        } catch (error) {
          // Another drawer instance may have completed an earlier queued save
          // after this instance captured `previous`. Re-read inside the same
          // serialization slot so a failed later save rolls back to the value
          // that is actually persisted, not to an older local snapshot.
          try {
            const persisted = await this.options.getter();
            this.value = this.options.map ? this.options.map(persisted) : persisted;
            this.error = null;
          } catch {
            // The compensating re-read failed as well, so the rolled-back value
            // may not match the backend. Drop the value so the template's
            // error branch renders with the Retry action instead of silently
            // showing a possibly stale value; retrying the load recovers it.
            this.value = null;
            this.error = backendCopy(String(error));
          }
          pushToast(withDetail(this.options.saveMessage, backendCopy(String(error))));
          return;
        }

        try {
          await this.options.afterSave?.(next);
        } catch (error) {
          // Persistence already succeeded. Keep the saved value visible and
          // report only the local follow-up failure; rolling back here would
          // make the UI disagree with the backend and the next application run.
          pushToast(withDetail('Setting was saved, but the current view could not apply it immediately', backendCopy(String(error))), 'warning');
        }
      });
    } finally {
      this.pendingSaves -= 1;
      if (this.pendingSaves === 0) this.busy = false;
    }
  };
}
