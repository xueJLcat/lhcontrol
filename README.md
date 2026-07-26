# lhcontrol

[**⬇️ Get the latest Windows Installer**](https://github.com/FlameInTheDark/lhcontrol/releases/latest/download/lhcontrol-amd64-installer.exe)

![Application Screenshot](<./screenshot.png>)

A simple application to control Valve Lighthouse (SteamVR) base stations v2.0 power state via Bluetooth LE.

lhcontrol is a Windows-only application. Its bundled Bluetooth dependency
contains Windows WinRT stability patches and is not intended to compile on
Linux or macOS.

## Features

*   Scan for nearby Lighthouse base stations.
*   Display all Lighthouse 2.0 power states (Sleep/Standby/Booting/On/Unknown).
*   Display the optical channel (1-16) reported by Lighthouse 2.0 stations.
*   Set individual stations to On, Standby, or Sleep.
*   Identify a physical station by flashing its LED when supported.
*   Detect channel conflicts and safely change a channel with mandatory readback.
*   Display firmware, hardware, model, serial-number, manufacturer, and BLE capabilities.
*   Power On/Off all base stations discovered during the current app session.
*   Rename base stations (locally) for easier identification.
*   Persistent list of discovered stations across scans (within a single app session).

## Technology Stack

*   **Framework:** [Wails v2](https://wails.io/)
*   **Backend:** Go
*   **Frontend:** Svelte
*   **Bluetooth:** [TinyGo Bluetooth Library](https://github.com/tinygo-org/bluetooth)

## Prerequisites

*   **Operating system:** Windows 10 or Windows 11, x64.
*   **Bluetooth Adapter:** A Windows-compatible Bluetooth Low Energy adapter with current drivers.
*   **Go:** Version 1.25.12.
*   **Node.js:** Version 22.14 or newer in the Node 22 release line.
*   **Wails CLI:** Install the project version with `go install github.com/wailsapp/wails/v2/cmd/wails@v2.13.0`.
*   **NSIS:** Version 3.12 is required only when building the Windows installer with `wails build -nsis`.

## Setup

1.  **Clone the repository:**
    ```bash
    git clone https://github.com/FlameInTheDark/lhcontrol
    cd lhcontrol
    ```
2.  **Install frontend dependencies:**
    Wails typically handles this automatically during the build, but you can run it manually if needed:
    ```bash
    cd frontend
    npm ci
    cd ..
    ```

## Running the Application

*   **Development Mode:** (Live reload)
    ```bash
    wails dev
    ```
*   **Production Build:**
    ```bash
    wails build
    ```
    This creates a Windows executable in the `build/bin` directory. A pre-built
    installer (`lhcontrol-amd64-installer.exe`) may also be available in the
    project's releases.

## Usage

1.  Launch the application.
2.  Click **Scan** to discover nearby base stations.
3.  The application will attempt to connect to discovered stations to determine their power state.
4.  Use the **Power** menu to select On, Standby, or Sleep.
5.  Open **Details** to inspect capabilities and metadata, identify a station, or safely change its channel.
6.  Use the **All** controls to change all stations discovered during the current app session, including stations missed by the latest scan.

## Troubleshooting

*   **Scanning Issues:** If scans fail after the first time, or interactions fail with errors like "characteristic not found", try removing the base station(s) from your operating system's Bluetooth device list and restarting your computer. Do *not* re-pair them in the OS settings; the application will find them via scanning.
*   **Bluetooth unavailable:** Turn Bluetooth back on or reconnect the adapter, wait two seconds, and scan again. The running application retries adapter initialization without requiring a restart.
*   **Bluetooth Drivers:** Ensure you have the latest drivers for your Bluetooth adapter.
*   **Permissions:** The application might require specific permissions to access Bluetooth hardware.

### Windows hardware verification

With the application running, execute `.\scripts\hardware-smoke.ps1` to run ten scan cycles, verify stable station ordering, and save a timed report under `build\verification`. Use `-MinimumStations`, `-ExpectedAddresses`, and `-ScanCycles` to define the hardware acceptance set; station assertions apply to devices actually discovered in every scan, not historical entries. Failed scans and power requests retain their raw status, station snapshot, duration, and request or validation error in the report. Add `-ExercisePower` only when it is safe to test On, Standby, and Sleep on every known base station; the script requires confirmed initial states, validates each readback, restores them in a `finally` block, and exits unsuccessfully if restoration fails. Run `.\scripts\hardware-smoke.ps1 -SelfTest` to validate the reporting and assertion logic without Bluetooth hardware.
*   **Diagnostic log:** `%APPDATA%\lhcontrol\lhcontrol.log` is capped at 5 MB. One previous segment is retained as `lhcontrol.log.1`.

## HTTP API (for External Integration)

This application also exposes a simple HTTP API on `http://127.0.0.1:7575` for basic control and status monitoring from external scripts or applications.

**Endpoints:**

*   **`POST /allon`**
    *   **Description:** Attempts to turn ON all base stations known in the current app session.
    *   **Request Body:** None
    *   **Response:** `200 OK` when every command was sent or skipped. Use `POST /stations/power` when per-station confirmation details are required. Returns `409 Conflict` if another Bluetooth operation is active.

*   **`POST /alloff`**
    *   **Description:** Attempts to put all base stations known in the current app session into Sleep.
    *   **Request Body:** None
    *   **Response:** `200 OK` when every command was sent or skipped. Use `POST /stations/power` when per-station confirmation details are required. Returns `409 Conflict` if another Bluetooth operation is active.

*   **`GET /status`**
    *   **Description:** Returns the current list of known base stations and their states.
    *   **Request Body:** None
    *   **Response:** `200 OK` with JSON body:
        ```json
        [
          {
            "name": "LHB-STATION1_RENAMED",
            "originalName": "LHB-XXXXXXXX",
            "address": "XX:XX:XX:XX:XX:XX",
            "powerState": 1,
            "channel": 3
          },
          {
            "name": "LHB-YYYYYYYY",
            "originalName": "LHB-YYYYYYYY",
            "address": "YY:YY:YY:YY:YY:YY",
            "powerState": 0,
            "channel": 8
          }
          // ... more stations
        ]
        ```
        (Power States: -1 = Unknown, 0 = Sleep, 1 = On, 2 = Standby, 3 = Booting. Channel: 0 = Unknown, 1-16 = optical channel.)

*   **`POST /scan`**
    *   **Description:** Triggers a background scan for base stations (approx. 5s scan + 7s state fetch). The list returned by `/status` will update once complete.
    *   **Request Body:** None
    *   **Response:** `202 Accepted` when the scan starts; `409 Conflict` if another Bluetooth operation is active.

*   **`GET /scan/status`**
    *   **Description:** Returns scan state, timestamps, warnings, and the number actually seen in the most recent scan.

*   **`POST /scan/stop`**
    *   **Description:** Cancels an active scan and waits for its workflow to finish. Safe to call when no scan is active.
    *   **Response:** `204 No Content`. The terminal scan status is `cancelled`.

*   **`POST /stations/power`**
    *   **Body:** `{"state":"on"}`, `{"state":"standby"}`, or `{"state":"sleep"}`.
    *   **Response:** A structured result for every known station, including command-sent, success, confirmation, and error fields.

*   **`POST /stations/:address/power`**
    *   **Body:** `{"state":"on"}`, `{"state":"standby"}`, or `{"state":"sleep"}`.
    *   **Response:** `200 OK` with the updated station, `commandSent`, `confirmed`, and `confirmationError`. A command that was sent but could not be confirmed is represented by `confirmed: false`; failures before the write retain their normal 4xx/5xx status.

*   **`POST /stations/:address/identify`**
    *   **Description:** Flashes the selected physical station when Identify is supported.

*   **`POST /stations/:address/refresh`**
    *   **Description:** Forces service/characteristic discovery and refreshes capabilities, metadata, power, and channel values.

*   **`PUT /stations/:address/channel`**
    *   **Body:** `{"channel":5}` (valid range: 1-16).
    *   **Description:** Rejects visible conflicts, writes the channel, and succeeds only after readback matches.

**Example Usage (curl):**

```bash
# Get current status
curl http://127.0.0.1:7575/status

# Turn all base stations ON
curl -X POST http://127.0.0.1:7575/allon

# Turn all base stations OFF
curl -X POST http://127.0.0.1:7575/alloff
```

## Release Hardware Checklist

Automated tests cannot emulate Windows WinRT or a physical Lighthouse. Before
publishing a release on a Bluetooth-enabled Windows machine:

1. Run at least 10 consecutive scans and verify the process remains stable.
2. Exit during a scan and confirm shutdown completes without a crash.
3. Exercise On, Standby, and Sleep for one station and for all known stations.
4. Verify command readback, channel conflict rejection, and a successful channel change.
5. Disable and re-enable Bluetooth, then verify scanning and device controls recover.
