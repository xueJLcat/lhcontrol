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
  // Slow setters whose backend work legitimately exceeds the default watchdog
  // (the API listen-address switch waits for listener restarts) override the
  // timeout instead of racing the rollback path.
  timeoutMs?: number;
}

// Every other long-lived backend call in the store carries a watchdog; a
// hung setting binding must not keep its row (and every later serialized
// operation queued behind it) busy forever.
const SETTING_OPERATION_TIMEOUT_MS = 10000;

// A timeout means the backend call may still be running (Wails calls cannot
// be cancelled), which makes the compensating rollback re-read observe a
// mid-transition value; the catch path treats this shape specially.
class SettingOperationTimeoutError extends Error {
  constructor(action: string, timeoutMs: number) {
    super(`${action} timed out after ${timeoutMs}ms`);
    this.name = 'SettingOperationTimeoutError';
  }
}

function withTimeout<T>(promise: Promise<T>, action: string, timeoutMs: number): Promise<T> {
  return new Promise<T>((resolve, reject) => {
    const timer = setTimeout(
      () => reject(new SettingOperationTimeoutError(action, timeoutMs)),
      timeoutMs
    );
    void promise.then(
      (value) => {
        clearTimeout(timer);
        resolve(value);
      },
      (error) => {
        clearTimeout(timer);
        reject(error);
      }
    );
  });
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
  // Advances with every accepted edit so a failed queued save can tell
  // whether a newer edit already owns the displayed value.
  private saveRevision = 0;
  // A Retry click that lands while a save is still settling (the error card
  // renders a microtask before busy clears) is remembered and re-run by the
  // settling operation's finally instead of being dropped silently.
  private reloadPending = false;

  constructor(private readonly options: AsyncSettingOptions<T>) {}

  private get timeoutMs(): number {
    return this.options.timeoutMs ?? SETTING_OPERATION_TIMEOUT_MS;
  }

  private finishBusy() {
    this.busy = false;
    if (this.reloadPending) {
      this.reloadPending = false;
      void this.load();
    }
  }

  load = async (): Promise<void> => {
    if (this.busy) {
      this.reloadPending = true;
      return;
    }
    this.busy = true;
    this.error = null;
    try {
      const value = await serializeSettingOperation(this.options.setter, () =>
        withTimeout(this.options.getter(), 'reading the setting', this.timeoutMs)
      );
      this.value = this.options.map ? this.options.map(value) : value;
    } catch (error) {
      this.value = null;
      this.error = backendCopy(String(error));
      pushToast(withDetail(this.options.loadMessage, backendCopy(String(error))));
    } finally {
      this.finishBusy();
    }
  };

  change = async (next: T): Promise<void> => {
    // A pending load can still replace the displayed value, so a save must
    // not race it. A save already in flight (or queued) must not drop the
    // newer value: queue it instead so the most recent edit wins once the
    // serialized saves drain.
    if (this.value === null || (this.busy && this.pendingSaves === 0)) return;
    this.value = next;
    const revision = ++this.saveRevision;
    this.pendingSaves += 1;
    this.busy = true;
    try {
      await serializeSettingOperation(this.options.setter, async () => {
        try {
          await withTimeout(this.options.setter(next), 'saving the setting', this.timeoutMs);
        } catch (error) {
          if (revision !== this.saveRevision) {
            // A newer queued edit owns the displayed value, and its save
            // settles the backend state next. Rolling back here would
            // clobber that optimistic value with an older persisted one and
            // leave the display behind once the newer save succeeds.
            pushToast(withDetail(this.options.saveMessage, backendCopy(String(error))));
            return;
          }
          if (error instanceof SettingOperationTimeoutError) {
            // The setter may still be running on the backend, so an immediate
            // re-read can observe a mid-transition value that a later commit
            // then contradicts. Drop the value and surface the error instead;
            // Retry reloads the actually persisted state.
            this.value = null;
            this.error = backendCopy(String(error));
            pushToast(withDetail(this.options.saveMessage, backendCopy(String(error))));
            return;
          }
          // Another drawer instance may have completed an earlier queued save
          // after this instance captured `previous`. Re-read inside the same
          // serialization slot so a failed save rolls back to the value that
          // is actually persisted, not to an older local snapshot.
          try {
            const persisted = await withTimeout(this.options.getter(), 'reading the setting', this.timeoutMs);
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
          const followUp = this.options.afterSave?.(next);
          if (followUp) await withTimeout(followUp, 'applying the setting', this.timeoutMs);
        } catch (error) {
          // Persistence already succeeded. Keep the saved value visible and
          // report only the local follow-up failure; rolling back here would
          // make the UI disagree with the backend and the next application run.
          pushToast(withDetail('Setting was saved, but the current view could not apply it immediately', backendCopy(String(error))), 'warning');
        }
      });
    } finally {
      this.pendingSaves -= 1;
      if (this.pendingSaves === 0) this.finishBusy();
    }
  };
}
