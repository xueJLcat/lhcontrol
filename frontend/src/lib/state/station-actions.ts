import {
  CancelBulkPower,
  GetCurrentStationInfo,
  IdentifyStation,
  RefreshStationCapabilities,
  RenameStationByAddress,
  SetAllStationsPowerDetailed,
  SetStationChannel,
  SetStationPower
} from '../backend';
import type { PowerFeedback, PowerTarget, StationInfo } from '../types';
import {
  canSetPower,
  channelChangeBlockedReason,
  hasCurrentChannel,
  powerTargetLabel,
  stateLabel
} from '../station';
import { formatBulkResult, summarizeBulkResult } from '../result-format';
import { pushToast } from '../toast';
import type { GlobalOperation } from '../operation-state';
import { OperationGate } from '../operation-gate';
import { PowerFeedbackRegistry } from '../power-feedback';
import { RevisionGate } from '../revision-gate';
import { ApiStatusPoller } from '../api-status-poller';
import { t, withDetail } from '../i18n.svelte';

export interface StationActionHost {
  stations: StationInfo[];
  statusMessage: string;
  powerTargetByAddress: Record<string, PowerTarget | undefined>;
  globalOperation: GlobalOperation;
  bulkTarget: PowerTarget | null;
  cancellingBulk: boolean;
  editingAddress: string | null;
  channelError: string;
  channelWarning: boolean;
  channelSavingAddress: string | null;
  readonly bulkLocked: boolean;
  readonly stationLocked: boolean;
  readonly untrustedCount: number;
  readonly disposed: boolean;
  readonly gates: OperationGate;
  readonly listRevisions: RevisionGate;
  readonly powerFeedback: PowerFeedbackRegistry;
  readonly apiStatus: ApiStatusPoller;
  readonly ui: {
    forceCloseChannelEditor(): void;
    requestBulkConfirmation(target: PowerTarget): void;
  };
  stationBusy(address: string): boolean;
  configBusy(address: string): boolean;
  gattLockedFor(address: string): boolean;
  setGattBusy(address: string, busy: boolean): void;
  setConfigBusy(address: string, busy: boolean): void;
  mergeStationUpdates(updated: StationInfo[]): void;
  commitStations(updated: StationInfo[]): void;
  applyStationList(
    updated: StationInfo[] | null | undefined,
    revision: number,
    capturedStationRevisions?: Map<string, number>
  ): boolean;
  withStationChanges(current: StationInfo, changes: Partial<StationInfo>): StationInfo;
}

export class StationActionController {
  constructor(private host: StationActionHost) {}
  private async fetchLatestList(revision = this.host.listRevisions.next()): Promise<boolean> {
    const capturedStationRevisions = this.host.gates.snapshotStationRevisions();
    try {
      return this.host.applyStationList(await GetCurrentStationInfo(), revision, capturedStationRevisions);
    } catch {
      return false;
    }
  }

  private async fetchStationUpdate(
    address: string,
    epoch: number,
    operationRevision: number
  ): Promise<StationInfo | null> {
    const updated = await GetCurrentStationInfo().catch(() => null);
    if (!this.host.gates.canCommitStationOperation(epoch, address, operationRevision)) return null;
    const station = updated?.find((item) => item.address === address) ?? null;
    if (station) {
      this.host.mergeStationUpdates([station]);
    }
    return station;
  }

  private powerReadbackLabel(station: StationInfo | null): string {
    if (!station || station.powerState < 0) return t('unavailable');
    return `${t(station.powerFresh ? 'actual' : 'last-known')} ${stateLabel(station)}`;
  }

  private channelReadbackLabel(station: StationInfo | null): string {
    if (!station || station.channel <= 0) return t('unavailable');
    return `${t(station.channelFresh ? 'actual' : 'last-known')} ${station.channel}`;
  }

  async setPower(station: StationInfo, state: PowerTarget) {
    if (!canSetPower(station, state) || this.host.stationBusy(station.address) || this.host.gattLockedFor(station.address)) return;
    const targetLabel = powerTargetLabel(state);
    const operationEpoch = this.host.gates.currentScanEpoch;
    const statusOperation = this.host.gates.beginStatusOperation();
    const operationRevision = this.host.gates.beginStationOperationRevision(station.address);
    this.host.setGattBusy(station.address, true);
    this.host.powerTargetByAddress = { ...this.host.powerTargetByAddress, [station.address]: state };
    this.host.powerFeedback.set(station.address, {
      kind: 'pending',
      text: t('Switching to {target}…', { target: targetLabel }),
      target: state
    });
    this.host.statusMessage = t('Setting {name} to {target}…', { name: station.name, target: targetLabel });
    try {
      const result = await SetStationPower(station.address, state);
      if (!this.host.gates.canCommitStationOperation(operationEpoch, station.address, operationRevision)) return;
      this.host.mergeStationUpdates([result.station]);
      this.host.powerFeedback.set(station.address, result.skipped
        ? {
            kind: 'success', text: t('Already {target}', { target: targetLabel }), target: state,
            readAt: result.station.lastPowerReadAt
          }
        : result.confirmed
        ? {
            kind: 'success', text: t('{target} confirmed', { target: targetLabel }), target: state,
            readAt: result.station.lastPowerReadAt
          }
        : {
            kind: 'warning',
            text: result.confirmationError
              ? t('{target} sent · confirmation failed', { target: targetLabel })
              : t('{target} sent · status unavailable', { target: targetLabel }),
            target: state,
            readAt: result.station.lastPowerReadAt
          });
      if (this.host.gates.canCommitStatus(statusOperation)) {
        this.host.statusMessage = result.skipped
          ? t('{name} is already {target}; no command was sent.', { name: station.name, target: targetLabel })
          : result.confirmed
          ? t('{name} is {target}.', { name: station.name, target: targetLabel })
          : result.confirmationError
            ? t('{name}: command sent, but confirmation failed. {detail}', { name: station.name, detail: result.confirmationError })
            : t('{name}: {target} command sent; this firmware cannot confirm the state.', { name: station.name, target: targetLabel });
      }
    } catch (error) {
      if (!this.host.gates.canCommitStationOperation(operationEpoch, station.address, operationRevision)) return;
      const errorText = String(error);
      const actual = await this.fetchStationUpdate(station.address, operationEpoch, operationRevision);
      if (!this.host.gates.canCommitStationOperation(operationEpoch, station.address, operationRevision)) return;
      this.host.powerFeedback.set(station.address, {
        kind: 'error',
        text: t('Failed · {readback}', { readback: this.powerReadbackLabel(actual) }),
        target: state,
        readAt: actual?.lastPowerReadAt
      });
      if (this.host.gates.canCommitStatus(statusOperation)) {
        this.host.statusMessage = `${t('Power change failed for {name}', { name: station.name })}: ${errorText}`;
        pushToast(this.host.statusMessage);
      }
    } finally {
      if (this.host.gates.canCleanupStationOperation(station.address, operationRevision)) {
        const nextTargets = { ...this.host.powerTargetByAddress };
        delete nextTargets[station.address];
        this.host.powerTargetByAddress = nextTargets;
        this.host.setGattBusy(station.address, false);
      }
    }
  }

  private actionablePowerStations(state: PowerTarget): StationInfo[] {
    return this.host.stations.filter((station) => canSetPower(station, state));
  }

  requestBulkPower(state: PowerTarget) {
    // Do not duplicate backend capability/state decisions here. Cached
    // frontend data can be stale after scanning, while the backend refreshes
    // capabilities and returns a result for every known station.
    if (this.host.bulkLocked || this.actionablePowerStations(state).length === 0) return;
    // When part of the fleet is not fully verified, the command scope is no
    // longer obvious from the button; demand an explicit confirmation that
    // lists what will be affected.
    if (this.host.untrustedCount > 0) {
      this.host.ui.requestBulkConfirmation(state);
      return;
    }
    void this.runBulkPower(state);
  }

  async runBulkPower(state: PowerTarget) {
    // requestBulkPower performs this check, but the confirmation modal calls
    // runBulkPower directly; re-check so a lock (external scan/operation or
    // auto-sleep) that lands between the modal opening and the confirm click
    // cannot start a bulk operation against a busy backend.
    if (this.host.bulkLocked || this.actionablePowerStations(state).length === 0) return;
    this.host.globalOperation = 'bulk-power';
    this.host.cancellingBulk = false;
    const statusOperation = this.host.gates.beginStatusOperation();
    this.host.bulkTarget = state;
    const targetLabel = powerTargetLabel(state);
    const operationEpoch = this.host.gates.currentScanEpoch;
    this.host.statusMessage = t('Setting all available stations to {target}…', { target: targetLabel });
    try {
      const result = await SetAllStationsPowerDetailed(state);
      if (!this.host.gates.canCommitOperation(operationEpoch)) return;
      this.host.mergeStationUpdates(result.results.map((item) => item.station).filter((item) => Boolean(item?.address)));
      await this.fetchLatestList();
      if (!this.host.gates.canCommitOperation(operationEpoch)) return;
      for (const item of result.results) {
        const feedback: Pick<PowerFeedback, 'kind' | 'text'> = item.skipped
          ? item.success && item.confirmed
            ? { kind: 'success', text: t('Already {target}', { target: targetLabel }) }
            : { kind: 'warning', text: t('Skipped · {reason}', { reason: item.reason || t('not actionable') }) }
            : item.success && item.confirmed
              ? { kind: 'success', text: t('{target} confirmed', { target: targetLabel }) }
              : item.success && item.commandSent
              ? { kind: 'warning', text: t('{target} sent · {detail}', { target: targetLabel, detail: item.error || t('status unavailable') }) }
              : { kind: 'error', text: item.error || t('Failed to set {target}', { target: targetLabel }) };
        this.host.powerFeedback.set(item.address, {
          ...feedback,
          target: state,
          readAt: item.station?.lastPowerReadAt
        });
      }
      const summary = summarizeBulkResult(result.results);
      if (!this.host.gates.canCommitStatus(statusOperation)) return;
      const summaryText = formatBulkResult(targetLabel, summary);
      this.host.statusMessage = result.timedOut
        ? `${t('Bulk {target} timed out', { target: targetLabel })}: ${summaryText}`
        : result.cancelled
          ? `${t('Bulk {target} cancelled', { target: targetLabel })}: ${summaryText}`
          : summaryText;
      const toastKind = result.timedOut || result.cancelled ? 'warning'
        : summary.failed.length ? 'error'
          : summary.unconfirmed || summary.skipped ? 'warning'
            : 'success';
      const toastMessage = result.timedOut || result.cancelled
        ? this.host.statusMessage
        : `Bulk ${targetLabel}: ${summaryText}`;
      pushToast(toastMessage, toastKind);
    } catch (error) {
      if (!this.host.gates.canCommitOperation(operationEpoch)) return;
      await this.fetchLatestList();
      if (!this.host.gates.canCommitOperation(operationEpoch)) return;
      if (this.host.gates.canCommitStatus(statusOperation)) {
        this.host.statusMessage = `${t('Bulk {target} operation partially failed', { target: targetLabel })}: ${String(error)}`;
        pushToast(this.host.statusMessage);
      }
    } finally {
      if (!this.host.disposed) {
        if (this.host.globalOperation === 'bulk-power') this.host.globalOperation = 'idle';
        this.host.bulkTarget = null;
        this.host.cancellingBulk = false;
      }
    }
  }

  async cancelBulkPower() {
    if (this.host.globalOperation !== 'bulk-power' || this.host.cancellingBulk) return;
    this.host.cancellingBulk = true;
    // Reuse the running bulk's status epoch instead of starting a new one: a
    // fresh epoch would invalidate the bulk's terminal "cancelled" summary
    // write. Committing under the captured epoch still drops this message when
    // a newer status owner takes over mid-cancellation.
    const statusOperation = this.host.gates.currentStatusEpoch;
    this.host.statusMessage = t('Stopping bulk power...');
    try {
      await CancelBulkPower();
    } catch (error) {
      if (!this.host.disposed) {
        const message = `${t('Cancel bulk power')}: ${String(error)}`;
        if (this.host.gates.canCommitStatus(statusOperation)) this.host.statusMessage = message;
        pushToast(message);
      }
    } finally {
      if (!this.host.disposed) this.host.cancellingBulk = false;
    }
  }

  startRename(station: StationInfo) {
    if (this.host.stationBusy(station.address) || this.host.stationLocked) return;
    this.host.editingAddress = station.address;
  }

  cancelRename() {
    this.host.editingAddress = null;
  }

  async saveRename(station: StationInfo, name: string) {
    if (this.host.configBusy(station.address)) {
      // This rename's own save is still settling; a repeated Enter or blur
      // must not be reported as a conflict with itself.
      return;
    }
    if (this.host.stationBusy(station.address) || this.host.stationLocked) {
      // Keep the row open for a retry and explain why the submission did
      // nothing; a silent rejection leaves the user typing into a dead input.
      const statusOperation = this.host.gates.beginStatusOperation();
      const reason = t('Rename blocked: another operation is in progress for {name}.', { name: station.name });
      if (this.host.gates.canCommitStatus(statusOperation)) this.host.statusMessage = reason;
      pushToast(reason, 'warning');
      return;
    }
    if (name === station.name) {
      this.cancelRename();
      return;
    }
    this.host.setConfigBusy(station.address, true);
    const statusOperation = this.host.gates.beginStatusOperation();
    const operationEpoch = this.host.gates.currentScanEpoch;
    const operationRevision = this.host.gates.beginStationOperationRevision(station.address);
    try {
      await RenameStationByAddress(station.address, name);
      if (!this.host.gates.canCommitStationOperation(operationEpoch, station.address, operationRevision)) return;
      await this.fetchStationUpdate(station.address, operationEpoch, operationRevision);
      if (!this.host.gates.canCommitStationOperation(operationEpoch, station.address, operationRevision)) return;
      this.host.stations = this.host.stations.map((current) => current.address === station.address
        ? this.host.withStationChanges(current, { name: name || current.originalName })
        : current);
      if (this.host.gates.canCommitStatus(statusOperation)) this.host.statusMessage = name ? t('Renamed to {name}.', { name }) : t('Reset name for {name}.', { name: station.originalName });
      // Close only the row this save owns: while this save was in flight the
      // user can open another station's rename, and its draft must survive.
      if (this.host.editingAddress === station.address) this.cancelRename();
    } catch (error) {
      if (!this.host.gates.canCommitStationOperation(operationEpoch, station.address, operationRevision)) return;
      if (this.host.gates.canCommitStatus(statusOperation)) {
        this.host.statusMessage = withDetail('Error renaming', error);
        pushToast(this.host.statusMessage);
      }
    } finally {
      this.host.apiStatus.refresh();
      if (this.host.gates.canCleanupStationOperation(station.address, operationRevision)) {
        this.host.setConfigBusy(station.address, false);
      }
    }
  }

  async identify(station: StationInfo) {
    if (this.host.stationBusy(station.address) || this.host.gattLockedFor(station.address)) return;
    this.host.setGattBusy(station.address, true);
    const statusOperation = this.host.gates.beginStatusOperation();
    const operationEpoch = this.host.gates.currentScanEpoch;
    const operationRevision = this.host.gates.beginStationOperationRevision(station.address);
    try {
      await IdentifyStation(station.address);
      if (!this.host.gates.canCommitStationOperation(operationEpoch, station.address, operationRevision)) return;
      await this.fetchStationUpdate(station.address, operationEpoch, operationRevision);
      if (!this.host.gates.canCommitStationOperation(operationEpoch, station.address, operationRevision)) return;
      if (this.host.gates.canCommitStatus(statusOperation)) this.host.statusMessage = t('Identify signal sent to {name}.', { name: station.name });
    } catch (error) {
      if (!this.host.gates.canCommitStationOperation(operationEpoch, station.address, operationRevision)) return;
      await this.fetchStationUpdate(station.address, operationEpoch, operationRevision);
      if (!this.host.gates.canCommitStationOperation(operationEpoch, station.address, operationRevision)) return;
      if (this.host.gates.canCommitStatus(statusOperation)) {
        this.host.statusMessage = `${t('Identify failed for {name}', { name: station.name })}: ${String(error)}`;
        pushToast(this.host.statusMessage);
      }
    } finally {
      if (this.host.gates.canCleanupStationOperation(station.address, operationRevision)) {
        this.host.setGattBusy(station.address, false);
      }
    }
  }

  async refreshCapabilities(station: StationInfo) {
    if (this.host.stationBusy(station.address) || this.host.gattLockedFor(station.address)) return;
    this.host.setGattBusy(station.address, true);
    const statusOperation = this.host.gates.beginStatusOperation();
    const operationEpoch = this.host.gates.currentScanEpoch;
    const operationRevision = this.host.gates.beginStationOperationRevision(station.address);
    try {
      const updated = await RefreshStationCapabilities(station.address);
      if (!this.host.gates.canCommitStationOperation(operationEpoch, station.address, operationRevision)) return;
      this.host.stations = this.host.stations.map((current) => current.address === station.address ? updated : current);
      if (this.host.gates.canCommitStatus(statusOperation)) {
        const message = updated.lastError
          ? `${t('Capabilities refreshed for {name}, but some values are unavailable', { name: station.name })}: ${updated.lastError}`
          : t('Capabilities refreshed for {name}.', { name: station.name });
        this.host.statusMessage = message;
        if (updated.lastError) pushToast(message, 'warning');
      }
    } catch (error) {
      if (!this.host.gates.canCommitStationOperation(operationEpoch, station.address, operationRevision)) return;
      await this.fetchStationUpdate(station.address, operationEpoch, operationRevision);
      if (!this.host.gates.canCommitStationOperation(operationEpoch, station.address, operationRevision)) return;
      if (this.host.gates.canCommitStatus(statusOperation)) {
        this.host.statusMessage = `${t('Capability refresh failed for {name}', { name: station.name })}: ${String(error)}`;
        pushToast(this.host.statusMessage);
      }
    } finally {
      if (this.host.gates.canCleanupStationOperation(station.address, operationRevision)) {
        this.host.setGattBusy(station.address, false);
      }
    }
  }

  clearChannelEditorFeedback() {
    this.host.channelError = '';
    this.host.channelWarning = false;
  }

  async saveChannel(station: StationInfo, targetChannel: number, allowUnknownConflictRisk: boolean) {
    if (!station) return;
    const blockedReason = channelChangeBlockedReason(station);
    if (blockedReason) {
      this.host.channelError = blockedReason;
      this.host.channelWarning = true;
      return;
    }
    if (this.host.stationBusy(station.address) || this.host.gattLockedFor(station.address) ||
      (hasCurrentChannel(station) && station.channel === targetChannel)) return;
    const address = station.address;
    // Capture the display name: the station can drop out of the list while
    // the write is in flight, and the post-await status messages must not
    // dereference a null selection.
    const stationName = station.name;
    const statusOperation = this.host.gates.beginStatusOperation();
    const operationEpoch = this.host.gates.currentScanEpoch;
    const operationRevision = this.host.gates.beginStationOperationRevision(address);
    this.host.setGattBusy(address, true);
    this.host.channelSavingAddress = address;
    this.host.channelError = '';
    this.host.channelWarning = false;
    try {
      const result = await SetStationChannel(address, targetChannel, allowUnknownConflictRisk);
      if (!this.host.gates.canCommitStationOperation(operationEpoch, address, operationRevision)) return;
      let actual = result.station?.address ? result.station : null;
      if (actual) {
        this.host.mergeStationUpdates([actual]);
      } else {
        actual = await this.fetchStationUpdate(address, operationEpoch, operationRevision);
        if (!this.host.gates.canCommitStationOperation(operationEpoch, address, operationRevision)) return;
      }
      if (result.confirmed === false) {
        const warning = result.confirmationError || t('Channel readback is unavailable.');
        this.host.channelError = `${t('Channel command sent but unconfirmed')}: ${warning} ${t('Readback')}: ${this.channelReadbackLabel(actual)}.`;
        this.host.channelWarning = true;
        if (this.host.gates.canCommitStatus(statusOperation)) this.host.statusMessage = t('{name}: channel command sent, but confirmation failed. {detail}', { name: stationName, detail: warning });
        return;
      }
      if (!actual) {
        this.host.commitStations(this.host.stations.map((item) => item.address === address
          ? this.host.withStationChanges(item, { channel: result.channel })
          : item));
      }
      this.host.ui.forceCloseChannelEditor();
      if (this.host.gates.canCommitStatus(statusOperation)) this.host.statusMessage = result.commandSent
        ? t('Channel changed from {previous} to {channel}. {warnings}', { previous: result.previousChannel || t('unknown'), channel: result.channel, warnings: result.warnings.join(' ') })
        : t('Channel already set to {channel}; no command was sent. {warnings}', { channel: result.channel, warnings: result.warnings.join(' ') });
    } catch (error) {
      if (!this.host.gates.canCommitStationOperation(operationEpoch, address, operationRevision)) return;
      const actual = await this.fetchStationUpdate(address, operationEpoch, operationRevision);
      if (!this.host.gates.canCommitStationOperation(operationEpoch, address, operationRevision)) return;
      this.host.channelError = `${String(error)} ${t('Readback')}: ${this.channelReadbackLabel(actual)}.`;
      this.host.channelWarning = false;
      if (this.host.gates.canCommitStatus(statusOperation)) {
        this.host.statusMessage = `${t('Channel change failed')}: ${this.host.channelError}`;
        pushToast(this.host.statusMessage);
      }
    } finally {
      if (this.host.gates.canCleanupStationOperation(address, operationRevision)) {
        this.host.setGattBusy(address, false);
        if (this.host.channelSavingAddress === address) this.host.channelSavingAddress = null;
      }
    }
  }

}
