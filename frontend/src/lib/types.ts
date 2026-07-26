import type { bluetooth, station } from '../../wailsjs/go/models';

export type Capabilities = bluetooth.Capabilities;
export type Metadata = bluetooth.DeviceMetadata;
export type StationInfo = station.StationInfo;
export type BulkPowerResult = station.BulkPowerResult;

export type PowerTarget = 'on' | 'standby' | 'sleep';

export interface PowerFeedback {
  kind: 'pending' | 'success' | 'warning' | 'error';
  text: string;
}
