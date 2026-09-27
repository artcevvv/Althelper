# AltHelper

A small Linux GUI that wraps the fiddly parts of sideloading an iOS app via
AltServer + your Apple ID

1. Starts (or checks) an **anisette-v3-server** Docker container on port
   `6969`, needed for Apple ID sign-in.
2. Downloads (or lets you point at) a **patched AltServer-Linux** binary —
   the stock `NyaMisty/AltServer-Linux` release is unmaintained and breaks
   on current iOS due to two separate bugs: Apple blocking the hardcoded
   `com.apple.dt.Xcode` client identifier during sign-in (since Sept 2026),
   and old `ldid` signing that crashes apps on launch on iOS 26.4+. The
   default repo pointed to here (`jaakkopalvaila/AltServer-Linux`, `ng`
   branch) fixes both.
3. Lists devices over USB (via `idevice_id`), and installs your `.ipa` with
   the right environment variables set for you automatically.

It does **not** try to reimplement AltServer/AltSign itself — it just
automates running the existing tools correctly, since almost every install
failure in this ecosystem comes from a missing env var, a stale binary, or
a wrong daemon running, not from anything genuinely broken beyond that.

## Prerequisites

Install these first (Debian/Ubuntu example — adjust for your distro):

```
sudo apt-get install docker.io usbmuxd libimobiledevice6 libimobiledevice-utils
```

- **Docker**: runs the anisette server. Make sure your user can run
  `docker` without `sudo` (add yourself to the `docker` group and
  re-login), or run the whole GUI app with `sudo`.
- **usbmuxd**: should already be running as a system service
  (`systemctl status usbmuxd`) for USB installs. You do **not** need
  netmuxd for a plain USB install — only for Wi-Fi installs, and that's a
  separate manual setup this tool doesn't automate (see "Wi-Fi mode"
  below).
- **libimobiledevice-utils**: provides `idevice_id`, used to list
  connected devices.
- **Go 1.21+** and the Fyne build dependencies, only needed to *build* the
  app (see below):

  ```
  sudo apt-get install golang gcc libgl1-mesa-dev xorg-dev
  ```

## Building

```
git clone <this project>   # or just unzip it
cd althelper
go mod tidy      # fetches Fyne and pins exact versions — needs internet
go build -o althelper .
./althelper
```

`go mod tidy` needs to reach `proxy.golang.org`; if your network blocks
that, set `GOPROXY=direct` first, or run it somewhere with normal internet
access and just copy the resulting binary over.

## Using it

The window is laid out in the order you'd actually do this by hand:

1. **Anisette server** — click "Start anisette (Docker)". This runs
   `dadoum/anisette-v3-server` in a container named `althelper-anisette`
   and waits until port 6969 answers. If Docker isn't installed or you
   don't have permission to run it, you'll get an error in the log at the
   bottom instead of a silent hang.
2. **AltServer-Linux binary** — click "Download patched AltServer". It
   looks up the latest GitHub release of the repo in the text box above
   it (defaults to `jaakkopalvaila/AltServer-Linux`) and grabs the asset
   matching your CPU architecture. **This fork situation is fluid** —
   forks of forks are common in this space and repos disappear or get
   renamed. If the download fails, check that repo's Releases page in a
   browser, grab the right binary by hand, and use "Browse for existing
   binary..." instead. Either way you end up with a path shown under
   "AltServer binary:".
3. **Device** — plug your iPhone in over USB, unlock it, tap **Trust**
   if prompted, then click "Refresh devices". If nothing shows up, run
   `idevice_id -l` yourself in a terminal first — if that's empty too,
   this app can't help you until that's fixed (re-pairing, unlocking the
   phone, etc.).
4. **Apple ID & app** — enter your Apple ID email/password (if you have
   two-factor on, some setups expect an app-specific password — try your
   normal one first, since patched AltSign generally handles the 2FA
   prompt itself) and pick the `.ipa` file.
5. Click **Install to device** and watch the log pane. It streams
   AltServer's own debug output (`-d -d`) directly, so if something fails
   you'll see AltServer's real error message, not just a generic alert.

### Wi-Fi mode

The checkbox under Device exists for completeness, but this app doesn't
set up `netmuxd` for you — that still needs to be running separately,
listening at the address you enter (default `127.0.0.1:27015`), and your
phone needs to have had Wi-Fi sync turned on at least once from a real
Finder/iTunes session. If you're not sure why this exists, you almost
certainly want USB mode (leave the box unchecked).

## Security notes

- Your Apple ID password is passed as a plain command-line argument to
  the AltServer binary (that's just how AltServer's CLI works) and is
  never written to disk or logged by this app. On a shared machine, be
  aware that command-line arguments of other users' processes can
  sometimes be visible via `/proc` or `ps`.
- Nothing here phones home except: the GitHub API (to find the AltServer
  release), the download URL for that release's binary, and Docker Hub
  (to pull the anisette image) — plus AltServer's own connections to
  Apple's servers, which it needs regardless of this GUI.

## Known limitations / honest caveats

- This is a thin wrapper, not a reimplementation — if AltServer-Linux
  itself has a bug (or the fork it's pointed at goes stale again), this
  tool will faithfully surface that same error in its log pane rather
  than fixing it. Watch that fork's issues page if installs start failing
  again after an Apple-side change.
- The GitHub "download latest release" step assumes the target repo
  actually publishes release binaries (not just CI artifacts, which
  require GitHub auth to fetch). If that ever stops being true, use
  "Browse for existing binary..." instead.
- No Wi-Fi/netmuxd automation, no code-signing of your own — this only
  drives the existing `AltServer-Linux -u -a -p file.ipa` invocation.
