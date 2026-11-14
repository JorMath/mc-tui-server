# mc-tui-server

A terminal UI (TUI) written in Go to manage local Minecraft servers: create
instances, start/stop/restart them, watch the live console, send commands,
edit `server.properties`, manage worlds and plugins, and install content
from Modrinth — all from one screen.

Built with [go-tui](https://github.com/grindlemire/go-tui).

## Features

- **Lifecycle** — start, stop (graceful `stop` + kill fallback) and restart
  instances with one key. Crashes are detected (shown in red) and, with
  auto-restart enabled per instance (`Enter` → actions menu → Auto-restart),
  the server restarts itself after 5s — giving up after 3 crashes in
  10 minutes.
- **Java runtime** — starting a server checks that your Java is new enough
  for the instance's Minecraft version. If it isn't, open the actions menu
  (`Enter`) and choose Java runtime to pick a runtime per instance: the
  system Java on PATH, a Temurin JRE the app
  downloads once per major version (shared by every instance that needs it),
  or a manual path. The wizard offers the same choice when the system Java
  can't run the version you're creating.
- **Players online** — running instances are pinged via the server-list
  protocol every 5s; the sidebar shows `players N/M` next to CPU/RAM.
- **World backups** — the actions menu (`Enter`) zips the active world into
  `backups/` with a timestamp; the file manager's Backups tab restores or
  deletes them. From the same menu you can schedule automatic backups every
  N hours (done safely while running via `save-off`/`save-on`), cap how many
  backups to keep, and set a daily restart — players get in-game warnings 5
  and 1 minutes before.
- **Players** — the actions menu (`Enter`) opens the whitelist, ops and bans
  editor per instance: live commands when the server runs, direct JSON edits
  (with the right UUID for online or offline mode) when it's stopped.
- **Interactive console** — live server log with a command bar to send
  commands straight to the server's stdin. `PgUp`/`PgDn` scroll back
  through the log (`End` resumes following), and the file manager's Logs
  tab opens past logs, including gzipped ones.
- **Metrics** — per-instance CPU and RAM usage refreshed every 500ms.
- **Version selector** — create instances on Vanilla (Mojang), Paper,
  Purpur, Fabric, Forge, NeoForge or Quilt. Jar-based servers download
  straight from the official APIs; Forge/NeoForge/Quilt run their official
  installer inside the instance automatically. You can also import an
  existing server folder — launch mode and type are detected, files stay
  where they are.
- **Server updates** — Update server software in the actions menu moves a
  jar-based instance (Vanilla, Paper, Purpur, Fabric) to another Minecraft
  version: it keeps the world and config, backs the world up first, then
  swaps the jar and optionally restarts. Downgrades are warned about.
- **File manager** — edit `server.properties` (comments preserved), list
  and delete worlds and plugins/mods safely.
- **Modrinth** — search content filtered by your instance's loader and game
  version and install it with one key. Each instance only offers what it
  supports: mods on Fabric/Forge/NeoForge/Quilt, plugins on Paper/Purpur,
  and datapacks on every type (installed into the active world); `Tab`
  switches the content type. Installing a mod also pulls in its required
  dependencies (Fabric API and friends) automatically, and `v` lets you
  pick a specific version instead of the latest. Press `u` to check every
  installed mod or plugin for newer compatible versions (matched by file
  hash) and update them all after confirming — the confirmation lists each
  change with its version and changelog.
- **Modpacks** — create a new instance from a Modrinth modpack (`.mrpack`)
  on any loader: Fabric, Forge, NeoForge or Quilt. The wizard downloads the
  pack's server files and overrides, then sets up the loader runtime —
  Fabric's server launcher directly, or by running the official
  Forge/NeoForge/Quilt installer (the app provisions the Java runtime it
  needs if your system Java is too old).
  When the pack publishes a new version, Update server software in the
  actions menu updates the instance in place:
  world backup first, then an index diff (changed files re-downloaded,
  removed ones deleted) and a loader reinstall if it changed.
- **Multi-instance** — manage as many local servers as you want; configs
  are stored as JSON in your user config directory. Rename, delete (with
  confirmation) or change the memory of an instance from the actions menu.

## Requirements

- **Java** on your PATH (or pick a managed Temurin JRE per instance from
  the actions menu → Java runtime) — the app downloads the runtime it needs
  from Adoptium. Managed runtimes are Eclipse Temurin, licensed under GPLv2
  with the Classpath Exception.
- A terminal with truecolor support and a font that includes block
  characters (Windows Terminal, or any modern Linux terminal). The layout
  adapts to the window size on the fly — sidebar, splash and hint bar
  scale down on small terminals. The mouse works too: click instances and
  tabs, scroll lists and the console with the wheel.

## Install

### Scoop (Windows)

```powershell
scoop install https://raw.githubusercontent.com/JorMath/mc-tui-server/main/packaging/scoop/mc-tui-server.json
```

### Prebuilt binaries

Grab the binary for your platform from the
[Releases page](https://github.com/JorMath/mc-tui-server/releases) (each
release ships a `checksums.txt`), put it somewhere on your PATH and run it:

```
mc-tui-server_windows_amd64.exe   # Windows
mc-tui-server_linux_amd64         # Linux x86-64
mc-tui-server_linux_arm64         # Linux ARM64
```

### With Go

```bash
go install github.com/JorMath/mc-tui-server@latest
```

### From source

```bash
git clone https://github.com/JorMath/mc-tui-server
cd mc-tui-server
go build -o mc-tui-server .
```

If you edit `app.gsx` you need the gsx compiler to regenerate the Go code:

```bash
go install github.com/grindlemire/go-tui/cmd/tui@latest
tui generate ./...
```

### Cross-compile release binaries

```powershell
# Windows
powershell -ExecutionPolicy Bypass -File scripts\build.ps1
```

```bash
# Linux / macOS
./scripts/build.sh
```

Binaries land in `dist/` for windows-amd64, linux-amd64 and linux-arm64,
with the version injected from `git describe`.

## Usage

```
mc-tui-server            # launch the TUI
mc-tui-server -version   # print the version and exit
```

### Keys

| Key | Action |
| --- | ------ |
| `↑/↓` or `j/k` | select instance |
| `Enter` | open the actions menu for the selected instance |
| `s` / `x` / `r` | start / stop / restart |
| `c` | command bar (send commands to the server) |
| `n` | new instance wizard (type → version → name → memory → Java when needed → EULA); pick `modpack (Modrinth)` to install a modpack (Fabric/Forge/NeoForge/Quilt) |
| `PgUp/PgDn · End` | scroll the console / follow the live log |
| `?` | help overlay with every shortcut |
| `q` | quit (running servers are stopped gracefully) |

The actions menu groups everything else into four sections: **Lifecycle**
(start, stop, restart, send command), **Content** (files, Modrinth,
players), **Maintenance** (backup world, scheduler, update server software,
Java runtime) and **Instance** (auto-restart, clone, rename, memory,
delete). Move with `↑/↓` or `j/k`, run the selected action with `Enter` and
close with `Esc`. Actions that require a stopped server stay selectable and
show the reason (for example "Stop the server first") instead of running.

Every screen shows its active keys in the footer, highlighted in cyan.

### Data locations

- Instance registry: `%APPDATA%\mc-tui-server\instances.json` (Windows) or
  `~/.config/mc-tui-server/instances.json` (Linux).
- Servers created by the wizard: `<config dir>/mc-tui-server/servers/<name>/`.
- Managed Java runtimes: `<config dir>/mc-tui-server/java/temurin-<major>/`
  (one per Java major, shared by every instance that uses it).
- Downloaded Temurin builds come from the Adoptium API and are verified
  against the published SHA-256 before extraction.

## Development

```bash
tui generate ./...   # regenerate app_gsx.go after editing app.gsx
go test ./...        # all internal packages are kept at 100% coverage
go vet ./...
```
