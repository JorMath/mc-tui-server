# mc-tui-server

A terminal UI (TUI) written in Go to manage local Minecraft servers: create
instances, start/stop/restart them, watch the live console, send commands,
edit `server.properties`, manage worlds and plugins, and install content
from Modrinth — all from one screen.

Built with [go-tui](https://github.com/grindlemire/go-tui).

## Features

- **Lifecycle** — start, stop (graceful `stop` + kill fallback) and restart
  instances with one key. Crashes are detected (shown in red) and, with
  auto-restart enabled per instance (`a`), the server restarts itself after
  5s — giving up after 3 crashes in 10 minutes. Starting also checks that
  your Java is new enough for the instance's Minecraft version.
- **Players online** — running instances are pinged via the server-list
  protocol every 5s; the sidebar shows `players N/M` next to CPU/RAM.
- **World backups** — `b` zips the active world into `backups/` with a
  timestamp; the file manager's Backups tab restores or deletes them.
- **Interactive console** — live server log with a command bar to send
  commands straight to the server's stdin.
- **Metrics** — per-instance CPU and RAM usage refreshed every 500ms.
- **Version selector** — create instances on Vanilla (Mojang), Paper,
  Purpur, Fabric, Forge, NeoForge or Quilt. Jar-based servers download
  straight from the official APIs; Forge/NeoForge/Quilt run their official
  installer inside the instance automatically.
- **File manager** — edit `server.properties` (comments preserved), list
  and delete worlds and plugins/mods safely.
- **Modrinth** — search content filtered by your instance's loader and game
  version and install it with one key. Each instance only offers what it
  supports: mods on Fabric/Forge/NeoForge/Quilt, plugins on Paper/Purpur,
  and datapacks on every type (installed into the active world); `Tab`
  switches the content type. Press `u` to update every installed mod or
  plugin to its latest compatible version (matched by file hash).
- **Modpacks** — create a new instance from a Modrinth modpack (`.mrpack`)
  on any loader: Fabric, Forge, NeoForge or Quilt. The wizard downloads the
  pack's server files and overrides, then sets up the loader runtime —
  Fabric's server launcher directly, or by running the official
  Forge/NeoForge/Quilt installer (needs Java, which you already have).
- **Multi-instance** — manage as many local servers as you want; configs
  are stored as JSON in your user config directory. Rename, delete (with
  confirmation) or change the memory of an instance right from the sidebar.

## Requirements

- **Java** on your PATH (or set `java_path` per instance) — the app manages
  server processes but does not install Java.
- A terminal with truecolor support and a font that includes block
  characters (Windows Terminal, or any modern Linux terminal). The layout
  adapts to the window size on the fly — sidebar, splash and hint bar
  scale down on small terminals.

## Install

### Prebuilt binaries

Grab the binary for your platform from the
[Releases page](https://github.com/JorMath/mc-tui-server/releases), put it
somewhere on your PATH and run it:

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
| `s` / `x` / `r` | start / stop / restart |
| `c` or `Enter` | command bar (send commands to the server) |
| `e` | files panel: properties · worlds · plugins (`1/2/3` or `Tab` to switch) |
| `m` | Modrinth search & install (mods/plugins/datapacks per instance type; `Tab` switches) |
| `n` | new instance wizard (type → version → name → memory → EULA); pick `modpack (Modrinth)` to install a modpack (Fabric/Forge/NeoForge/Quilt) |
| `M` | change the instance's memory (MB, applies on next start) |
| `a` | toggle auto-restart on crash for the instance (shown as `↻`) |
| `b` | back up the active world into `backups/` (server must be stopped) |
| `R` | rename the selected instance (server must be stopped) |
| `d` | delete the selected instance and all its files, after confirmation |
| `q` | quit (running servers are stopped gracefully) |

Every screen shows its active keys in the footer, highlighted in cyan.

### Data locations

- Instance registry: `%APPDATA%\mc-tui-server\instances.json` (Windows) or
  `~/.config/mc-tui-server/instances.json` (Linux).
- Servers created by the wizard: `<config dir>/mc-tui-server/servers/<name>/`.

## Development

```bash
tui generate ./...   # regenerate app_gsx.go after editing app.gsx
go test ./...        # all internal packages are kept at 100% coverage
go vet ./...
```
