# AltHelper — iOS Sideload Assistant for Linux

[![CI & Release](https://github.com/artcevvv/Althelper/actions/workflows/release.yml/badge.svg)](https://github.com/artcevvv/Althelper/actions/workflows/release.yml)
[![GitHub release](https://img.shields.io/github/v/release/artcevvv/Althelper?include_prereleases)](https://github.com/artcevvv/Althelper/releases)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

**AltHelper** is a native Linux desktop application that streamlines and automates sideloading `.ipa` packages onto iOS devices (iPhone and iPad) using your Apple ID and patched AltServer binaries.

Instead of wrestling with manual Docker commands, missing environment variables, detached 2FA terminal prompts, or fragmented forks, AltHelper wraps the entire workflow into a responsive, dual-pane GUI with integrated health checking and live console output.

---

## Table of Contents

- [Purpose](#purpose)
- [How It Works](#how-it-works)
- [Features](#features)
- [Privacy & Security](#privacy--security)
- [Prerequisites](#prerequisites)
- [Installation & Getting Started](#installation--getting-started)
  - [Option A: AppImage (Recommended)](#option-a-appimage-recommended)
  - [Option B: Building from Source](#option-b-building-from-source)
- [Step-by-Step Usage Guide](#step-by-step-usage-guide)
- [Troubleshooting & Caveats](#troubleshooting--caveats)
- [License](#license)

---

## Purpose

Sideloading iOS applications on Linux via `AltServer-Linux` is powerful, but often fragile and tedious:
- Apple requires cryptographic **Anisette machine data** for authentication, which necessitates running an emulated provisioning service.
- The original `NyaMisty/AltServer-Linux` repository is unmaintained, requiring patched community forks (such as `jaakkopalvaila/AltServer-Linux`) that fix Apple sign-in blocks and modern iOS code-signing crashes.
- Apple IDs with **Two-Factor Authentication (2FA)** require interactive terminal input that breaks headless or naive background process managers.
- USB device detection requires coordinated communication through `usbmuxd`.

**AltHelper** automates and unifies these moving pieces into an intuitive interface, keeping you in full control without needing to manually run complex terminal sequences every week.

---

## How It Works

AltHelper acts as a coordinator between your local system services, container runtime, and device bridge:

```
┌────────────────────────────────────────────────────────┐
│                   AltHelper (GUI)                      │
│                                                        │
│  [1. Anisette]   [2. AltServer]   [3. Device & App]    │
│        │                │                 │            │
└────────┼────────────────┼─────────────────┼────────────┘
         │                │                 │
         ▼                ▼                 ▼
 ┌───────────────┐ ┌───────────────┐ ┌───────────────┐
 │ Docker Engine │ │ AltServer-    │ │   usbmuxd /   │
 │ (anisette-v3) │ │ Linux Binary  │ │  idevice_id   │
 └───────┬───────┘ └───────┬───────┘ └───────┬───────┘
         │                 │                 │
         │ :6969 HTTP      │ Signing Data    │ USB / Lightning
         └────────────────►│◄────────────────┘
                           │
                           ▼
                  ┌─────────────────┐
                  │   iOS Device    │
                  │ (Installed App) │
                  └─────────────────┘
```

1. **Anisette Provisioning**: Manages a local Docker container (`dadoum/anisette-v3-server`) bound to `127.0.0.1:6969` with persistent machine data (`anisette-v3_data` volume) so authentication tokens persist across runs.
2. **AltServer Execution**: Sets required environment variables (`ALTSERVER_ANISETTE_SERVER=http://127.0.0.1:6969`), passes target UDID, credentials, and payload, and pipes live status output to the GUI.
3. **Interactive 2FA Handling**: Watches process standard output for Apple verification challenges, displays a native modal dialog, and streams the user-entered code directly to AltServer's standard input.

---

## Features

- **Docker Anisette Management**:
  - 1-click container start, stop, and status monitoring.
  - Active HTTP healthchecks (`GET http://127.0.0.1:6969/`) verifying readiness before initiating installations.
  - Automatic detection and remediation of unmapped host ports.
- **AltServer Binary Management**:
  - Automatic CPU architecture detection (`x86_64`, `aarch64`, `armv7l`).
  - Automatic 1-click download of the latest patched fork releases directly from GitHub.
  - Local binary file picker with automatic permission enforcement (`chmod +x`).
  - Persistent binary path saving across sessions.
- **Device Connectivity**:
  - Live USB device scanning via `idevice_id`.
  - Optional Wi-Fi mode support (configurable endpoint for `netmuxd`).
- **Seamless 2FA Support**:
  - Interactive two-factor authentication dialog that pauses the background runner and feeds codes to AltServer on demand.
- **Config & Credential Persistence**:
  - Optional, opt-in profile saving to local config (`~/.local/share/althelper/profile.json`) with strict Unix file permissions (`0600`).
- **Lag-Free Terminal Activity Log**:
  - Asynchronous, batched log streaming (capped at 16 FPS) that prevents interface freezing during verbose signing operations.
  - Monospace formatting and a dedicated "Clear Log" control.

---

## Privacy & Security

Your privacy and account security are fundamental design priorities:

- **No Third-Party Telemetry**: AltHelper does not include tracking, telemetry, or remote analytics of any kind.
- **Direct Communication Only**:
  - Connections to `api.github.com` and GitHub release CDNs occur only when checking or downloading AltServer binaries.
  - Connections to Docker Hub occur only via your local Docker daemon when pulling `dadoum/anisette-v3-server`.
  - Authentication traffic is handled strictly by the local AltServer binary communicating directly with Apple servers.
- **Local Credential Storage**:
  - Storing your Apple ID and password is **completely optional**.
  - If enabled, credentials are saved solely on your local filesystem under `~/.local/share/althelper/profile.json`.
  - The configuration file is written with strict file permissions (`0600`), ensuring only your local user account can read it.
- **Password Masking**: Passwords are masked within the GUI and sanitized from the application console log.

> [!NOTE]
> For enhanced security, using an **App-Specific Password** or a dedicated Apple ID for sideloading is recommended.

---

## Prerequisites

Before running AltHelper, ensure standard iOS communication utilities and Docker are installed on your Linux distribution:

### Debian / Ubuntu / Pop!_OS / Linux Mint
```bash
sudo apt-get update
sudo apt-get install -y docker.io usbmuxd libimobiledevice6 libimobiledevice-utils
```

### Arch Linux / Manjaro
```bash
sudo pacman -S docker usbmuxd libimobiledevice
```

### Fedora
```bash
sudo dnf install -y docker usbmuxd libimobiledevice libimobiledevice-utils
```

### Post-Installation Setup
1. **Enable and start usbmuxd**:
   ```bash
   sudo systemctl enable --now usbmuxd
   ```
2. **Enable Docker and add your user to the docker group**:
   ```bash
   sudo systemctl enable --now docker
   sudo usermod -aG docker $USER
   ```
   *(Log out and log back in for group changes to take effect).*

---

## Installation & Getting Started

### Option A: AppImage (Recommended)

Pre-built AppImages are available on the [Releases](https://github.com/artcevvv/Althelper/releases) page:

1. Download `AltHelper-x86_64.AppImage` from the latest release.
2. Make it executable:
   ```bash
   chmod +x AltHelper-x86_64.AppImage
   ```
3. Run it:
   ```bash
   ./AltHelper-x86_64.AppImage
   ```

### Option B: Building from Source

#### Build Dependencies (Ubuntu/Debian)
```bash
sudo apt-get install -y golang gcc libgl1-mesa-dev xorg-dev
```

#### Compilation
```bash
git clone https://github.com/artcevvv/Althelper.git
cd Althelper
go mod tidy
go build -ldflags="-s -w" -o althelper .
./althelper
```

---

## Step-by-Step Usage Guide

AltHelper is organized sequentially into five straightforward steps:

### 1. Anisette Server
- Click **Start** to spawn the Docker container (`dadoum/anisette-v3-server`).
- AltHelper will check port forwarding on `:6969` and run an HTTP healthcheck to verify that machine provisioning is active.

### 2. AltServer Binary
- Click **Download Binary** to fetch the patched architecture-specific `AltServer-Linux` binary directly from GitHub.
- Alternatively, click **Browse Binary...** to select an existing executable.
- The path is automatically remembered for future sessions.

### 3. Target Device
- Connect your iPhone or iPad via USB cable.
- Unlock the screen and tap **Trust This Computer** if prompted.
- Click **Refresh Devices** to populate the device dropdown.
- *(Optional)* Check **Install over Wi-Fi** if you have a running `netmuxd` instance.

### 4. Apple ID & App
- Enter your Apple ID email and password.
- Check **Remember Apple ID profile** if you want to store credentials in your local user config.
- Click **Choose .ipa File...** to select the target iOS package.

### 5. Installation
- Click **Install to Device**.
- If two-factor authentication is active on your Apple ID, a modal dialog will prompt for the 6-digit verification code.
- Follow progress in the **Live Activity Log** panel on the right.

---

## Troubleshooting & Caveats

- **Device not showing up in dropdown**:
  Run `idevice_id -l` in a terminal. If it returns blank, check physical cable connections, unlock your device, and verify `systemctl status usbmuxd`.
- **Anisette port conflicts**:
  If port `6969` is already in use by another service on your machine, terminate the conflicting process or container prior to clicking **Start**.
- **Apple ID 2FA 401 errors**:
  Ensure the 6-digit verification code is entered promptly when the modal appears. If using a secondary Apple ID, ensure account terms have been accepted by logging into [appleid.apple.com](https://appleid.apple.com) in a browser at least once.

---

## License

This project is licensed under the [MIT License](LICENSE).
