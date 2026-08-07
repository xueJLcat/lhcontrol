export namespace autosleep {
	
	export class Settings {
	    enabled: boolean;
	    target: string;
	    delaySeconds: number;
	
	    static createFrom(source: any = {}) {
	        return new Settings(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.enabled = source["enabled"];
	        this.target = source["target"];
	        this.delaySeconds = source["delaySeconds"];
	    }
	}

}

export namespace bluetooth {
	
	export class AdapterInfo {
	    deviceId: string;
	    name: string;
	
	    static createFrom(source: any = {}) {
	        return new AdapterInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.deviceId = source["deviceId"];
	        this.name = source["name"];
	    }
	}
	export class Capabilities {
	    powerRead: boolean;
	    powerWrite: boolean;
	    powerNotify: boolean;
	    standby: boolean;
	    channelRead: boolean;
	    channelWrite: boolean;
	    channelNotify: boolean;
	    identify: boolean;
	    deviceInformation: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Capabilities(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.powerRead = source["powerRead"];
	        this.powerWrite = source["powerWrite"];
	        this.powerNotify = source["powerNotify"];
	        this.standby = source["standby"];
	        this.channelRead = source["channelRead"];
	        this.channelWrite = source["channelWrite"];
	        this.channelNotify = source["channelNotify"];
	        this.identify = source["identify"];
	        this.deviceInformation = source["deviceInformation"];
	    }
	}
	export class DeviceMetadata {
	    manufacturer: string;
	    model: string;
	    serialNumber: string;
	    hardwareRevision: string;
	    firmwareRevision: string;
	
	    static createFrom(source: any = {}) {
	        return new DeviceMetadata(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.manufacturer = source["manufacturer"];
	        this.model = source["model"];
	        this.serialNumber = source["serialNumber"];
	        this.hardwareRevision = source["hardwareRevision"];
	        this.firmwareRevision = source["firmwareRevision"];
	    }
	}

}

export namespace main {
	
	export class APIStatus {
	    running: boolean;
	    address: string;
	    error: string;
	    warnings: string[];
	    configWritable: boolean;
	
	    static createFrom(source: any = {}) {
	        return new APIStatus(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.running = source["running"];
	        this.address = source["address"];
	        this.error = source["error"];
	        this.warnings = source["warnings"];
	        this.configWritable = source["configWritable"];
	    }
	}

}

export namespace station {
	
	export class StationInfo {
	    name: string;
	    originalName: string;
	    address: string;
	    powerState: number;
	    powerStateName: string;
	    powerStateConfirmed: boolean;
	    rawPowerState: number;
	    channel: number;
	    channelConflict: boolean;
	    isPresent: boolean;
	    presenceUncertain: boolean;
	    seenInLatestScan: boolean;
	    scanFresh: boolean;
	    missedScans: number;
	    lastSeenAt: string;
	    lastReadAt: string;
	    lastPowerReadAt: string;
	    lastChannelReadAt: string;
	    metadataReadAt: string;
	    lastError: string;
	    statusFresh: boolean;
	    powerFresh: boolean;
	    channelFresh: boolean;
	    metadataFresh: boolean;
	    connectionState: string;
	    capabilitiesKnown: boolean;
	    capabilities: bluetooth.Capabilities;
	    metadata: bluetooth.DeviceMetadata;
	
	    static createFrom(source: any = {}) {
	        return new StationInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.originalName = source["originalName"];
	        this.address = source["address"];
	        this.powerState = source["powerState"];
	        this.powerStateName = source["powerStateName"];
	        this.powerStateConfirmed = source["powerStateConfirmed"];
	        this.rawPowerState = source["rawPowerState"];
	        this.channel = source["channel"];
	        this.channelConflict = source["channelConflict"];
	        this.isPresent = source["isPresent"];
	        this.presenceUncertain = source["presenceUncertain"];
	        this.seenInLatestScan = source["seenInLatestScan"];
	        this.scanFresh = source["scanFresh"];
	        this.missedScans = source["missedScans"];
	        this.lastSeenAt = source["lastSeenAt"];
	        this.lastReadAt = source["lastReadAt"];
	        this.lastPowerReadAt = source["lastPowerReadAt"];
	        this.lastChannelReadAt = source["lastChannelReadAt"];
	        this.metadataReadAt = source["metadataReadAt"];
	        this.lastError = source["lastError"];
	        this.statusFresh = source["statusFresh"];
	        this.powerFresh = source["powerFresh"];
	        this.channelFresh = source["channelFresh"];
	        this.metadataFresh = source["metadataFresh"];
	        this.connectionState = source["connectionState"];
	        this.capabilitiesKnown = source["capabilitiesKnown"];
	        this.capabilities = this.convertValues(source["capabilities"], bluetooth.Capabilities);
	        this.metadata = this.convertValues(source["metadata"], bluetooth.DeviceMetadata);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class BulkPowerStationResult {
	    address: string;
	    name: string;
	    skipped: boolean;
	    reason: string;
	    commandSent: boolean;
	    success: boolean;
	    confirmed: boolean;
	    error: string;
	    station: StationInfo;
	
	    static createFrom(source: any = {}) {
	        return new BulkPowerStationResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.address = source["address"];
	        this.name = source["name"];
	        this.skipped = source["skipped"];
	        this.reason = source["reason"];
	        this.commandSent = source["commandSent"];
	        this.success = source["success"];
	        this.confirmed = source["confirmed"];
	        this.error = source["error"];
	        this.station = this.convertValues(source["station"], StationInfo);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class BulkPowerResult {
	    target: string;
	    results: BulkPowerStationResult[];
	    cancelled: boolean;
	    timedOut: boolean;
	
	    static createFrom(source: any = {}) {
	        return new BulkPowerResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.target = source["target"];
	        this.results = this.convertValues(source["results"], BulkPowerStationResult);
	        this.cancelled = source["cancelled"];
	        this.timedOut = source["timedOut"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	export class ChannelChangeResult {
	    address: string;
	    previousChannel: number;
	    channel: number;
	    commandSent: boolean;
	    confirmed: boolean;
	    confirmationError: string;
	    warnings: string[];
	    station: StationInfo;
	
	    static createFrom(source: any = {}) {
	        return new ChannelChangeResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.address = source["address"];
	        this.previousChannel = source["previousChannel"];
	        this.channel = source["channel"];
	        this.commandSent = source["commandSent"];
	        this.confirmed = source["confirmed"];
	        this.confirmationError = source["confirmationError"];
	        this.warnings = source["warnings"];
	        this.station = this.convertValues(source["station"], StationInfo);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class PowerActionResult {
	    station: StationInfo;
	    commandSent: boolean;
	    skipped: boolean;
	    reason?: string;
	    confirmed: boolean;
	    confirmationError: string;
	
	    static createFrom(source: any = {}) {
	        return new PowerActionResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.station = this.convertValues(source["station"], StationInfo);
	        this.commandSent = source["commandSent"];
	        this.skipped = source["skipped"];
	        this.reason = source["reason"];
	        this.confirmed = source["confirmed"];
	        this.confirmationError = source["confirmationError"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class ScanStatus {
	    state: string;
	    startedAt: string;
	    completedAt: string;
	    error: string;
	    warnings: string[];
	    found: number;
	
	    static createFrom(source: any = {}) {
	        return new ScanStatus(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.state = source["state"];
	        this.startedAt = source["startedAt"];
	        this.completedAt = source["completedAt"];
	        this.error = source["error"];
	        this.warnings = source["warnings"];
	        this.found = source["found"];
	    }
	}

}

