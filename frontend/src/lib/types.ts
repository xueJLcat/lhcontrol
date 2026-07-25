export interface Capabilities {
  powerRead: boolean; powerWrite: boolean; powerNotify: boolean; standby: boolean;
  channelRead: boolean; channelWrite: boolean; channelNotify: boolean;
  identify: boolean; deviceInformation: boolean;
}

export interface Metadata {
  manufacturer: string; model: string; serialNumber: string;
  hardwareRevision: string; firmwareRevision: string;
}

export interface StationInfo {
  name: string; originalName: string; address: string;
  powerState: number; powerStateName: string; powerStateConfirmed: boolean; rawPowerState: number;
  channel: number; channelConflict: boolean; isPresent: boolean;
  seenInLatestScan: boolean; scanFresh: boolean; missedScans: number;
  lastSeenAt: string; lastReadAt: string; lastError: string;
  lastPowerReadAt: string; lastChannelReadAt: string; metadataReadAt: string;
  statusFresh: boolean; powerFresh: boolean; channelFresh: boolean; metadataFresh: boolean;
  connectionState: string; capabilitiesKnown: boolean;
  capabilities: Capabilities; metadata: Metadata;
}

export type PowerTarget = 'on' | 'standby' | 'sleep';

export interface PowerFeedback {
  kind: 'pending' | 'success' | 'warning' | 'error';
  text: string;
}
