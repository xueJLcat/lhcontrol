# lhcontrol

[**⬇️ Get the latest Windows Installer**](https://github.com/FlameInTheDark/lhcontrol/releases/latest/download/lhcontrol-amd64-installer.exe)

![Application Screenshot](<./screenshot.png>)

A simple application to control Valve Lighthouse (SteamVR) base stations v2.0 power state via Bluetooth LE.

## Features

*   Scan for nearby Lighthouse base stations.
*   Display all Lighthouse 2.0 power states (Sleep/Standby/Booting/On/Unknown).
*   Display the optical channel (1-16) reported by Lighthouse 2.0 stations.
*   Set individual stations to On, Standby, or Sleep.
*   Identify a physical station by flashing its LED when supported.
*   Detect channel conflicts and safely change a channel with mandatory readback.
*   Display firmware, hardware, model, serial-number, manufacturer, and BLE capabilities.
*   Power On/Off all known base stations simultaneously.
*   Rename base stations (locally) for easier identification.
*   Persistent list of discovered stations across scans (within a single app session).

## Technology Stack

*   **Framework:** [Wails v2](https://wails.io/)
*   **Backend:** Go
*   **Frontend:** Svelte
*   **Bluetooth:** [TinyGo Bluetooth Library](https://github.com/tinygo-org/bluetooth)

## Prerequisites

*   **Bluetooth Adapter:** You MUST have a working Bluetooth adapter compatible with your OS that supports **Bluetooth Low Energy (BLE)**. Many built-in adapters work, but dedicated USB adapters can sometimes offer better performance/compatibility.
*   **Go:** Version 1.18 or higher.
*   **Node.js & npm:** Required by Wails for frontend dependencies.
*   **Wails CLI:** Install via `go install github.com/wailsapp/wails/v2/cmd/wails@latest`.
*   **TinyGo:** While the main build uses the standard Go compiler, the `tinygo/bluetooth` library is used. Ensure required system dependencies for BLE development are met (e.g., build-essential, libbluetooth-dev on Debian/Ubuntu).

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
    npm install
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
    This will create an executable in the `build/bin` directory.
    Alternatively, for Windows users, a pre-built installer (`lhcontrol-amd64-installer.exe`) may be available in the project's releases.

## Usage

1.  Launch the application.
2.  Click **Scan** to discover nearby base stations.
3.  The application will attempt to connect to discovered stations to determine their power state.
4.  Use the **Power** menu to select On, Standby, or Sleep.
5.  Open **Details** to inspect capabilities and metadata, identify a station, or safely change its channel.
6.  Use the **All** controls to change all stations retained as visible by the latest reliable scans.

## Troubleshooting

*   **Scanning Issues:** If scans fail after the first time, or interactions fail with errors like "characteristic not found", try removing the base station(s) from your operating system's Bluetooth device list and restarting your computer. Do *not* re-pair them in the OS settings; the application will find them via scanning.
*   **Bluetooth Drivers:** Ensure you have the latest drivers for your Bluetooth adapter.
*   **Permissions:** The application might require specific permissions to access Bluetooth hardware.

## HTTP API (for External Integration)

This application also exposes a simple HTTP API on `http://127.0.0.1:7575` for basic control and status monitoring from external scripts or applications.

**Endpoints:**

*   **`POST /allon`**
    *   **Description:** Attempts to turn ON all known base stations.
    *   **Request Body:** None
    *   **Response:** `200 OK` after all stations confirm the ON state; `409 Conflict` if another Bluetooth operation is active.

*   **`POST /alloff`**
    *   **Description:** Attempts to turn OFF all known base stations.
    *   **Request Body:** None
    *   **Response:** `200 OK` after all stations confirm the OFF state; `409 Conflict` if another Bluetooth operation is active.

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

*   **`POST /stations/power`**
    *   **Body:** `{"state":"on"}`, `{"state":"standby"}`, or `{"state":"sleep"}`.
    *   **Response:** A structured result for every attempted visible station, including command-sent, success, confirmation, and error fields.

*   **`POST /stations/:address/power`**
    *   **Body:** `{"state":"on"}`, `{"state":"standby"}`, or `{"state":"sleep"}`.
    *   **Response:** The updated station plus a `confirmed` flag. A writable-only firmware returns `confirmed: false`.

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
