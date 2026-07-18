# mc-tui-server

A terminal UI (TUI) written in Go to manage local Minecraft servers: create
instances, start/stop/restart them, watch the live console, send commands,
edit `server.properties`, manage worlds and plugins, and install content
from Modrinth — all from one screen.

Built with [go-tui](https://github.com/grindlemire/go-tui).

## Features

- **Lifecycle** — start, stop (graceful `stop` + kill fallback) and restart
  instances with one key.
- **Interactive console** — live server log with a command bar to send
  commands straight to the server's stdin.
- **Metrics** — per-instance CPU and RAM usage refreshed every 500ms.
- **Version selector** — create instances by downloading the server jar
  from the official Vanilla (Mojang), Paper, Purpur or Fabric APIs.
- **File manager** — edit `server.properties` (comments preserved), list
  and delete worlds and plugins/mods safely.
- **Modrinth** — search plugins/mods filtered by your instance's loader and
  game version, and install them with one key.
- **Multi-instance** — manage as many local servers as you want; configs
  are stored as JSON in your user config directory. Rename or delete
  instances (with confirmation) right from the sidebar.

## Requirements

- **Java** on your PATH (or set `java_path` per instance) — the app manages
  server processes but does not install Java.
- A terminal with truecolor support and a font that includes block
  characters (Windows Terminal, or any modern Linux terminal).

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
| `m` | Modrinth search & install |
| `n` | new instance wizard (type → version → name → memory → EULA) |
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
